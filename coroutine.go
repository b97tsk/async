package async

import "path"

const (
	doEnd = iota
	doYield
	doTransit
	doBreak
	doContinue
	doReturn
	doExit
	doTailTransit // Transit and remove controller.
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
// A coroutine is created with a function called [Task].
// A coroutine's job is to end the task.
// When an [Executor] spawns a coroutine with a task, it runs the coroutine by
// calling the task function with the coroutine as the argument.
// The return value determines whether to end the coroutine or to yield it
// so that it could resume later.
//
// In order for a coroutine to resume, the coroutine must watch at least one
// [Event] (e.g. [Signal], [State] and [Memo], etc.), when calling the task
// function.
// A notification of such an event resumes the coroutine.
// When a coroutine is resumed, the executor runs the coroutine again.
//
// A coroutine can also make a transit to work on another task according to
// the return value of the task function.
// A coroutine can transit from one task to another until a task ends it.
type Coroutine struct {
	executor    *Executor
	path        string
	task        Task
	flag        uint8
	deps        map[Event]bool
	inners      []coroutineOrFunc
	outer       *Coroutine
	defers      []Task
	controllers []controller
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
	var anyPaused bool

	{
		deps := co.deps
		for d := range deps {
			deps[d] = false
			d.pauseListener(co)
		}
		anyPaused = len(deps) != 0
	}

	var res Result

	pc := &co.executor.pc

	for {
		co.clearInners()

		co.flag &^= flagStale | flagEnded

		if !pc.TryCatch(func() { res = co.task(co) }) {
			res = co.Exit()
		}

		if res.action != doYield && res.action != doTransit {
			controllers := co.controllers
			for len(controllers) != 0 {
				i := len(controllers) - 1
				res = controllers[i].negotiate(co, res)
				if res.action != doTransit {
					controllers[i] = nil
					controllers = controllers[:i]
					co.controllers = controllers
				}
				if res.action == doTransit || res.action == doTailTransit {
					break
				}
			}
			if res.action != doTransit && res.action != doTailTransit {
				res = rootController.negotiate(co, res)
			}
			if res.action == doTailTransit {
				res.action = doTransit
			}
		}

		if res.transitTo != nil {
			co.task = res.transitTo
		}

		if res.action != doTransit {
			break
		}

		if res.controller != nil {
			addController := true
			if _, ok := res.controller.(*funcController); ok {
				lastController := rootController
				if n := len(co.controllers); n != 0 {
					lastController = co.controllers[n-1]
				}
				if _, ok := lastController.(*funcController); ok {
					// Tail-call optimization:
					// If the last controller is also a funcController, do not add another one.
					// (doTailTransit also pays tribute to this optimization.)
					addController = false
				}
			}
			if addController {
				co.controllers = append(co.controllers, res.controller)
				if maxCapSize := 1000000; cap(co.controllers) > maxCapSize {
					panic("async: too many controllers or recursions")
				}
			}
		}

		co.clearDeps()
		anyPaused = false
	}

	if res.action == doYield && anyPaused {
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

	pc := &co.executor.pc

	for i := len(inners) - 1; i >= 0; i-- {
		switch v := inners[i]; {
		case v.co != nil:
			// v.co could have been ended and recycled.
			// We need the following check to confirm that v.co is still an inner coroutine of co.
			if v.co.outer == co {
				v.co.end()
			}
		case v.f != nil:
			pc.TryCatch(v.f)
		}
	}

	clear(inners)
}

// Executor returns the executor that spawned co.
//
// Since co can be recycled by an executor, it is recommended to save
// the return value in a variable first.
func (co *Coroutine) Executor() *Executor {
	return co.executor
}

// Path returns the path of co.
//
// Since co can be recycled by an executor, it is recommended to save
// the return value in a variable first.
func (co *Coroutine) Path() string {
	return co.path
}

// Watch watches some events so that, when any of them notifies, co resumes.
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

// Cleanup adds a function call when co resumes or ends, or when co is making
// a transit to work on another [Task].
func (co *Coroutine) Cleanup(f func()) {
	co.inners = append(co.inners, coroutineOrFunc{f: f})
}

// Defer adds a [Task] for execution when returning from a [Func].
// Deferred tasks are executed in last-in-first-out (LIFO) order.
func (co *Coroutine) Defer(t Task) {
	co.defers = append(co.defers, must(t))
}

// Spawn creates an inner coroutine to work on t, using the result of
// path.Join(co.Path(), p) as its path.
//
// Inner coroutines are ended automatically when the outer one resumes or
// ends, or when the outer one is making a transit to work on another task.
func (co *Coroutine) Spawn(p string, t Task) {
	inner := co.executor.newCoroutine().init(co.executor, path.Join(co.path, p), t).recyclable()
	inner.run()

	if inner.flag&flagEnded == 0 {
		inner.outer = co
		co.inners = append(co.inners, coroutineOrFunc{co: inner})
	}
}

// Result is the type of the return value of a [Task] function.
// A Result determines what next for a coroutine to do after running a task.
//
// A Result can be created by calling one of the following methods:
//   - [Coroutine.End]: for ending a coroutine;
//   - [Coroutine.Await]: for yielding a coroutine with additional events to
//     watch;
//   - [Coroutine.Yield]: for yielding a coroutine with another task to which
//     will be transited later when resuming;
//   - [Coroutine.Transit]: for transiting to another task.
type Result struct {
	action     int
	label      Label      // used by: doBreak, doContinue
	transitTo  Task       // used by: doYield, doTransit, doTailTransit
	controller controller // used by: doTransit
}

// End returns a [Result] that will cause co to end or make a transit to work
// on another [Task].
func (co *Coroutine) End() Result {
	return Result{action: doEnd}
}

// Await returns a [Result] that will cause co to yield.
// Await also accepts additional events to watch.
func (co *Coroutine) Await(ev ...Event) Result {
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return Result{action: doYield}
}

// Yield returns a [Result] that will cause co to yield and, when co is resumed,
// make a transit to work on t instead.
func (co *Coroutine) Yield(t Task) Result {
	return Result{action: doYield, transitTo: must(t)}
}

// Transit returns a [Result] that will cause co to make a transit to work on t.
func (co *Coroutine) Transit(t Task) Result {
	return Result{action: doTransit, transitTo: must(t)}
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

// Return returns a [Result] that will cause co to return from a [Func].
func (co *Coroutine) Return() Result {
	return Result{action: doReturn}
}

// Exit returns a [Result] that will cause co to exit.
// All deferred tasks will be run before co exits.
func (co *Coroutine) Exit() Result {
	return Result{action: doExit}
}

type controller interface {
	negotiate(co *Coroutine, res Result) Result
}

// A Task is a piece of work that a coroutine is given to do when it is spawned.
// The return value of a task, a [Result], determines what next for a coroutine
// to do.
//
// The argument co must not escape, because co can be recycled by an [Executor]
// when co ends.
type Task func(co *Coroutine) Result

// Then returns a [Task] that first works on t, then next after t ends.
//
// To chain multiple tasks, use [Block] function.
func (t Task) Then(next Task) Task {
	return func(co *Coroutine) Result {
		return Result{
			action:     doTransit,
			transitTo:  must(t),
			controller: newThenController(next),
		}
	}
}

type thenController struct {
	next Task
}

func newThenController(next Task) controller {
	return &thenController{must(next)}
}

func (c *thenController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doEnd:
		return Result{action: doTailTransit, transitTo: c.next}
	default:
		return res
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

// Await returns a [Task] that awaits some events until any of them notifies,
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

// Block returns a [Task] that runs each of the given tasks in sequence.
// When one task ends, Block runs another.
func Block(s ...Task) Task {
	switch len(s) {
	case 0:
		return End()
	case 1:
		return s[0]
	case 2:
		return s[0].Then(s[1])
	}
	return func(co *Coroutine) Result {
		return Result{
			action:     doTransit,
			transitTo:  must(s[0]),
			controller: newBlockController(s[1:]),
		}
	}
}

type blockController struct {
	s []Task
}

func newBlockController(s []Task) controller {
	return &blockController{s}
}

func (c *blockController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doEnd:
		if len(c.s) == 0 {
			return co.End()
		}
		t := c.s[0]
		c.s = c.s[1:]
		action := doTransit
		if len(c.s) == 0 {
			action = doTailTransit
		}
		return Result{action: action, transitTo: must(t)}
	default:
		return res
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
		return Result{
			action:     doTransit,
			transitTo:  must(t),
			controller: newLoopController(l, t),
		}
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
		u := func(co *Coroutine) Result {
			if i < n {
				i++
				return co.Transit(t)
			}
			return co.Break()
		}
		return Result{
			action:     doTransit,
			transitTo:  u,
			controller: newLoopController(l, u),
		}
	}
}

type loopController struct {
	l Label
	t Task
}

func newLoopController(l Label, t Task) controller {
	return &loopController{l, t}
}

func (c *loopController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doEnd:
		return co.Transit(c.t)
	case doBreak:
		switch res.label {
		case c.l, NoLabel:
			return co.End()
		}
		return res
	case doContinue:
		switch res.label {
		case c.l, NoLabel:
			return co.Transit(c.t)
		}
		return res
	default:
		return res
	}
}

// Defer returns a [Task] that adds t for execution when returning from
// a [Func].
// Deferred tasks are executed in last-in-first-out (LIFO) order.
func Defer(t Task) Task {
	return func(co *Coroutine) Result {
		co.Defer(t)
		return co.End()
	}
}

// Return returns a [Task] that returns from a surrounding [Func].
func Return() Task {
	return (*Coroutine).Return
}

// Exit returns a [Task] that causes the coroutine that runs it to exit.
// All deferred tasks are run before the coroutine exits.
func Exit() Task {
	return (*Coroutine).Exit
}

// Func returns a [Task] that runs t in a function scope.
// Spawned tasks are considered surrounded by an invisible [Func].
func Func(t Task) Task {
	return func(co *Coroutine) Result {
		return Result{
			action:     doTransit,
			transitTo:  must(t),
			controller: newFuncController(len(co.defers)),
		}
	}
}

type funcController struct {
	keep int
	exit bool
}

func newFuncController(keep int) controller {
	return &funcController{keep: keep}
}

func (c *funcController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doExit:
		c.exit = true
		fallthrough
	case doEnd, doReturn:
		defers := co.defers
		if len(defers) == c.keep {
			if c.exit {
				return co.Exit()
			}
			return co.End()
		}
		i := len(defers) - 1
		t := defers[i]
		defers[i] = nil
		co.defers = defers[:i]
		return co.Transit(t)
	case doBreak:
		panic("async: unhandled break action")
	case doContinue:
		panic("async: unhandled continue action")
	default:
		panic("async: unknown action (internal error)")
	}
}

func must(t Task) Task {
	if t == nil {
		panic("async: nil Task")
	}
	return t
}

var rootController = newFuncController(0)
