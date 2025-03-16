package async

import "path"

const (
	doEnd = iota
	doYield
	doTransit
	doBreak
	doContinue
)

const (
	flagStale = 1 << iota
	flagResumed
	flagEnded
	flagRecyclable
	flagRecycled
)

// A Coroutine is an execution of code, similar to a goroutine but cooperative
// and stackless.
//
// A Coroutine is created with a function called [Task].
// A Coroutine's job is to end the Task.
// When an [Executor] spawns a Coroutine with a Task, it runs the Coroutine by
// calling the Task function with the Coroutine as the argument.
// The return value determines whether to end the Coroutine or to yield it
// so that it could resume later.
//
// In order for a Coroutine to resume, the Coroutine must watch at least one
// [Event] (e.g. [Signal], [State] and [Memo], etc.), when calling the Task
// function.
// A notification of such an Event resumes the Coroutine.
// When a Coroutine is resumed, the Executor runs the Coroutine again.
//
// A Coroutine can also make a transit to work on another Task function
// according to the return value of the Task function.
// A Coroutine can transit from one Task to another until a Task ends it.
type Coroutine struct {
	executor *Executor
	path     string
	task     Task
	flag     uint8
	deps     map[Event]bool
	inners   []coroutineOrFunc
	outer    *Coroutine
}

type coroutineOrFunc struct {
	co *Coroutine
	f  func()
}

func (e *Executor) newCoroutine() *Coroutine {
	if co := e.pool.Get(); co != nil {
		return co.(*Coroutine)
	}
	return new(Coroutine)
}

func (e *Executor) freeCoroutine(co *Coroutine) {
	if co.flag&(flagRecyclable|flagRecycled) == flagRecyclable {
		co.executor = nil
		co.task = nil
		co.flag |= flagRecycled
		co.outer = nil
		e.pool.Put(co)
	}
}

func (co *Coroutine) init(e *Executor, p string, t Task) *Coroutine {
	co.executor = e
	co.path = p
	co.task = t
	co.flag = flagStale
	return co
}

func (co *Coroutine) recyclable() *Coroutine {
	co.flag |= flagRecyclable
	return co
}

func (co *Coroutine) less(other *Coroutine) bool {
	return co.path < other.path
}

func (e *Executor) resumeCoroutine(co *Coroutine) {
	flag := co.flag
	if flag&flagEnded != 0 {
		return
	}

	if flag&flagResumed != 0 {
		co.flag = flag | flagStale
		return
	}

	co.flag = flag | flagStale | flagResumed

	e.pq.Push(co)
}

func (e *Executor) runCoroutine(co *Coroutine) {
	flag := co.flag
	flag &^= flagResumed
	co.flag = flag

	if flag&flagEnded != 0 {
		e.freeCoroutine(co)
		return
	}

	if flag&flagStale == 0 {
		return
	}

	e.mu.Unlock()
	co.run()
	e.mu.Lock()
}

func (co *Coroutine) run() {
	{
		deps := co.deps
		for d := range deps {
			deps[d] = false
			d.pauseListener(co)
		}
	}

	var res Result

	for {
		co.clearInners()

		co.flag &^= flagStale | flagEnded

		res = co.task(co)

		if res.transitTo != nil {
			co.task = res.transitTo
		}

		if res.action != doTransit {
			break
		}

		co.clearDeps()
	}

	if res.action == doYield {
		deps := co.deps
		for d, inUse := range deps {
			if !inUse {
				delete(deps, d)
				d.removeListener(co)
			}
		}
	}

	if res.action != doYield || len(co.deps) == 0 && len(co.inners) == 0 {
		co.end()
	}

	switch res.action {
	case doEnd, doYield:
	case doTransit:
		panic("async(Coroutine): unhandled switch action (internal error)")
	case doBreak:
		panic("async(Coroutine): unhandled break action")
	case doContinue:
		panic("async(Coroutine): unhandled continue action")
	default:
		panic("async(Coroutine): unknown action (internal error)")
	}
}

func (co *Coroutine) end() {
	if co.flag&flagEnded != 0 {
		return
	}

	co.flag |= flagEnded

	co.clearDeps()
	co.clearInners()

	if co.flag&flagResumed == 0 {
		co.executor.freeCoroutine(co)
	}
}

func (co *Coroutine) clearDeps() {
	deps := co.deps
	for d := range deps {
		delete(deps, d)
		d.removeListener(co)
	}
}

func (co *Coroutine) clearInners() {
	inners := co.inners
	co.inners = inners[:0]

	for i := len(inners) - 1; i >= 0; i-- {
		switch v := inners[i]; {
		case v.co != nil:
			// v.co could have been ended and recycled.
			// We need the following check to confirm that v.co is still an inner Coroutine of co.
			if v.co.outer == co {
				v.co.end()
			}
		case v.f != nil:
			v.f()
		}
	}

	clear(inners)
}

// Executor returns the [Executor] that spawned co.
//
// Since co can be recycled by an Executor, it is recommended to save
// the return value in a variable first.
func (co *Coroutine) Executor() *Executor {
	return co.executor
}

// Path returns the path of co.
//
// Since co can be recycled by an Executor, it is recommended to save
// the return value in a variable first.
func (co *Coroutine) Path() string {
	return co.path
}

// Watch watches some Events so that, when any of them notifies, co resumes.
func (co *Coroutine) Watch(ev ...Event) {
	deps := co.deps
	if deps == nil {
		deps = make(map[Event]bool)
		co.deps = deps
	}

	for _, d := range ev {
		deps[d] = true
		d.addListener(co)
	}
}

// Cleanup adds a function call when co resumes or ends, or when co is
// transiting to work on another [Task].
func (co *Coroutine) Cleanup(f func()) {
	co.inners = append(co.inners, coroutineOrFunc{f: f})
}

// Spawn creates an inner [Coroutine] to work on t, using the result of
// path.Join(co.Path(), p) as its path.
//
// Inner Coroutines are ended automatically when the outer one resumes or
// ends, or when the outer one is making a transit to work on another Task.
func (co *Coroutine) Spawn(p string, t Task) {
	inner := co.executor.newCoroutine().init(co.executor, path.Join(co.path, p), t).recyclable()
	inner.run()

	if inner.flag&flagEnded == 0 {
		inner.outer = co
		co.inners = append(co.inners, coroutineOrFunc{co: inner})
	}
}

// Result is the type of the return value of a [Task] function.
// A Result determines what next for a [Coroutine] to do after calling
// a Task function.
//
// A Result can be created by calling one of the following methods of
// Coroutine:
//   - [Coroutine.End]: for ending a Coroutine;
//   - [Coroutine.Await]: for yielding a Coroutine with additional Events to
//     watch;
//   - [Coroutine.Yield]: for yielding a Coroutine with another Task to which
//     will be transited later when resuming;
//   - [Coroutine.Transit]: for transiting to another Task.
type Result struct {
	action    int
	label     Label
	transitTo Task
}

// End returns a [Result] that will cause co to end or make a transit to work
// on another [Task].
func (co *Coroutine) End() Result {
	return Result{action: doEnd}
}

// Await returns a [Result] that will cause co to yield.
// Await also accepts additional Events to watch.
func (co *Coroutine) Await(ev ...Event) Result {
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return Result{action: doYield}
}

// Yield returns a [Result] that will cause co to yield and, when co is resumed,
// make a transit to work on t instead.
func (co *Coroutine) Yield(t Task) Result {
	if t == nil {
		panic("async(Coroutine): undefined behavior: Yield(nil)")
	}
	return Result{action: doYield, transitTo: t}
}

// Transit returns a [Result] that will cause co to make a transit to work on t.
func (co *Coroutine) Transit(t Task) Result {
	if t == nil {
		panic("async(Coroutine): undefined behavior: Transit(nil)")
	}
	return Result{action: doTransit, transitTo: t}
}

// Break returns a [Result] that will cause co to break a loop.
func (co *Coroutine) Break() Result {
	return co.BreakLabel(NoLabel)
}

// BreakLabel returns a [Result] that will cause co to break a loop with label
// l.
func (co *Coroutine) BreakLabel(l Label) Result {
	return Result{action: doBreak, label: l}
}

// Continue returns a [Result] that will cause co to continue a loop.
func (co *Coroutine) Continue() Result {
	return co.ContinueLabel(NoLabel)
}

// ContinueLabel returns a [Result] that will cause co to continue a loop with
// label l.
func (co *Coroutine) ContinueLabel(l Label) Result {
	return Result{action: doContinue, label: l}
}

// A Task is a piece of work that a [Coroutine] is given to do when it is
// spawned.
// The return value of a Task, a [Result], determines what next for a Coroutine
// to do.
//
// The argument co must not escape, because co can be recycled by an [Executor]
// when co ends.
type Task func(co *Coroutine) Result

// Then returns a [Task] that first works on t, then next after t ends.
//
// To chain multiple Tasks, use [Block] function.
func (t Task) Then(next Task) Task {
	if next == nil {
		panic("async(Task): undefined behavior: Then(nil)")
	}
	return func(co *Coroutine) Result {
		return co.Transit(t.then(next))
	}
}

func (t Task) then(next Task) Task {
	return func(co *Coroutine) Result {
		switch res := t(co); res.action {
		case doEnd:
			return Result{action: doTransit, transitTo: next}
		case doYield, doTransit:
			if res.transitTo != nil {
				t = res.transitTo
			}
			return Result{action: res.action}
		default:
			return res
		}
	}
}

// Do returns a [Task] that calls f, and then ends.
func Do(f func()) Task {
	return func(co *Coroutine) Result {
		f()
		return co.End()
	}
}

// End returns a [Task] that ends without doing anything.
func End() Task {
	return (*Coroutine).End
}

// Await returns a [Task] that awaits some Events until any of them notifies,
// and then ends.
// If ev is empty, Await returns a [Task] that never ends.
func Await(ev ...Event) Task {
	return func(co *Coroutine) Result {
		if len(ev) != 0 {
			co.Watch(ev...)
		}
		return co.Yield(End())
	}
}

// Block returns a [Task] that runs each of the provided Tasks in sequence.
// When one Task ends, Block runs another.
func Block(s ...Task) Task {
	return func(co *Coroutine) Result {
		return co.Transit(chain(s...))
	}
}

func chain(s ...Task) Task {
	var t Task
	return func(co *Coroutine) Result {
		if t == nil {
			if len(s) == 0 {
				return co.End()
			}
			t, s = s[0], s[1:]
		}
		switch res := t(co); res.action {
		case doEnd:
			t = nil
			return Result{action: doTransit}
		case doYield, doTransit:
			if res.transitTo != nil {
				t = res.transitTo
			}
			return Result{action: res.action}
		default:
			return res
		}
	}
}

// Break returns a [Task] that breaks a loop.
func Break() Task {
	return (*Coroutine).Break
}

// BreakLabel returns a [Task] that breaks a loop with label l.
func BreakLabel(l Label) Task {
	return func(co *Coroutine) Result {
		return co.BreakLabel(l)
	}
}

// Continue returns a [Task] that continues a loop.
func Continue() Task {
	return (*Coroutine).Continue
}

// ContinueLabel returns a [Task] that continues a loop with label l.
func ContinueLabel(l Label) Task {
	return func(co *Coroutine) Result {
		return co.ContinueLabel(l)
	}
}

const NoLabel Label = ""

type Label string

// Loop returns a [Task] that forms a loop, which would run t repeatedly.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
func Loop(t Task) Task {
	return LoopLabel(NoLabel, t)
}

// LoopN returns a [Task] that forms a loop, which would run t repeatedly
// for n times.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
func LoopN(n int, t Task) Task {
	return LoopLabelN(NoLabel, n, t)
}

// LoopLabel returns a [Task] that forms a loop with label l, which would
// run t repeatedly.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
// Both [Coroutine.BreakLabel] and [BreakLabel], with label l, can
// break this loop early.
// Both [Coroutine.ContinueLabel] and [ContinueLabel], with label l, can
// continue this loop early.
func LoopLabel(l Label, t Task) Task {
	return func(co *Coroutine) Result {
		return co.Transit(loop(l, t))
	}
}

// LoopLabelN returns a [Task] that forms a loop with label l, which would
// run t repeatedly for n times.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
// Both [Coroutine.BreakLabel] and [BreakLabel], with label l, can
// break this loop early.
// Both [Coroutine.ContinueLabel] and [ContinueLabel], with label l, can
// continue this loop early.
func LoopLabelN(l Label, n int, t Task) Task {
	return func(co *Coroutine) Result {
		i := 0
		return co.Transit(loop(l, func(co *Coroutine) Result {
			if i < n {
				i++
				return co.Transit(t)
			}
			return co.Break()
		}))
	}
}

func loop(l Label, t Task) Task {
	t0 := t
	return func(co *Coroutine) Result {
		switch res := t(co); res.action {
		case doEnd:
			t = t0
			return Result{action: doTransit}
		case doYield, doTransit:
			if res.transitTo != nil {
				t = res.transitTo
			}
			return Result{action: res.action}
		case doBreak:
			switch res.label {
			case l, NoLabel:
				return co.End()
			}
			return res
		case doContinue:
			switch res.label {
			case l, NoLabel:
				t = t0
				return Result{action: doTransit}
			}
			return res
		default:
			return res
		}
	}
}
