package async

import "sync"

// An Executor is a coroutine spawner, and a coroutine runner.
//
// When a coroutine is spawned or resumed, it is added into an internal queue.
// The Run method then pops and runs each of them from the queue until
// the queue is emptied.
// It is done in a single-threaded manner.
// If one coroutine blocks, no other coroutines can run.
// The best practice is not to block.
//
// The internal queue is a priority queue.
// Coroutines added in the queue are sorted by their levels
// (inner coroutines have one level higher than their outer ones).
// Coroutines with the same level are sorted by their arrival order (FIFO).
// Popping the queue removes the first coroutine with the least level.
//
// Manually calling the Run method is usually not desired.
// One would instead use the Autorun method to set up an autorun function to
// calling the Run method automatically whenever a coroutine is spawned or
// resumed.
// An executor never calls the autorun function twice at the same time.
type Executor struct {
	mu      sync.Mutex
	pq      priorityqueue[*Coroutine]
	pc      paniccatcher
	running bool
	autorun func()
	pool    sync.Pool
}

// Autorun sets up an autorun function to calling the Run method automatically
// whenever a coroutine is spawned or resumed.
//
// One must pass a function that calls the Run method.
//
// If f blocks, the Spawn method may block too.
// The best practice is not to block.
func (e *Executor) Autorun(f func()) {
	e.mu.Lock()
	e.autorun = f
	e.mu.Unlock()
}

// Run pops and runs every coroutine in the queue until the queue is emptied.
//
// Run must not be called twice at the same time.
func (e *Executor) Run() {
	e.mu.Lock()
	e.running = true

	for !e.pq.Empty() {
		co := e.pq.Pop()
		e.runCoroutine(co)
	}

	pc := e.pc
	e.pc.Reset()

	e.running = false
	e.mu.Unlock()

	pc.Rethrow()
}

// Spawn creates a coroutine to work on t.
//
// The coroutine is added in a queue. To run it, either call the Run method, or
// call the Autorun method to set up an autorun function beforehand.
//
// Spawn is safe for concurrent use.
func (e *Executor) Spawn(t Task) {
	var autorun func()

	co := e.newCoroutine().init(e, t).recyclable()

	e.mu.Lock()

	if !e.running && e.autorun != nil {
		e.running = true
		autorun = e.autorun
	}

	e.pq.Push(co)
	e.mu.Unlock()

	if autorun != nil {
		autorun()
	}
}
