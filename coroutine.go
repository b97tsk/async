package async

import (
	"iter"
	"slices"
	"weak"
)

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
// A coroutine can also make a transit to work on another task according to
// the return value of the task function.
// A coroutine can transit from one task to another until a task ends it.
type Coroutine struct {
	flag        uint8
	level       uint32
	weight      Weight
	outer       *Coroutine
	executor    *Executor
	task        Task
	deps        map[Event]bool
	cleanups    []Cleanup
	defers      []Task
	controllers []controller
	cache       map[any]cacheEntry
}

// Weight is the type of weight for use when spawning a weighted coroutine.
type Weight int

type cacheEntry interface {
	isDead() bool
}

func (e *Executor) newCoroutine() *Coroutine {
	if co := e.pool.Get(); co != nil {
		return co.(*Coroutine)
	}
	return new(Coroutine)
}

func (e *Executor) freeCoroutine(co *Coroutine) {
	if co.flag&(flagRecyclable|flagRecycled) == flagRecyclable {
		co.flag |= flagRecycled
		co.executor = nil
		co.task = nil
		if len(co.cache) != 0 {
			removeRandomDeadEntries(co.cache)
		}
		e.pool.Put(co)
	}
}

func (co *Coroutine) init(e *Executor, t Task) *Coroutine {
	co.flag = flagStale
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
	if c := compare(co.level, other.level); c != 0 {
		return c == -1
	}
	return co.weight > other.weight
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
		co.clearCleanups()

		co.flag &^= flagStale | flagEnded

		if !pc.TryCatch(func() { res = co.task(co) }) {
			res = co.Exit()
		}

		if res.action != doYield && res.action != doTransit {
			controllers := co.controllers
			for len(controllers) != 0 {
				i := len(controllers) - 1
				c := controllers[i]
				if !pc.TryCatch(func() { res = c.negotiate(co, res) }) {
					res = c.negotiate(co, co.Exit())
				}
				if res.action != doTransit {
					if c, ok := c.(Cleanup); ok {
						c.Cleanup()
					}
					controllers[i] = nil
					controllers = controllers[:i]
					co.controllers = controllers
				}
				if res.action == doTransit || res.action == doTailTransit {
					break
				}
			}
			if res.action != doTransit && res.action != doTailTransit {
				if !pc.TryCatch(func() { res = rootController.negotiate(co, res) }) {
					res = rootController.negotiate(co, co.Exit())
				}
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
			if _, ok := res.controller.(funcController); ok {
				lastController := rootController
				if n := len(co.controllers); n != 0 {
					lastController = co.controllers[n-1]
				}
				if _, ok := lastController.(funcController); ok {
					// Tail-call optimization:
					// If the last controller is also a funcController, do not add another one.
					// (doTailTransit also pays tribute to this optimization.)
					addController = false
				}
			}
			if addController {
				co.controllers = append(co.controllers, res.controller)
				if capSizeLimit := 1000000; cap(co.controllers) > capSizeLimit {
					co.task = func(co *Coroutine) Result {
						panic("async: too many controllers or recursions")
					}
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

	if res.action != doYield {
		co.end()
	}
}

func (co *Coroutine) end() {
	if co.flag&flagEnded != 0 {
		return
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

func (co *Coroutine) clearCleanups() {
	pc := &co.executor.pc
	cleanups := co.cleanups
	for len(co.cleanups) != 0 {
		cleanups := co.cleanups
		co.cleanups = nil
		for _, c := range slices.Backward(cleanups) {
			pc.TryCatch(c.Cleanup)
		}
	}
	clear(cleanups)
	co.cleanups = cleanups[:0]
}

func (co *Coroutine) removeFromParent() {
	outer := co.outer
	if outer == nil {
		return
	}
	for i, c := range outer.cleanups {
		if c == (*innerCoroutineCleanup)(co) {
			outer.cleanups = slices.Delete(outer.cleanups, i, i+1)
			break
		}
	}
	co.outer = nil
}

type innerCoroutineCleanup Coroutine

func (inner *innerCoroutineCleanup) Cleanup() {
	inner.task = Exit()
	inner.outer = nil
	(*Coroutine)(inner).run()
	if inner.flag&flagEnded == 0 {
		panic("async: inner coroutine did not end (internal error)")
	}
}

// Executor returns the executor that spawned co.
//
// Since co can be recycled by an executor, it is recommended to save
// the return value in a variable first.
func (co *Coroutine) Executor() *Executor {
	return co.executor
}

// Exiting reports whether co is exiting.
func (co *Coroutine) Exiting() bool {
	return co.flag&flagExiting != 0
}

// Watch watches some events so that, when any of them notifies, co resumes.
func (co *Coroutine) Watch(ev ...Event) {
	if co.Exiting() {
		return
	}
	for _, d := range ev {
		deps := co.deps
		if deps == nil {
			deps = make(map[Event]bool)
			co.deps = deps
		}
		deps[d] = true
		d.addListener(co)
	}
}

// WatchSeq watches a sequence of events from seq so that, when any of them
// notifies, co resumes.
func (co *Coroutine) WatchSeq(seq iter.Seq[Event]) {
	if co.Exiting() {
		return
	}
	for d := range seq {
		deps := co.deps
		if deps == nil {
			deps = make(map[Event]bool)
			co.deps = deps
		}
		deps[d] = true
		d.addListener(co)
	}
}

// Cleanup represents any type that carries a Cleanup method.
// A Cleanup can be added to a coroutine in a [Task] function for making
// an effect some time later when the coroutine resumes or ends, or when
// the coroutine is making a transit to work on another [Task].
type Cleanup interface {
	Cleanup()
}

// A CleanupFunc is a func() that implements the [Cleanup] interface.
type CleanupFunc func()

// Cleanup implements the [Cleanup] interface.
func (f CleanupFunc) Cleanup() { f() }

// Cleanup adds something to clean up when co resumes or ends, or when co is
// making a transit to work on another [Task].
func (co *Coroutine) Cleanup(c Cleanup) {
	if c == nil {
		return
	}
	co.cleanups = append(co.cleanups, c)
}

// CleanupFunc adds a function call when co resumes or ends, or when co is
// making a transit to work on another [Task].
func (co *Coroutine) CleanupFunc(f func()) {
	if f == nil {
		return
	}
	co.Cleanup(CleanupFunc(f))
}

// Defer adds a [Task] for execution when returning from a [Func].
// Deferred tasks are executed in last-in-first-out (LIFO) order.
func (co *Coroutine) Defer(t Task) {
	if t == nil {
		return
	}
	co.defers = append(co.defers, t)
}

// Spawn creates an inner coroutine with default weight to work on t.
//
// Inner coroutines are ended automatically when the outer one resumes or
// ends, or when the outer one is making a transit to work on another task.
func (co *Coroutine) Spawn(t Task) {
	co.SpawnWeighted(0, t)
}

// SpawnWeighted creates an inner coroutine with weight w to work on t.
//
// Inner coroutines are ended automatically when the outer one resumes or
// ends, or when the outer one is making a transit to work on another task.
func (co *Coroutine) SpawnWeighted(w Weight, t Task) {
	level := co.level + 1
	if level == 0 {
		panic("async: too many levels")
	}

	inner := co.executor.newCoroutine().init(co.executor, t).recyclable().withLevel(level).withWeight(w)
	inner.run()

	if inner.flag&flagEnded == 0 {
		inner.outer = co
		co.cleanups = append(co.cleanups, (*innerCoroutineCleanup)(inner))
	}
}

// Result is the type of the return value of a [Task] function.
// A Result determines what next for a coroutine to do after running a task.
//
// A Result can be created by calling one of the following methods:
//   - [Coroutine.End]: for ending a coroutine or transiting to another task;
//   - [Coroutine.Await]: for yielding a coroutine with additional events to
//     watch;
//   - [Coroutine.Yield]: for yielding a coroutine with another task to which
//     will be transited later when resuming;
//   - [Coroutine.Transit]: for transiting to another task;
//   - [Coroutine.Break]: for breaking a loop;
//   - [Coroutine.BreakLabel]: for breaking a loop with a specific label;
//   - [Coroutine.Continue]: for continuing a loop;
//   - [Coroutine.ContinueLabel]: for continuing a loop with a specific label;
//   - [Coroutine.Return]: for returning from a [Func];
//   - [Coroutine.Exit]: for exiting a coroutine.
//
// These methods may have side effects. One should never store a Result in
// a variable and overwrite it with another, before returning it. Instead,
// one should just return a Result right after it is created.
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
//
// Yielding is not allowed when a coroutine is exiting.
// If co is exiting, Await panics.
func (co *Coroutine) Await(ev ...Event) Result {
	if co.Exiting() {
		panic("async: yielding while exiting")
	}
	if len(ev) != 0 {
		co.Watch(ev...)
	}
	return Result{action: doYield}
}

// AwaitSeq returns a [Result] that will cause co to yield.
// AwaitSeq also accepts a sequence of events from seq to watch.
//
// Yielding is not allowed when a coroutine is exiting.
// If co is exiting, AwaitSeq panics.
func (co *Coroutine) AwaitSeq(seq iter.Seq[Event]) Result {
	if co.Exiting() {
		panic("async: yielding while exiting")
	}
	co.WatchSeq(seq)
	return Result{action: doYield}
}

// Yield returns a [Result] that will cause co to yield and, when co is resumed,
// make a transit to work on t instead.
//
// Yielding is not allowed when a coroutine is exiting.
// If co is exiting, Yield panics.
func (co *Coroutine) Yield(t Task) Result {
	if co.Exiting() {
		panic("async: yielding while exiting")
	}
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
			action:     doTransit,
			transitTo:  must(t),
			controller: &thenController{must(next)},
		}
	}
}

type thenController struct {
	next Task
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

// AwaitSeq returns a [Task] that awaits a sequence of events from seq until
// any of them notifies, and then ends.
// If seq is empty, AwaitSeq returns a [Task] that never ends.
func AwaitSeq(seq iter.Seq[Event]) Task {
	return func(co *Coroutine) Result {
		co.WatchSeq(seq)
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
			controller: &blockController{s[1:]},
		}
	}
}

type blockController struct {
	s []Task
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
func LoopN[Int intType](n Int, t Task) Task {
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
			controller: &loopController{l, t},
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
func LoopLabelN[Int intType](l Label, n Int, t Task) Task {
	return func(co *Coroutine) Result {
		i := Int(0)
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
			controller: &loopController{l, u},
		}
	}
}

type intType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type loopController struct {
	l Label
	t Task
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
		defers := co.defers
		if len(defers) == int(c) {
			if co.Exiting() {
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

var rootController controller = funcController(0)

// FromSeq returns a [Task] that runs each of the tasks from seq in sequence.
func FromSeq(seq iter.Seq[Task]) Task {
	return func(co *Coroutine) Result {
		next, stop := iter.Pull(seq)
		return Result{
			action:     doTransit,
			transitTo:  End(),
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
			return co.Transit(t)
		}
	}
	return res
}

func (c *seqController) Cleanup() {
	c.stop()
}

func keyFor[T any]() any {
	type key struct{}
	return key{}
}

func newFor[T any]() func() *T {
	return func() *T { return new(T) }
}

type weakPointer[T any] struct {
	weak.Pointer[T]
}

func (p weakPointer[T]) isDead() bool {
	return p.Value() == nil
}

func cacheFor[T any](co *Coroutine, key any, new func() *T) *T {
	cache := co.cache
	if cache == nil {
		cache = make(map[any]cacheEntry)
		co.cache = cache
	}
	if len(cache) != 0 {
		removeRandomDeadEntries(cache)
	}
	wp, _ := cache[key].(weakPointer[T])
	v := wp.Value()
	if v == nil && new != nil {
		v = new()
		cache[key] = weakPointer[T]{weak.Make(v)}
	}
	return v
}

func removeRandomDeadEntries(m map[any]cacheEntry) {
	const maxNonDeadEncountered = 10
	n := 0
	for k, v := range m {
		if v.isDead() {
			delete(m, k)
			continue
		}
		if n++; n == maxNonDeadEncountered {
			break
		}
	}
}

// Join returns a [Task] that runs each of the given tasks in an inner
// coroutine and awaits until all of them complete, and then ends.
func Join(s ...Task) Task {
	key := new(int)
	return func(co *Coroutine) Result {
		type object struct {
			wg    WaitGroup
			tasks []Task
		}
		o := cacheFor[object](co, key, func() *object {
			o := new(object)
			done := func(co *Coroutine) Result {
				o.wg.Done()
				return co.End()
			}
			tasks := slices.Clone(s)
			for i, t := range tasks {
				tasks[i] = func(co *Coroutine) Result {
					co.Defer(done)
					return co.Transit(t)
				}
			}
			o.tasks = tasks
			return o
		})
		co.Watch(&o.wg)
		o.wg.Add(len(o.tasks))
		for _, t := range o.tasks {
			co.Spawn(t)
		}
		return co.Yield(End())
	}
}

// Select returns a [Task] that runs each of the given tasks in an inner
// coroutine and awaits until any of them completes, and then ends.
// When Select ends, tasks other than the one that completes are forcely
// exited.
func Select(s ...Task) Task {
	key := new(int)
	return func(co *Coroutine) Result {
		type object struct {
			sig   Signal
			done  bool
			tasks []Task
		}
		o := cacheFor[object](co, key, func() *object {
			o := new(object)
			done := func(co *Coroutine) Result {
				o.sig.Notify()
				o.done = true
				return co.End()
			}
			tasks := slices.Clone(s)
			for i, t := range tasks {
				tasks[i] = func(co *Coroutine) Result {
					co.Defer(done)
					return co.Transit(t)
				}
			}
			o.tasks = tasks
			return o
		})
		co.Watch(&o.sig)
		o.done = false
		for _, t := range o.tasks {
			co.Spawn(t)
			if o.done {
				return co.End()
			}
		}
		return co.Yield(End())
	}
}
