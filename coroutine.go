package async

import "path"

const (
	doEnd = iota
	doYield
	doSwitch
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
// A Coroutine is created with a function called [Operation].
// A Coroutine's job is to complete it.
// When an [Executor] spawns a Coroutine, it runs the Coroutine by calling
// the Operation function with the Coroutine as the argument.
// The return value determines whether to end the Coroutine or to yield it
// so that it could resume later.
//
// In order for a Coroutine to resume, the Coroutine must watch at least
// one [Event], which must be a [Signal], a [State] or a [Memo], when calling
// the Operation function.
// A notification of such an Event resumes the Coroutine.
// When a Coroutine is resumed, the Executor runs the Coroutine again.
//
// A Coroutine can also switch to work on another Operation function according
// to the return value of the Operation function.
// A Coroutine can switch from one Operation to another until an Operation
// ends it.
type Coroutine struct {
	executor *Executor
	path     string
	op       Operation
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
		co.op = nil
		co.flag |= flagRecycled
		co.outer = nil
		e.pool.Put(co)
	}
}

func (co *Coroutine) init(e *Executor, p string, op Operation) *Coroutine {
	co.executor = e
	co.path = p
	co.op = op
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

func (co *Coroutine) resume() {
	flag := co.flag
	if flag&flagEnded != 0 {
		return
	}

	if flag&flagResumed != 0 {
		co.flag = flag | flagStale
		return
	}

	co.flag = flag | flagStale | flagResumed
	co.executor.resumeCoroutine(co)
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
		}
	}

	var res Result

	for {
		co.clearInners()

		co.flag &^= flagStale | flagEnded

		res = co.op(co)

		if res.op != nil {
			co.op = res.op
		}

		if res.action != doSwitch {
			break
		}

		co.clearDeps()
	}

	if res.action != doEnd {
		deps := co.deps
		for d, inUse := range deps {
			if !inUse {
				delete(deps, d)
				d.removeListener(co)
			}
		}
	}

	if res.action == doEnd || len(co.deps) == 0 && len(co.inners) == 0 {
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
func (co *Coroutine) Watch(s ...Event) {
	deps := co.deps
	if deps == nil {
		deps = make(map[Event]bool)
		co.deps = deps
	}

	for _, d := range s {
		if _, ok := deps[d]; ok {
			deps[d] = true
			continue
		}

		deps[d] = true
		d.addListener(co)
	}
}

// Defer adds a function call when co resumes or ends, or when co is switching
// to work on another [Operation].
func (co *Coroutine) Defer(f func()) {
	co.inners = append(co.inners, coroutineOrFunc{f: f})
}

// Spawn creates an inner [Coroutine] to work on op, using the result of
// path.Join(co.Path(), p) as its path.
//
// Inner Coroutines are ended automatically when the outer one resumes or
// ends, or when the outer one is switching to work on another Operation.
func (co *Coroutine) Spawn(p string, op Operation) {
	inner := co.executor.newCoroutine().init(co.executor, path.Join(co.path, p), op).recyclable()
	inner.run()

	if inner.flag&flagEnded == 0 {
		inner.outer = co
		co.inners = append(co.inners, coroutineOrFunc{co: inner})
	}
}

// Result is the type of the return value of an [Operation] function.
// A Result determines what next for a [Coroutine] to do after calling
// an Operation function.
//
// A Result can be created by calling one of the following method of
// Coroutine:
//   - [Coroutine.End]: for ending a Coroutine;
//   - [Coroutine.Await]: for yielding a Coroutine with additional Events to
//     watch;
//   - [Coroutine.Yield]: for yielding a Coroutine with another Operation to
//     which will be switched later when resuming;
//   - [Coroutine.Switch]: for switching to another Operation.
type Result struct {
	action int
	op     Operation
}

// End returns a [Result] that will cause co to end or switch to work on
// another [Operation] in a [Chain].
func (co *Coroutine) End() Result {
	return Result{action: doEnd}
}

// Await returns a [Result] that will cause co to yield.
// Await also accepts additional Events to be awaited for.
func (co *Coroutine) Await(s ...Event) Result {
	if len(s) != 0 {
		co.Watch(s...)
	}
	return Result{action: doYield}
}

// Yield returns a [Result] that will cause co to yield.
// op becomes the current Operation of co so that, when co is resumed, op is
// called instead.
func (co *Coroutine) Yield(op Operation) Result {
	if op == nil {
		panic("async(Coroutine): undefined behavior: Yield(nil)")
	}
	return Result{action: doYield, op: op}
}

// Switch returns a [Result] that will cause co to switch to work on op.
// co will be reset and op will be called immediately as the current Operation
// of co.
func (co *Coroutine) Switch(op Operation) Result {
	if op == nil {
		panic("async(Coroutine): undefined behavior: Switch(nil)")
	}
	return Result{action: doSwitch, op: op}
}

// An Operation is a piece of work that a [Coroutine] is given to do when it is
// spawned.
// The return value of an Operation, a [Result], determines what next for
// a Coroutine to do.
//
// The argument co must not escape, because co can be recycled by an [Executor]
// when co ends.
type Operation func(co *Coroutine) Result

// Chain returns an [Operation] that will work on each of the provided
// Operations in sequence.
// When one Operation completes, Chain works on another.
func Chain(s ...Operation) Operation {
	var op Operation
	return func(co *Coroutine) Result {
		if op == nil {
			if len(s) == 0 {
				return co.End()
			}
			op, s = s[0], s[1:]
		}
		switch res := op(co); res.action {
		case doEnd:
			op = nil
			return Result{action: doSwitch}
		case doYield, doSwitch:
			if res.op != nil {
				op = res.op
			}
			return Result{action: res.action}
		default:
			panic("async: internal error: unknown action")
		}
	}
}

// Do returns an [Operation] that calls f, and then completes.
func Do(f func()) Operation {
	return func(co *Coroutine) Result {
		f()
		return co.End()
	}
}

// Never returns an [Operation] that never completes.
// Operations in a [Chain] after Never are never getting worked on.
func Never() Operation {
	return func(co *Coroutine) Result {
		return co.Await()
	}
}

// Nop returns an [Operation] that completes without doing anything.
func Nop() Operation {
	return (*Coroutine).End
}

// Then returns an [Operation] that first works on op, then switches to
// work on next after op completes.
//
// To chain multiple Operations, use [Chain] function.
func (op Operation) Then(next Operation) Operation {
	if next == nil {
		panic("async(Operation): undefined behavior: Then(nil)")
	}
	return func(co *Coroutine) Result {
		switch res := op(co); res.action {
		case doEnd:
			return Result{action: doSwitch, op: next}
		case doYield, doSwitch:
			if res.op != nil {
				op = res.op
			}
			return Result{action: res.action}
		default:
			panic("async: internal error: unknown action")
		}
	}
}
