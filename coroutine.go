package async

import (
	"iter"
	"slices"
)

type action int

const (
	_ action = iota
	doYield
	doTransition
	doEnd
	doBreak
	doContinue
	doReturn
	doExit
	doTailTransition // Do transition and remove controller.
)

const (
	flagResumed = 1 << iota
	flagEnqueued
	flagEnded
	flagRecyclable
	flagRecycled
	flagManaged
	flagExiting
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
// A coroutine can also make a transition to work on another task according to
// the return value of the task function.
// A coroutine can transition from one task to another until a task ends it.
type Coroutine struct {
	flag        uint8
	level       uint32
	weight      Weight
	parent      *Coroutine
	executor    *Executor
	guard       func() bool
	task        Task
	deps        map[Event]struct{}
	cleanups    []Cleanup
	defers      []Task
	controllers []controller
}

// Weight is the type of weight for use when spawning a weighted coroutine.
type Weight int

func (e *Executor) newCoroutine() *Coroutine {
	if co := e.coroutinePool().Get(); co != nil {
		return co.(*Coroutine)
	}
	return new(Coroutine)
}

func (e *Executor) freeCoroutine(co *Coroutine) {
	if co.flag&(flagRecyclable|flagRecycled) == flagRecyclable {
		co.flag |= flagRecycled
		co.parent = nil
		co.executor = nil
		co.task = nil
		e.coroutinePool().Put(co)
	}
}

func (co *Coroutine) init(e *Executor, t Task) *Coroutine {
	co.flag = flagResumed
	co.level = 0
	co.weight = 0
	co.executor = e
	co.task = t
	return co
}

func (co *Coroutine) recyclable() *Coroutine {
	co.flag |= flagRecyclable
	return co
}

func (co *Coroutine) withLevel(l uint32) *Coroutine {
	co.level = l
	return co
}

func (co *Coroutine) withWeight(w Weight) *Coroutine {
	co.weight = w
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
	return co.level < other.level
}

// Resume resumes co.
func (co *Coroutine) Resume() {
	e := co.executor
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resumeCoroutine(co)
}

func (e *Executor) resumeCoroutine(co *Coroutine) {
	switch flag := co.flag; {
	case flag&flagEnded != 0:
		// Child coroutines may try to resume their parent ones.
		// No panicking here.
	case flag&flagEnqueued != 0:
		co.flag = flag | flagResumed
	default:
		co.flag = flag | flagResumed | flagEnqueued
		e.pq.Push(co)
	}
}

func (e *Executor) runCoroutine(co *Coroutine) {
	flag := co.flag
	flag &^= flagEnqueued
	co.flag = flag
	switch {
	case flag&flagEnded != 0:
		e.freeCoroutine(co)
	case flag&flagResumed != 0:
		e.mu.Unlock()
		co.run()
		e.mu.Lock()
	}
}

func (co *Coroutine) run() (yielded bool) {
	var res Result

	pc := &co.executor.pc
	guard := co.guard

	for {
		if guard != nil {
			var ok bool
			if !pc.Try(func() { ok = guard() }) {
				ok = true
				co.task = Exit()
			}
			if !ok {
				return true
			}
			guard = nil
			co.guard = nil
		}

		co.clearDeps()
		co.clearCleanups()

		co.flag &^= flagResumed | flagEnded

		if !pc.Try(func() { res = co.task(co) }) {
			res = co.Exit()
		}

		if res.action != doYield && res.action != doTransition {
			controllers := co.controllers
			for len(controllers) != 0 {
				i := len(controllers) - 1
				c := controllers[i]
				if !pc.Try(func() { res = c.negotiate(co, res) }) {
					res = c.negotiate(co, co.Exit())
				}
				if res.action != doTransition {
					if c, ok := c.(interface{ cleanup() }); ok {
						pc.Try(c.cleanup)
					}
					controllers[i] = nil
					controllers = controllers[:i]
					co.controllers = controllers
				}
				if res.action == doTransition || res.action == doTailTransition {
					break
				}
			}
			if res.action != doTransition && res.action != doTailTransition {
				if !pc.Try(func() { res = rootController.negotiate(co, res) }) {
					res = rootController.negotiate(co, co.Exit())
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

		if res.action != doTransition {
			break
		}

		if res.controller != nil {
			addController := true
			if _, ok := res.controller.(funcController); ok {
				lastController := rootController
				if n := len(co.controllers); n != 0 {
					lastController = co.controllers[n-1]
				}
				if _, ok := lastController.(funcController); ok {
					// Tail-call optimization:
					// If the last controller is also a funcController, do not add another one.
					// (doTailTransition also pays tribute to this optimization.)
					addController = false
				}
			}
			if addController {
				co.controllers = append(co.controllers, res.controller)
				if capSizeLimit := 1000000; cap(co.controllers) > capSizeLimit {
					co.flag &^= flagRecyclable
					co.task = func(co *Coroutine) Result {
						panic("async: too many controllers or recursions")
					}
				}
			}
		}
	}

	if res.action == doYield {
		return true
	}

	co.flag |= flagEnded

	co.clearDeps()
	co.clearCleanups()
	co.removeFromParent()

	if len(co.defers) != 0 {
		panic("async: not all deferred tasks are handled (internal error)")
	}

	if len(co.controllers) != 0 {
		panic("async: not all controllers are handled (internal error)")
	}

	if co.flag&flagEnqueued == 0 {
		co.executor.freeCoroutine(co)
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
	pc := &co.executor.pc
	cleanups := co.cleanups
	for len(co.cleanups) != 0 {
		cleanups := co.cleanups
		co.cleanups = nil
		for _, c := range slices.Backward(cleanups) {
			pc.Try(c.Cleanup)
		}
	}
	clear(cleanups)
	co.cleanups = cleanups[:0]
}

func (co *Coroutine) removeFromParent() {
	if co.flag&flagManaged == 0 {
		return
	}
	parent := co.parent
	for i, c := range parent.cleanups {
		if c == (*childCoroutineCleanup)(co) {
			parent.cleanups = slices.Delete(parent.cleanups, i, i+1)
			break
		}
	}
}

type childCoroutineCleanup Coroutine

func (child *childCoroutineCleanup) Cleanup() {
	co := (*Coroutine)(child)
	co.flag &^= flagManaged
	co.guard = nil
	co.task = Exit()
	if yielded := co.run(); yielded {
		panic("async: child coroutine did not end (internal error)")
	}
}

// Weight returns the weight of co.
func (co *Coroutine) Weight() Weight {
	return co.weight
}

// Parent returns the parent coroutine of co.
func (co *Coroutine) Parent() *Coroutine {
	return co.parent
}

// Executor returns the executor that spawned co.
func (co *Coroutine) Executor() *Executor {
	return co.executor
}

// Ended reports whether co has already ended (or exited).
func (co *Coroutine) Ended() bool {
	return co.flag&flagEnded != 0
}

// Exiting reports whether co is exiting.
func (co *Coroutine) Exiting() bool {
	return co.flag&flagExiting != 0
}

// Resumed reports whether co has been resumed.
func (co *Coroutine) Resumed() bool {
	return co.flag&flagResumed != 0
}

// Watch watches some events so that, when any of them notifies, co resumes.
func (co *Coroutine) Watch(ev ...Event) {
	if co.flag&(flagEnded|flagExiting) != 0 {
		return
	}
	for _, d := range ev {
		deps := co.deps
		if deps == nil {
			deps = make(map[Event]struct{})
			co.deps = deps
		}
		deps[d] = struct{}{}
		d.addListener(co)
	}
}

// Cleanup represents any type that carries a Cleanup method.
// A Cleanup can be added to a coroutine in a [Task] function for making
// an effect some time later when the coroutine resumes or ends, or when
// the coroutine is making a transition to work on another [Task].
type Cleanup interface {
	Cleanup()
}

// A CleanupFunc is a func() that implements the [Cleanup] interface.
type CleanupFunc func()

// Cleanup implements the [Cleanup] interface.
func (f CleanupFunc) Cleanup() { f() }

// Cleanup adds something to clean up when co resumes or ends, or when co is
// making a transition to work on another [Task].
func (co *Coroutine) Cleanup(c Cleanup) {
	if co.Ended() {
		panic("async: coroutine has already ended")
	}
	if c == nil {
		return
	}
	co.cleanups = append(co.cleanups, c)
}

// CleanupFunc adds a function call when co resumes or ends, or when co is
// making a transition to work on another [Task].
func (co *Coroutine) CleanupFunc(f func()) {
	if co.Ended() {
		panic("async: coroutine has already ended")
	}
	if f == nil {
		return
	}
	co.cleanups = append(co.cleanups, CleanupFunc(f))
}

// Defer adds a [Task] for execution when returning from a [Func].
// Deferred tasks are executed in last-in-first-out (LIFO) order.
func (co *Coroutine) Defer(t Task) {
	if co.Ended() {
		panic("async: coroutine has already ended")
	}
	if t == nil {
		return
	}
	co.defers = append(co.defers, t)
}

// Spawn creates a child coroutine with the same weight as co to work on t.
//
// Child coroutines, if not yet ended, are forcedly exited when the parent one
// resumes or ends, or when the parent one is making a transition to work on
// another task.
func (co *Coroutine) Spawn(t Task) {
	if co.Ended() {
		panic("async: coroutine has already ended")
	}

	level := co.level + 1
	if level == 0 {
		panic("async: too many levels")
	}

	child := co.executor.newCoroutine().init(co.executor, t).recyclable().withLevel(level).withWeight(co.weight)
	child.parent = co

	if yielded := child.run(); yielded {
		child.flag |= flagManaged
		co.cleanups = append(co.cleanups, (*childCoroutineCleanup)(child))
	}
}

// Result is the type of the return value of a [Task] function.
// A Result determines what next for a coroutine to do after running a task.
//
// A Result can be created by calling one of the following methods:
//   - [Coroutine.Await]: for creating a [PendingResult] that can be transformed
//     into a [Result] with one of its methods, which will then cause
//     the running coroutine to yield;
//   - [Coroutine.Yield]: for yielding a coroutine with additional events to
//     watch and, when resumed, reiterating the running task;
//   - [Coroutine.Transition]: for making a transition to work on another task;
//   - [Coroutine.End]: for ending the running task of a coroutine;
//   - [Coroutine.Break]: for breaking a [Loop] (or [LoopN]);
//   - [Coroutine.Continue]: for continuing a [Loop] (or [LoopN]);
//   - [Coroutine.Return]: for returning from a [Func];
//   - [Coroutine.Exit]: for ending a coroutine forcedly.
//
// These methods may have side effects. One should never store a Result in
// a variable and overwrite it with another, before returning it. Instead,
// one should just return a Result right after it is created.
type Result struct {
	action     action
	guard      func() bool // used by doYield only
	task       Task        // used by doYield, doTransition and doTailTransition
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
// when resumed, make a transition to work on another task.
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

// Until transforms pr into one with a condition.
// Affected coroutines remain yielded until the condition is met.
func (pr PendingResult) Until(f func() bool) PendingResult {
	pr.res.guard = f
	return pr
}

// Await returns a [PendingResult] that can be transformed into a [Result]
// with one of its methods, which will then cause co to yield.
// Await also accepts additional events to watch.
//
// Yielding is not allowed when a coroutine is exiting.
// If co is exiting, Await panics.
func (co *Coroutine) Await(ev ...Event) PendingResult {
	if co.Exiting() {
		panic("async: yielding while exiting")
	}
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return PendingResult{res: Result{action: doYield}}
}

// Yield returns a [Result] that will cause co to yield and, when co is resumed,
// reiterate the running task.
// Yield also accepts additional events to watch.
//
// Yielding is not allowed when a coroutine is exiting.
// If co is exiting, Yield panics.
func (co *Coroutine) Yield(ev ...Event) Result {
	return co.Await(ev...).Reiterate()
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
	return Result{action: doBreak}
}

// Continue returns a [Result] that will cause co to continue a [Loop]
// (or [LoopN]).
func (co *Coroutine) Continue() Result {
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
			action:     doTransition,
			task:       must(t),
			controller: &thenController{must(next)},
		}
	}
}

type thenController struct {
	next Task
}

func (c *thenController) negotiate(co *Coroutine, res Result) Result {
	if res.action != doEnd {
		return res
	}
	return Result{action: doTailTransition, task: c.next}
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
		return co.Await(ev...).End()
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
			controller: &blockController{s[1:]},
		}
	}
}

type blockController struct {
	s []Task
}

func (c *blockController) negotiate(co *Coroutine, res Result) Result {
	if res.action != doEnd || len(c.s) == 0 {
		return res
	}
	t := c.s[0]
	c.s = c.s[1:]
	action := doTransition
	if len(c.s) == 0 {
		action = doTailTransition
	}
	return Result{action: action, task: must(t)}
}

// Break returns a [Task] that breaks a [Loop] (or [LoopN]).
func Break() Task {
	return (*Coroutine).Break
}

// Continue returns a [Task] that continues a [Loop] (or [LoopN]).
func Continue() Task {
	return (*Coroutine).Continue
}

// Loop returns a [Task] that forms a loop, which would run t repeatedly.
// Both [Coroutine.Break] and [Break] can break this loop early.
// Both [Coroutine.Continue] and [Continue] can continue this loop early.
func Loop(t Task) Task {
	return func(co *Coroutine) Result {
		return Result{
			action:     doTransition,
			task:       must(t),
			controller: &loopController{t},
		}
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
		return Result{
			action:     doTransition,
			task:       f,
			controller: &loopController{f},
		}
	}
}

type intType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type loopController struct {
	t Task
}

func (c *loopController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doEnd:
		return co.Transition(c.t)
	case doBreak:
		return co.End()
	case doContinue:
		return co.Transition(c.t)
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
			action:     doTransition,
			task:       must(t),
			controller: funcController(len(co.defers)),
		}
	}
}

// Note that funcController must be stateless, because rootController,
// as a funcController, might be shared by multiple coroutines run by
// different executors.
type funcController int

func (c funcController) negotiate(co *Coroutine, res Result) Result {
	switch res.action {
	case doEnd, doReturn, doExit:
		if defers := co.defers; int(c) < len(defers) {
			i := len(defers) - 1
			t := defers[i]
			defers[i] = nil
			co.defers = defers[:i]
			return co.Transition(t)
		}
		if co.Exiting() {
			return co.Exit()
		}
		return co.End()
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

var rootController controller = funcController(0)

// FromSeq returns a [Task] that runs each of the tasks from seq in sequence.
func FromSeq(seq iter.Seq[Task]) Task {
	return func(co *Coroutine) Result {
		next, stop := iter.Pull(seq)
		return Result{
			action:     doTransition,
			task:       End(),
			controller: &seqController{next, stop},
		}
	}
}

type seqController struct {
	next func() (Task, bool)
	stop func()
}

func (c *seqController) negotiate(co *Coroutine, res Result) Result {
	if res.action == doEnd {
		if t, ok := c.next(); ok {
			return co.Transition(t)
		}
	}
	return res
}

func (c *seqController) cleanup() {
	c.stop()
}

func resumeParent(co *Coroutine) Result {
	co.Parent().Resume()
	return co.End()
}

// Join returns a [Task] that runs each of the given tasks in its own
// child coroutine and awaits until all of them complete, and then ends.
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
// When Select ends, tasks other than the one that completes are forcedly
// exited.
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

// ConcatSeq returns a [Task] that runs each of the tasks from seq in its own
// child coroutine sequentially until all of them complete, and then ends.
func ConcatSeq(seq iter.Seq[Task]) Task {
	return func(co *Coroutine) Result {
		next, stop := iter.Pull(seq)
		co.CleanupFunc(stop)
		return co.Await().Until(func() bool {
			t, ok := next()
			if ok {
				co.Spawn(func(co *Coroutine) Result {
					co.Defer(resumeParent)
					return co.Transition(t)
				})
			}
			return !ok
		}).End()
	}
}

// MergeSeq returns a [Task] that runs each of the tasks from seq in its own
// child coroutine concurrently until all of them complete, and then ends.
// The argument concurrency specifies the maximum number of tasks that can
// coexist at the same time. If it is zero, no tasks will be run and MergeSeq
// never ends. It may wrap around. The maximum value of concurrency is -1.
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
