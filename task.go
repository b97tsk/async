package async

import "path"

const (
	doEnd = iota
	doYield
	doSwitch
)

const (
	flagStale = 1 << iota
	flagWoken
	flagEnded
	flagRecyclable
	flagRecycled
)

// A Task is an execution of code, similar to a goroutine but cooperative and
// stackless.
//
// A Task is created with a function called [Operation].
// A Task's job is to complete it.
// When an [Executor] spawns a Task, it runs the Task by calling the Operation
// function with the Task as the argument.
// The return value determines whether to end the Task or to yield it so that
// it could resume later.
//
// In order for a Task to resume, the Task must watch at least one [Event],
// which must be a [Signal], a [State] or a [Memo], when calling the Operation
// function.
// A notification of such an Event resumes the Task.
// When a Task is resumed, the Executor runs the Task again.
//
// A Task can also switch to work on another Operation function according to
// the return value of the Operation function.
// A Task can switch from one Operation to another until an Operation ends it.
type Task struct {
	executor *Executor
	path     string
	op       Operation
	flag     uint8
	deps     map[Event]bool
	inners   []taskOrFunc
	outer    *Task
}

type taskOrFunc struct {
	t *Task
	f func()
}

func (e *Executor) newTask() *Task {
	if t := e.pool.Get(); t != nil {
		return t.(*Task)
	}
	return new(Task)
}

func (e *Executor) freeTask(t *Task) {
	if t.flag&(flagRecyclable|flagRecycled) == flagRecyclable {
		t.executor = nil
		t.op = nil
		t.flag |= flagRecycled
		t.outer = nil
		e.pool.Put(t)
	}
}

func (t *Task) init(e *Executor, p string, op Operation) *Task {
	t.executor = e
	t.path = p
	t.op = op
	t.flag = flagStale
	return t
}

func (t *Task) recyclable() *Task {
	t.flag |= flagRecyclable
	return t
}

func (t *Task) less(other *Task) bool {
	return t.path < other.path
}

func (t *Task) wake() {
	flag := t.flag
	if flag&flagEnded != 0 {
		return
	}

	if flag&flagWoken != 0 {
		t.flag = flag | flagStale
		return
	}

	t.flag = flag | flagStale | flagWoken
	t.executor.resumeTask(t)
}

func (e *Executor) runTask(t *Task) {
	flag := t.flag
	flag &^= flagWoken
	t.flag = flag

	if flag&flagEnded != 0 {
		e.freeTask(t)
		return
	}

	if flag&flagStale == 0 {
		return
	}

	e.mu.Unlock()
	t.run()
	e.mu.Lock()
}

func (t *Task) run() {
	{
		deps := t.deps
		for d := range deps {
			deps[d] = false
		}
	}

	var res Result

	for {
		t.clearInners()

		t.flag &^= flagStale | flagEnded

		res = t.op(t)

		if res.op != nil {
			t.op = res.op
		}

		if res.action != doSwitch {
			break
		}

		t.clearDeps()
	}

	if res.action != doEnd {
		deps := t.deps
		for d, inUse := range deps {
			if !inUse {
				delete(deps, d)
				d.removeListener(t)
			}
		}
	}

	if res.action == doEnd || len(t.deps) == 0 && len(t.inners) == 0 {
		t.end()
	}
}

func (t *Task) end() {
	if t.flag&flagEnded != 0 {
		return
	}

	t.flag |= flagEnded

	t.clearDeps()
	t.clearInners()

	if t.flag&flagWoken == 0 {
		t.executor.freeTask(t)
	}
}

func (t *Task) clearDeps() {
	deps := t.deps
	for d := range deps {
		delete(deps, d)
		d.removeListener(t)
	}
}

func (t *Task) clearInners() {
	inners := t.inners
	t.inners = inners[:0]

	for i := len(inners) - 1; i >= 0; i-- {
		switch v := inners[i]; {
		case v.t != nil:
			// v.t could have been ended and recycled.
			// We need the following check to confirm that v.t is still an inner task of t.
			if v.t.outer == t {
				v.t.end()
			}
		case v.f != nil:
			v.f()
		}
	}

	clear(inners)
}

// Executor returns the [Executor] that spawned t.
//
// Since t can be recycled by an Executor, it is recommended to save
// the return value in a variable first.
func (t *Task) Executor() *Executor {
	return t.executor
}

// Path returns the path of t.
//
// Since t can be recycled by an Executor, it is recommended to save
// the return value in a variable first.
func (t *Task) Path() string {
	return t.path
}

// Watch watches some Events so that, when any of them notifies, t resumes.
func (t *Task) Watch(s ...Event) {
	deps := t.deps
	if deps == nil {
		deps = make(map[Event]bool)
		t.deps = deps
	}

	for _, d := range s {
		if _, ok := deps[d]; ok {
			deps[d] = true
			continue
		}

		deps[d] = true
		d.addListener(t)
	}
}

// Defer adds a function call when t resumes or ends, or when t is switching
// to work on another [Operation].
func (t *Task) Defer(f func()) {
	t.inners = append(t.inners, taskOrFunc{f: f})
}

// Spawn creates an inner [Task] to work on op, using the result of
// path.Join(t.Path(), p) as its path.
//
// Inner Tasks are ended automatically when the outer one resumes or ends, or
// when the outer one is switching to work on another Operation.
func (t *Task) Spawn(p string, op Operation) {
	inner := t.executor.newTask().init(t.executor, path.Join(t.path, p), op).recyclable()
	inner.run()

	if inner.flag&flagEnded == 0 {
		inner.outer = t
		t.inners = append(t.inners, taskOrFunc{t: inner})
	}
}

// Result is the type of the return value of an [Operation] function.
// A Result determines what next for a [Task] to do after calling an Operation
// function.
//
// A Result can be created by calling one of the following method of Task:
//   - [Task.End]: for ending a Task;
//   - [Task.Await]: for yielding a Task with additional Events to watch;
//   - [Task.Yield]: for yielding a Task with another Operation to which will
//     be switched later when resuming;
//   - [Task.Switch]: for switching to another Operation.
type Result struct {
	action int
	op     Operation
}

// End returns a [Result] that will cause t to end or switch to work on
// another [Operation] in a [Chain].
func (t *Task) End() Result {
	return Result{action: doEnd}
}

// Await returns a [Result] that will cause t to yield.
// Await also accepts additional Events to be awaited for.
func (t *Task) Await(s ...Event) Result {
	if len(s) != 0 {
		t.Watch(s...)
	}
	return Result{action: doYield}
}

// Yield returns a [Result] that will cause t to yield.
// op becomes the current Operation of t so that, when t is resumed, op is
// called instead.
func (t *Task) Yield(op Operation) Result {
	if op == nil {
		panic("Yield(nil): undefined behavior")
	}
	return Result{action: doYield, op: op}
}

// Switch returns a [Result] that will cause t to switch to work on op.
// t will be reset and op will be called immediately as the current Operation
// of t.
func (t *Task) Switch(op Operation) Result {
	if op == nil {
		panic("Switch(nil): undefined behavior")
	}
	return Result{action: doSwitch, op: op}
}

// An Operation is a piece of work that a [Task] is given to do when it is
// spawned.
// The return value of an Operation, a [Result], determines what next for
// a Task to do.
//
// The argument t must not escape, because t can be recycled by an [Executor]
// when t ends.
type Operation func(t *Task) Result

// Chain returns an [Operation] that will work on each of the provided
// Operations in sequence.
// When one Operation completes, Chain works on another.
func Chain(s ...Operation) Operation {
	var op Operation
	return func(t *Task) Result {
		if op == nil {
			if len(s) == 0 {
				return t.End()
			}
			op, s = s[0], s[1:]
		}
		switch res := op(t); res.action {
		case doEnd:
			op = nil
			return Result{action: doSwitch}
		case doYield, doSwitch:
			if res.op != nil {
				op = res.op
			}
			return Result{action: res.action}
		default:
			panic("internal error: unknown action")
		}
	}
}

// Do returns an [Operation] that calls f, and then completes.
func Do(f func()) Operation {
	return func(t *Task) Result {
		f()
		return t.End()
	}
}

// Never returns an [Operation] that never completes.
// Operations in a [Chain] after Never are never getting worked on.
func Never() Operation {
	return func(t *Task) Result {
		return t.Await()
	}
}

// Nop returns an [Operation] that completes without doing anything.
func Nop() Operation {
	return (*Task).End
}

// Then returns an [Operation] that first works on op, then switches to
// work on next after op completes.
//
// To chain multiple Operations, use [Chain] function.
func (op Operation) Then(next Operation) Operation {
	if next == nil {
		panic("Then(nil): undefined behavior")
	}
	return func(t *Task) Result {
		switch res := op(t); res.action {
		case doEnd:
			return Result{action: doSwitch, op: next}
		case doYield, doSwitch:
			if res.op != nil {
				op = res.op
			}
			return Result{action: res.action}
		default:
			panic("internal error: unknown action")
		}
	}
}
