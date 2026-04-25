package async

import (
	"context"
	"fmt"
	"iter"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

type action int

const (
	_ action = iota
	doYield
	doSoftYield
	doHardYield
	doTransition
	doTailTransition // Do transition and remove controller.
	doEnd
	doBreak
	doContinue
	doReturn
	doUnwind // Exit or panic.
)

const (
	flagResumed = 1 << iota
	flagEnqueued
	flagCleanup
	flagEnded
	flagFreed
	flagCanceled
	flagSoftYield
	flagHardYield
	flagExiting
	flagPanicking
	flagInsideLoop
	flagNonCancelable
	flagNonRecyclable
)

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// A Coroutine is an execution of code, similar to a goroutine but cooperative
// and stackless.
//
// A coroutine is created with a function called [Task].
// A coroutine's job is to complete the task.
// When an [Executor] spawns a coroutine with a task, it runs the coroutine by
// calling the task function with the coroutine as the argument.
// The return value determines whether to end the coroutine or to yield it
// so that it could resume later.
//
// In order for a coroutine to resume, the coroutine must watch at least one
// [Event] (e.g. [Signal], [State], etc.), when calling the task function.
// A notification of such an event resumes the coroutine.
// When a coroutine is resumed, the executor runs the coroutine again.
//
// A coroutine can also make a transition to work on another task according to
// the return value of the task function.
// A coroutine can transition from one task to another until a task ends it.
type Coroutine struct {
	_ noCopy

	flag        uint16
	depth       uint16
	childnum    uint32
	weight      Weight
	parent      *Coroutine
	executor    *Executor
	wg          *sync.WaitGroup
	ps          panicstack
	guard       func() bool
	task        Task
	deps        map[Event]struct{}
	cleanups    []Cleanup
	defers      []Task
	controllers []controller
}

type Weight int64

var coroutinePool = sync.Pool{
	New: func() any { return new(Coroutine) },
}

func newCoroutine() *Coroutine {
	return coroutinePool.Get().(*Coroutine)
}

func freeCoroutine(co *Coroutine) {
	if co.flag&flagFreed != 0 {
		panic("async: internal error: double free")
	}
	co.flag |= flagFreed
	co.parent = nil
	co.executor = nil
	wg := co.wg
	co.wg = nil
	clear(co.ps)
	co.ps = co.ps[:0]
	co.task = nil
	if co.flag&flagNonRecyclable == 0 {
		coroutinePool.Put(co)
	}
	if wg != nil {
		wg.Done()
	}
}

func (co *Coroutine) init(parent *Coroutine, e *Executor, w Weight, t Task) *Coroutine {
	var depth uint16
	if parent != nil {
		depth = parent.depth + 1
		if depth == 0 {
			panic("async: depth overflow")
		}
		if parent.childnum+1 == 0 {
			panic("async: too many child coroutines")
		}
		parent.childnum++
	}
	co.flag = flagResumed
	co.depth = depth
	co.weight = w
	co.parent = parent
	co.executor = e
	co.task = t
	return co
}

func compare[Int intType](x, y Int) int {
	if x < y {
		return -1
	}
	if x > y {
		return +1
	}
	return 0
}

func (co *Coroutine) less(other *Coroutine) bool {
	if c := compare(co.weight, other.weight); c != 0 {
		return c == +1
	}
	return co.depth < other.depth
}

// Resume resumes co.
func (co *Coroutine) Resume() {
	co.executor.resumeCoroutine(co, true)
}

func (e *Executor) resumeCoroutine(co *Coroutine, lock bool) {
	switch flag := co.flag; {
	case flag&flagEnqueued != 0:
		co.flag = flag | flagResumed
	default:
		co.flag = flag | flagResumed | flagEnqueued
		if lock {
			e.mu.Lock()
		}
		e.pq.Push(co)
		if lock {
			e.mu.Unlock()
		}
	}
}

func (e *Executor) runCoroutine(co *Coroutine) {
	flag := co.flag
	flag &^= flagEnqueued
	co.flag = flag
	switch {
	case flag&flagEnded != 0:
		freeCoroutine(co)
	case flag&flagResumed != 0:
		e.mu.Unlock()
		co.run()
		e.mu.Lock()
	}
}

func (co *Coroutine) run() (suspended bool) {
	var res Result

	ps := &co.ps
	guard := co.guard

	for {
		if guard != nil {
			var ok bool

			co.flag &^= flagResumed

			if !ps.Try(func() { ok = guard() }) {
				co.flag |= flagPanicking
				co.task = (*Coroutine).unwind
				ok = true
			}

			if !ok {
				return true
			}

			guard = nil
			co.guard = nil
		}

		co.flag &^= flagResumed
		co.flag |= flagCleanup

		co.clearDeps()
		co.clearCleanups()

		if co.childnum != 0 {
			return true
		}

		co.flag &^= flagResumed | flagCleanup | flagSoftYield

		if !co.NonCancelable() {
			co.flag &^= flagHardYield
		}

		if !ps.Try(func() { res = co.task(co) }) {
			res = co.panic()
		}

		switch res.action {
		case doYield:
			if co.Canceled() && !co.NonCancelable() {
				res = co.cancel()
			}
		case doSoftYield:
			if co.Canceled() {
				res = Result{action: doTransition, task: res.task}
			} else {
				co.flag |= flagSoftYield
				res.action = doYield
			}
		case doHardYield:
			co.flag |= flagHardYield
			res.action = doYield
		}

		if res.action != doYield && res.action != doTransition {
			co.flag &^= flagResumed
			co.flag |= flagCleanup

			co.clearDeps()
			co.clearCleanups()

			if co.childnum != 0 {
				co.task = actionToTask(res.action)
				return true
			}

			co.flag &^= flagResumed | flagCleanup

			if co.Panicking() {
				res = co.unwind()
			}

			controllers := co.controllers
			for len(controllers) != 0 {
				i := len(controllers) - 1
				c := &controllers[i]
				if !ps.Try(func() { res = c.negotiate(co, res) }) {
					res = c.negotiate(co, co.panic())
				}
				if res.action != doTransition {
					if !ps.Try(c.cleanup) {
						res = co.panic()
					}
					controllers[i] = controller{}
					controllers = controllers[:i]
					co.controllers = controllers
				}
				if res.action == doTransition || res.action == doTailTransition {
					break
				}
			}

			if res.action != doTransition && res.action != doTailTransition {
				rootController := &controller{kind: funcController}
				if !ps.Try(func() { res = rootController.negotiate(co, res) }) {
					res = rootController.negotiate(co, co.panic())
				}
			}

			if res.action == doTailTransition {
				res.action = doTransition
			}
		}

		if res.task != nil {
			co.task = res.task
		}

		if res.guard != nil {
			guard = res.guard
			co.guard = guard
			continue // For calling guard immediately.
		}

		if res.controller.kind != 0 {
			addController := true
			if res.controller.kind == funcController && res.controller.flag == 0 {
				lastController := &controller{kind: funcController}
				if n := len(co.controllers); n != 0 {
					lastController = &co.controllers[n-1]
				}
				if lastController.kind == funcController && lastController.doff == res.controller.doff {
					// Tail-call optimization:
					// If the last controller is also a funcController, do not add another one.
					// (doTailTransition also pays tribute to this optimization.)
					addController = false
				}
			}
			if addController {
				co.controllers = append(co.controllers, res.controller)
				if capSizeLimit := 1000000; cap(co.controllers) > capSizeLimit {
					co.flag |= flagNonRecyclable
					co.task = func(co *Coroutine) Result {
						panic("async: too many controllers or recursions")
					}
				}
			}
		}

		if res.action != doTransition {
			break
		}
	}

	if res.action == doYield {
		return true
	}

	co.flag |= flagEnded

	parent := co.parent
	if parent != nil {
		if i := slices.Index(parent.cleanups, Cleanup((*childCoroutineCleanup)(co))); i != -1 {
			parent.cleanups = slices.Delete(parent.cleanups, i, i+1)
		}
		parent.childnum--
		if parent.childnum == 0 && parent.flag&flagCleanup != 0 {
			parent.Resume()
		}
	}

	if co.Panicking() {
		if parent != nil {
			parent.flag |= flagPanicking
			parent.guard = nil
			parent.task = (*Coroutine).unwind
			parent.ps = append(parent.ps, co.ps...)
			parent.Resume()
		} else {
			co.executor.ps = append(co.executor.ps, co.ps...)
		}
	}

	if len(co.deps) != 0 {
		panic("async: internal error: deps did not clear")
	}
	if len(co.cleanups) != 0 {
		panic("async: internal error: cleanups did not clear")
	}
	if len(co.defers) != 0 {
		panic("async: internal error: defers did not clear")
	}
	if len(co.controllers) != 0 {
		panic("async: internal error: controllers did not clear")
	}
	if co.childnum != 0 {
		panic("async: internal error: child coroutines did not clear")
	}

	if co.flag&flagEnqueued == 0 {
		freeCoroutine(co)
	}

	return false
}

func (co *Coroutine) clearDeps() {
	deps := co.deps
	for d := range deps {
		delete(deps, d)
		d.removeListener(co)
	}
}

func (co *Coroutine) clearCleanups() {
	ok := true
	cleanups := co.cleanups
	for len(co.cleanups) != 0 {
		cleanups := co.cleanups
		co.cleanups = nil
		for _, c := range slices.Backward(cleanups) {
			ok = co.ps.Try(c.Cleanup) && ok
		}
	}
	clear(cleanups)
	co.cleanups = cleanups[:0]
	if !ok {
		co.flag |= flagPanicking
		co.task = (*Coroutine).unwind
	}
}

type childCoroutineCleanup Coroutine

func (child *childCoroutineCleanup) Cleanup() {
	co := (*Coroutine)(child)
	co.flag |= flagCanceled
	if co.flag&(flagSoftYield|flagHardYield) != flagHardYield {
		co.guard = nil
		if co.flag&flagSoftYield == 0 {
			co.flag |= flagExiting
			co.task = (*Coroutine).unwind
		}
		co.run()
	}
}

// Weight returns the weight of co.
func (co *Coroutine) Weight() Weight {
	return co.weight
}

// Parent returns the parent coroutine of co.
//
// Note that a coroutine must not escape to a non-child coroutine or another
// goroutine because, a coroutine may be put into pool for later reuse when
// it completes.
func (co *Coroutine) Parent() *Coroutine {
	return co.parent
}

// Executor returns the executor that spawned co.
func (co *Coroutine) Executor() *Executor {
	return co.executor
}

// Resumed reports whether co has been resumed.
func (co *Coroutine) Resumed() bool {
	return co.flag&flagResumed != 0
}

// Exiting reports whether co is exiting.
//
// When exiting, entering a [Func], in a deferred task, would temporarily
// reset Exiting to false until that [Func] ends or exits again.
func (co *Coroutine) Exiting() bool {
	return co.flag&flagExiting != 0
}

// Panicking reports whether co is panicking.
//
// When panicking, entering a [Func], in a deferred task, would temporarily
// reset Panicking to false until that [Func] ends or panics again.
func (co *Coroutine) Panicking() bool {
	return co.flag&flagPanicking != 0
}

// Canceled reports whether co has been canceled.
func (co *Coroutine) Canceled() bool {
	return co.flag&flagCanceled != 0
}

// NonCancelable reports whether co is currently running a [NonCancelable] task.
func (co *Coroutine) NonCancelable() bool {
	return co.flag&flagNonCancelable != 0
}

// Watch watches some events so that, when any of them notifies, co resumes.
func (co *Coroutine) Watch(ev ...Event) {
	if co.flag&flagCleanup != 0 {
		panic("async: watch during cleanup")
	}
	var deps map[Event]struct{}
	for _, d := range ev {
		if deps == nil {
			deps = co.deps
			if deps == nil {
				deps = make(map[Event]struct{})
				co.deps = deps
			}
		}
		deps[d] = struct{}{}
		d.addListener(co)
	}
}

// Cleanup represents any type that carries a Cleanup method.
// A Cleanup can be added to a coroutine in a [Task] function for making
// an effect some time later when the coroutine resumes or finishes a [Task].
type Cleanup interface {
	Cleanup()
}

// A CleanupFunc is a func() that implements the [Cleanup] interface.
type CleanupFunc func()

// Cleanup implements the [Cleanup] interface.
func (f CleanupFunc) Cleanup() { f() }

// Cleanup adds something to clean up when co resumes or finishes a [Task].
func (co *Coroutine) Cleanup(c Cleanup) {
	if c == nil {
		return
	}
	co.cleanups = append(co.cleanups, c)
}

// CleanupFunc adds a function call when co resumes or finishes a [Task].
func (co *Coroutine) CleanupFunc(f func()) {
	if f == nil {
		return
	}
	co.cleanups = append(co.cleanups, CleanupFunc(f))
}

// Defer adds a [Task] for execution when returning from a [Func].
// Deferred tasks are executed in last-in-first-out (LIFO) order.
func (co *Coroutine) Defer(t Task) {
	if t == nil {
		return
	}
	co.defers = append(co.defers, t)
}

// Recover returns the latest value in the panic stack and stops co from
// panicking.
// If co isn't panicking, Recover returns nil.
//
// One might be tempted to use the built-in panic function and this method to
// mimic the power of try-catch statement in some other programming languages,
// but there's a cost.
// In order to be able to continue running, when there's a panic, a coroutine
// immediately recovers it and puts it into the panic stack, along with a stack
// trace returned by [runtime/debug.Stack], which might take thousands of bytes.
//
// Instead of using the built-in panic function to trigger a panic, one could
// consider use [Coroutine.Panic] to mimic one, which leaves no stack trace
// behind.
func (co *Coroutine) Recover() (v any) {
	switch flag := co.flag; {
	case flag&flagCleanup != 0:
		panic("async: recover during cleanup")
	case flag&flagPanicking == 0:
		return nil
	}
	p := &co.ps[len(co.ps)-1]
	p.recovered = true
	co.flag &^= flagPanicking
	return p.value
}

// RecoverFunc is like [Coroutine.Recover] but only recovers the recent panic
// that satisfies a condition.
func (co *Coroutine) RecoverFunc(f func(v any) bool) (v any) {
	switch flag := co.flag; {
	case flag&flagCleanup != 0:
		panic("async: recover during cleanup")
	case flag&flagPanicking == 0:
		return nil
	}
	p := &co.ps[len(co.ps)-1]
	if f(p.value) {
		p.recovered = true
		co.flag &^= flagPanicking
		v = p.value
	}
	return v
}

// Spawn creates a child coroutine with the same weight as co to work on t.
//
// Spawn runs t immediately. If t panics immediately, Spawn panics, too.
//
// Child coroutines, if not yet ended, are canceled when the parent one resumes
// or finishes a [Task].
// When a coroutine is canceled, it runs to completion with all yield points
// treated like exit points.
//
// However, within a [NonCancelable] context, a canceled coroutine is allowed
// to yield, which would correspondingly cause its parent coroutine to yield,
// too. In such case, the parent coroutine stays suspended until all its child
// coroutines complete.
func (co *Coroutine) Spawn(t Task) {
	child := newCoroutine().init(co, co.executor, co.weight, t)
	switch suspended := child.run(); {
	case suspended:
		co.cleanups = append(co.cleanups, (*childCoroutineCleanup)(child))
	case co.Panicking():
		// child panics.
		panic(dummy{}) // Stop current task.
	}
}

// Result is the type of the return value of a [Task] function.
// A Result determines what next for a coroutine to do after running a task.
//
// A Result can be created by calling one of the following methods:
//   - [Coroutine.Await], [Coroutine.SoftAwait] or [Coroutine.HardAwait]:
//     for creating a [PendingResult] that can be transformed into a [Result]
//     with one of its methods, which will then cause the running coroutine to
//     yield;
//   - [Coroutine.Yield], [Coroutine.SoftYield] or [Coroutine.HardYield]:
//     for yielding a coroutine with additional events to watch and, when
//     resumed, reiterating the running task;
//   - [Coroutine.Transition]: for making a transition to work on another task;
//   - [Coroutine.End]: for ending the running task of a coroutine;
//   - [Coroutine.Break]: for breaking a [Loop] (or [LoopN]);
//   - [Coroutine.Continue]: for continuing a [Loop] (or [LoopN]);
//   - [Coroutine.Return]: for returning from a [Func];
//   - [Coroutine.Exit]: for exiting a coroutine;
//   - [Coroutine.Panic]: for simulating a panic;
//   - [Coroutine.PanicWithStackTrace]: for simulating a panic with a stack
//     trace.
//
// These methods may have side effects. One should never store a Result in
// a variable and overwrite it with another, before returning it. Instead,
// one should just return a Result right after it is created.
type Result struct {
	action     action
	guard      func() bool // used by do(Soft|Hard)Yield only
	task       Task        // used by do(Soft|Hard)Yield and do(Tail)Transition
	controller controller  // used by doTransition only
}

// PendingResult is the return type of the [Coroutine.Await] method.
// A PendingResult is an intermediate value that must be transformed into
// a [Result] with one of its methods before returning from a [Task].
type PendingResult struct {
	res Result
}

// Reiterate returns a [Result] that will cause the running coroutine to yield
// and, when resumed, reiterate the running task.
func (pr PendingResult) Reiterate() Result {
	return pr.res
}

// Then returns a [Result] that will cause the running coroutine to yield and,
// when resumed, make a transition to work on another [Task].
func (pr PendingResult) Then(t Task) Result {
	pr.res.task = must(t)
	return pr.res
}

// End returns a [Result] that will cause the running coroutine to yield and,
// when resumed, end the running task.
func (pr PendingResult) End() Result {
	return pr.Then(End())
}

// Break returns a [Result] that will cause the running coroutine to yield and,
// when resumed, break a [Loop] (or [LoopN]).
func (pr PendingResult) Break() Result {
	return pr.Then(Break())
}

// Continue returns a [Result] that will cause the running coroutine to yield
// and, when resumed, continue a [Loop] (or [LoopN]).
func (pr PendingResult) Continue() Result {
	return pr.Then(Continue())
}

// Return returns a [Result] that will cause the running coroutine to yield and,
// when resumed, return from a [Func].
func (pr PendingResult) Return() Result {
	return pr.Then(Return())
}

// Exit returns a [Result] that will cause the running coroutine to yield and,
// when resumed, cause the running coroutine to exit.
func (pr PendingResult) Exit() Result {
	return pr.Then(Exit())
}

// Panic returns a [Result] that will cause the running coroutine to yield and,
// when resumed, cause the running coroutine to behave like there's a panic.
// Unlike the built-in panic function, Panic leaves no stack trace behind.
// Please use with caution.
func (pr PendingResult) Panic(v any) Result {
	return pr.Then(Panic(v))
}

// Until transforms pr into one with a condition.
// Affected coroutines remain suspended until the condition is met.
func (pr PendingResult) Until(f func() bool) PendingResult {
	pr.res.guard = f
	return pr
}

// Await returns a [PendingResult] that can be transformed into a [Result]
// with one of its methods, which will then cause co to yield.
// Await also accepts additional events to watch.
func (co *Coroutine) Await(ev ...Event) PendingResult {
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return PendingResult{res: Result{action: doYield}}
}

// SoftAwait is like [Coroutine.Await], but cancelable.
// Upon cancellation, where [Coroutine.Await] would unwind and exit,
// SoftAwait would just resume and continue running.
func (co *Coroutine) SoftAwait(ev ...Event) PendingResult {
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return PendingResult{res: Result{action: doSoftYield}}
}

// HardAwait is like [Coroutine.Await], but non-cancelable.
// Upon cancellation, where [Coroutine.Await] would unwind and exit,
// HardAwait would just do nothing and remain suspended.
func (co *Coroutine) HardAwait(ev ...Event) PendingResult {
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return PendingResult{res: Result{action: doHardYield}}
}

// Yield returns a [Result] that will cause co to yield and, when co is resumed,
// reiterate the running task.
// Yield also accepts additional events to watch.
func (co *Coroutine) Yield(ev ...Event) Result {
	return co.Await(ev...).Reiterate()
}

// SoftYield is like [Coroutine.Yield], but cancelable.
// Upon cancellation, where [Coroutine.Yield] would unwind and exit,
// SoftYield would just resume and continue running.
func (co *Coroutine) SoftYield(ev ...Event) Result {
	return co.SoftAwait(ev...).Reiterate()
}

// HardYield is like [Coroutine.Yield], but non-cancelable.
// Upon cancellation, where [Coroutine.Yield] would unwind and exit,
// HardYield would just do nothing and remain suspended.
func (co *Coroutine) HardYield(ev ...Event) Result {
	return co.HardAwait(ev...).Reiterate()
}

// Transition returns a [Result] that will cause co to make a transition to
// work on t.
func (co *Coroutine) Transition(t Task) Result {
	return Result{action: doTransition, task: must(t)}
}

// End returns a [Result] that will cause co to end its current running task.
func (co *Coroutine) End() Result {
	return Result{action: doEnd}
}

// Break returns a [Result] that will cause co to break a [Loop] (or [LoopN]).
func (co *Coroutine) Break() Result {
	if co.flag&flagInsideLoop == 0 {
		panic("async: break without a loop")
	}
	return Result{action: doBreak}
}

// Continue returns a [Result] that will cause co to continue a [Loop]
// (or [LoopN]).
func (co *Coroutine) Continue() Result {
	if co.flag&flagInsideLoop == 0 {
		panic("async: continue without a loop")
	}
	return Result{action: doContinue}
}

// Return returns a [Result] that will cause co to return from a [Func].
func (co *Coroutine) Return() Result {
	return Result{action: doReturn}
}

// Exit returns a [Result] that will cause co to exit.
// All deferred tasks will be run before co exits.
func (co *Coroutine) Exit() Result {
	co.flag |= flagExiting
	return Result{action: doUnwind}
}

func (co *Coroutine) cancel() Result {
	co.flag |= flagExiting | flagCanceled
	return Result{action: doUnwind}
}

func (co *Coroutine) panic() Result {
	co.flag |= flagPanicking
	return Result{action: doUnwind}
}

func (co *Coroutine) unwind() Result {
	return Result{action: doUnwind}
}

// Panic returns a [Result] that will cause co to behave as there's a panic.
// Unlike the built-in panic function, Panic leaves no stack trace behind.
// Please use with caution.
func (co *Coroutine) Panic(v any) Result {
	if v == nil {
		panic("async: Panic called with nil argument")
	}
	co.ps.Push(v, nil)
	co.flag |= flagPanicking
	return Result{action: doUnwind}
}

// PanicWithStackTrace returns a [Result] that will cause co to behave as
// there's a panic.
// PanicWithStackTrace takes an extra stacktrace argument, which makes it
// very much like the built-in panic function.
// Useful when one wants to propagate panics from goroutines.
func (co *Coroutine) PanicWithStackTrace(v any, stacktrace []byte) Result {
	if v == nil {
		panic("async: PanicWithStackTrace called with nil argument")
	}
	co.ps.Push(v, stacktrace)
	co.flag |= flagPanicking
	return Result{action: doUnwind}
}

type controllerKind int8

const (
	_ controllerKind = iota
	funcController
	thenController
	blockController
	loopController
	seqController
	ncController
)

type controller struct {
	kind  controllerKind
	flag  uint16              // used by funcController and loopController
	poff  int                 // used by funcController only
	doff  int                 // used by funcController only
	task  Task                // used by thenController and loopController
	tasks []Task              // used by blockController only
	next  func() (Task, bool) // used by seqController only
	stop  func()              // used by seqController only
}

func (c *controller) negotiate(co *Coroutine, res Result) Result {
	switch c.kind {
	case funcController:
		switch res.action {
		case doEnd, doReturn, doUnwind:
			if !co.Panicking() && len(co.ps) > c.poff {
				// Discard recovered panic values.
				clear(co.ps[c.poff:])
				co.ps = co.ps[:c.poff]
			}
			if len(co.defers) > c.doff {
				i := len(co.defers) - 1
				t := co.defers[i]
				co.defers[i] = nil
				co.defers = co.defers[:i]
				return co.Transition(t)
			}
			unwind := co.flag&(flagExiting|flagPanicking) != 0
			co.flag |= c.flag
			if unwind {
				return co.unwind()
			}
			return co.End()
		case doBreak, doContinue:
			panic("async: internal error: unhandled break/continue action")
		default:
			panic(fmt.Sprintf("async: unknown action: %v", res.action))
		}
	case thenController:
		if res.action != doEnd {
			return res
		}
		return Result{action: doTailTransition, task: c.task}
	case blockController:
		if res.action != doEnd || len(c.tasks) == 0 {
			return res
		}
		t := c.tasks[0]
		c.tasks = c.tasks[1:]
		action := doTransition
		if len(c.tasks) == 0 {
			action = doTailTransition
		}
		return Result{action: action, task: must(t)}
	case loopController:
		switch res.action {
		case doEnd, doContinue:
			return co.Transition(c.task)
		}
		if c.flag == 0 {
			co.flag &^= flagInsideLoop
		}
		if res.action == doBreak {
			return co.End()
		}
		return res
	case seqController:
		if res.action == doEnd {
			if t, ok := c.next(); ok {
				return co.Transition(t)
			}
		}
		return res
	case ncController:
		co.flag &^= flagHardYield | flagNonCancelable
		return res
	default:
		panic("async: internal error: unknown controller")
	}
}

func (c *controller) cleanup() {
	switch c.kind {
	case seqController:
		c.stop()
	}
}

// A Task is a piece of work that a coroutine is given to do when it is spawned.
// The return value of a task, a [Result], determines what next for a coroutine
// to do.
//
// The argument co must not escape to a non-child coroutine or another goroutine
// because, co may be put into pool for later reuse when co completes.
type Task func(co *Coroutine) Result

// Then returns a [Task] that first works on t, then next after t ends.
//
// To chain multiple tasks, use [Block] function.
func (t Task) Then(next Task) Task {
	return func(co *Coroutine) Result {
		return Result{
			action:     doTransition,
			task:       must(t),
			controller: controller{kind: thenController, task: must(next)},
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

// Await returns a [Task] that awaits some events until any of them notifies,
// and then ends.
// If ev is empty, Await returns a [Task] that never ends.
func Await(ev ...Event) Task {
	if len(ev) == 0 {
		// Return a pure function instead.
		return func(co *Coroutine) Result {
			return co.Await().End()
		}
	}
	return func(co *Coroutine) Result {
		return co.Await(ev...).End()
	}
}

// SoftAwait is like [Await], but cancelable.
// Upon cancellation, where [Await] would unwind and exit, SoftAwait would
// just resume and continue running.
func SoftAwait(ev ...Event) Task {
	if len(ev) == 0 {
		// Return a pure function instead.
		return func(co *Coroutine) Result {
			return co.SoftAwait().End()
		}
	}
	return func(co *Coroutine) Result {
		return co.SoftAwait(ev...).End()
	}
}

// HardAwait is like [Await], but non-cancelable.
// Upon cancellation, where [Await] would unwind and exit, HardAwait would
// just do nothing and remain suspended.
func HardAwait(ev ...Event) Task {
	if len(ev) == 0 {
		// Return a pure function instead.
		return func(co *Coroutine) Result {
			return co.HardAwait().End()
		}
	}
	return func(co *Coroutine) Result {
		return co.HardAwait(ev...).End()
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
			action:     doTransition,
			task:       must(s[0]),
			controller: controller{kind: blockController, tasks: s[1:]},
		}
	}
}

// Break returns a [Task] that breaks a [Loop] (or [LoopN]).
func Break() Task {
	return func(co *Coroutine) Result {
		return co.Break()
	}
}

// Continue returns a [Task] that continues a [Loop] (or [LoopN]).
func Continue() Task {
	return func(co *Coroutine) Result {
		return co.Continue()
	}
}

// Loop returns a [Task] that forms a loop, which would run t repeatedly.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
func Loop(t Task) Task {
	return func(co *Coroutine) Result {
		res := Result{
			action: doTransition,
			task:   must(t),
			controller: controller{
				kind: loopController,
				flag: co.flag & flagInsideLoop,
				task: t,
			},
		}
		co.flag |= flagInsideLoop
		return res
	}
}

// LoopN returns a [Task] that forms a loop, which would run t repeatedly
// for n times.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
func LoopN[Int intType](n Int, t Task) Task {
	return func(co *Coroutine) Result {
		i := Int(0)
		f := func(co *Coroutine) Result {
			if i < n {
				i++
				return co.Transition(t)
			}
			return co.Break()
		}
		res := Result{
			action: doTransition,
			task:   f,
			controller: controller{
				kind: loopController,
				flag: co.flag & flagInsideLoop,
				task: f,
			},
		}
		co.flag |= flagInsideLoop
		return res
	}
}

type intType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
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

// Panic returns a [Task] that causes the coroutine that runs it to behave
// like there's a panic.
// Unlike the built-in panic function, Panic leaves no stack trace behind.
// Please use with caution.
func Panic(v any) Task {
	return func(co *Coroutine) Result {
		return co.Panic(v)
	}
}

// Func returns a [Task] that runs t in a function scope.
// Spawned tasks are considered surrounded by an invisible [Func].
func Func(t Task) Task {
	return func(co *Coroutine) Result {
		const flag = flagExiting | flagPanicking | flagInsideLoop
		res := Result{
			action: doTransition,
			task:   must(t),
			controller: controller{
				kind: funcController,
				flag: co.flag & flag,
				poff: len(co.ps),
				doff: len(co.defers),
			},
		}
		co.flag &^= flag
		return res
	}
}

// FromSeq returns a [Task] that runs each of the tasks from seq in sequence.
//
// Caveat: requires spawning a goroutine (which is stackful) when running
// the returned task. The goroutine leaks, as well as the coroutine that runs
// the returned task, if the returned task never ends.
func FromSeq(seq iter.Seq[Task]) Task {
	return func(co *Coroutine) Result {
		next, stop := iter.Pull(seq)
		return Result{
			action:     doTransition,
			task:       End(),
			controller: controller{kind: seqController, next: next, stop: stop},
		}
	}
}

// NonCancelable returns a [Task] that runs t in a non-cancelable context,
// preventing it from being canceled by a parent coroutine.
//
// For one-shot non-cancelable yields, one can use [HardAwait],
// [Coroutine.HardAwait] or [Coroutine.HardYield].
func NonCancelable(t Task) Task {
	return func(co *Coroutine) Result {
		res := co.Transition(t)
		if !co.NonCancelable() {
			co.flag |= flagHardYield | flagNonCancelable
			res.controller.kind = ncController
		}
		return res
	}
}

func actionToTask(action action) Task {
	switch action {
	case doEnd:
		return (*Coroutine).End
	case doBreak:
		return (*Coroutine).Break
	case doContinue:
		return (*Coroutine).Continue
	case doReturn:
		return (*Coroutine).Return
	case doUnwind:
		return (*Coroutine).unwind
	default:
		panic("async: internal error: unexpected action")
	}
}

func must(t Task) Task {
	if t == nil {
		panic("async: nil Task")
	}
	return t
}

func resumeParent(co *Coroutine) Result {
	co.Parent().Resume()
	return co.End()
}

// Join returns a [Task] that runs each of the given tasks in its own
// child coroutine and awaits until all of them complete, and then ends.
//
// When passed no arguments, Join returns a [Task] that never ends.
func Join(s ...Task) Task {
	return func(co *Coroutine) Result {
		n := len(s)
		done := func(co *Coroutine) Result {
			if n--; n == 0 {
				co.Parent().Resume()
			}
			return co.End()
		}
		for _, t := range s {
			co.Spawn(func(co *Coroutine) Result {
				co.Defer(done)
				return co.Transition(t)
			})
		}
		return co.Await().End()
	}
}

// Select returns a [Task] that runs each of the given tasks in its own
// child coroutine and awaits until any of them completes, and then ends.
// When Select ends, tasks other than the one that completes are canceled.
//
// When passed no arguments, Select returns a [Task] that never ends.
func Select(s ...Task) Task {
	z := slices.Clone(s)
	for i, t := range z {
		z[i] = func(co *Coroutine) Result {
			co.Defer(resumeParent)
			return co.Transition(t)
		}
	}
	return func(co *Coroutine) Result {
		for _, t := range z {
			co.Spawn(t)
			if co.Resumed() {
				break
			}
		}
		return co.Await().End()
	}
}

// Spawn returns a [Task] that runs t in a child coroutine and awaits until
// t completes, and then ends.
//
// Spawn(t) is equivalent to Join(t) or Select(t), but cheaper and clearer.
func Spawn(t Task) Task {
	f := func(co *Coroutine) Result {
		co.Defer(resumeParent)
		return co.Transition(t)
	}
	return func(co *Coroutine) Result {
		co.Spawn(f)
		return co.Await().End()
	}
}

// MergeSeq returns a [Task] that runs each of the tasks from seq in its own
// child coroutine concurrently until all of them complete, and then ends.
// The argument concurrency specifies the maximum number of tasks that can
// run at the same time. If it is zero, no tasks will be run and MergeSeq
// never ends. It may wrap around. The maximum value of concurrency is -1.
//
// Caveat: requires spawning a goroutine (which is stackful) when running
// the returned task. The goroutine leaks, as well as the coroutine that runs
// the returned task, if the returned task never ends.
func MergeSeq(concurrency int, seq iter.Seq[Task]) Task {
	return func(co *Coroutine) Result {
		next, stop := iter.Pull(seq)
		co.CleanupFunc(stop)
		var tasks struct {
			n int
		}
		done := func(co *Coroutine) Result {
			tasks.n--
			return resumeParent(co)
		}
		return co.Await().Until(func() bool {
			for {
				if tasks.n == concurrency {
					return false
				}
				t, ok := next()
				if !ok {
					return tasks.n == 0
				}
				tasks.n++
				co.Spawn(func(co *Coroutine) Result {
					co.Defer(done)
					return co.Transition(t)
				})
			}
		}).End()
	}
}

// A Goer is for spawning goroutines and keeping track of them.
//
// For go1.25 and later, a [sync.WaitGroup] would satisfy this interface.
type Goer interface {
	Go(f func())
}

// Go returns a [Task] that uses g to spawn a goroutine to run f, which takes
// a [context.Context] as argument that will be canceled when the running
// coroutine or ctx is canceled.
// The return value of f, a [Task], if non-nil, will be run after f returns.
// To cancel Go, f must return a [Task] that terminates Go, such as [Exit].
// If f panics, Go propagates it.
// Go completes only when everything is settled.
func Go(ctx context.Context, g Goer, f func(ctx context.Context) Task) Task {
	return func(co *Coroutine) Result {
		ctx := ctx
		if !co.NonCancelable() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			co.CleanupFunc(cancel)
		}
		var state struct {
			Signal
			done bool
			t    Task
			v    any
			s    []byte
		}
		t := func(co *Coroutine) Result {
			switch {
			case state.done:
				if state.t != nil {
					return co.Transition(state.t)
				}
				if state.v != nil {
					return co.PanicWithStackTrace(state.v, state.s)
				}
				if co.Canceled() && !co.NonCancelable() {
					return co.Exit()
				}
				return co.End()
			case co.Canceled():
				return co.HardYield(&state)
			default:
				state.done = true
				state.Notify()
				return co.End()
			}
		}
		e, w := co.Executor(), co.Weight()
		g.Go(func() {
			defer func() {
				if v := recover(); v != nil {
					state.v, state.s = v, debug.Stack()
				}
				e.SpawnWeighted(w, t)
			}()
			state.t = f(ctx)
		})
		return co.SoftAwait(&state).Then(t)
	}
}

// Sleep returns a [Task] that awaits until a period of time elapses, and then
// ends.
// When ctx is canceled, the coroutine that runs Sleep exits.
func Sleep(ctx context.Context, g Goer, d time.Duration) Task {
	return Go(ctx, g, func(ctx context.Context) Task {
		select {
		case <-ctx.Done():
			return Exit()
		case <-time.After(d):
			return nil
		}
	})
}
