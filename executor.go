package async

import (
	"path"
	"sync"
)

// An Executor is a [Coroutine] spawner, and a Coroutine runner.
//
// When a Coroutine is spawned or resumed, it is added into an internal queue.
// The Run method then pops and runs each of them from the queue until
// the queue is emptied.
// It is done in a single-threaded manner.
// If one Coroutine blocks, no other Coroutines can run.
// The best practice is not to block.
//
// The internal queue is a priority queue.
// Coroutines added in the queue are sorted by their paths.
// Coroutines with the same path are sorted by their arrival order (FIFO).
// Popping the queue removes the first Coroutine with the least path.
//
// Manually calling the Run method is usually not desired.
// One would instead use the Autorun method to set up an autorun function to
// calling the Run method automatically whenever a Coroutine is spawned or
// resumed.
// The Executor never calls the autorun function twice at the same time.
type Executor struct {
	mu      sync.Mutex
	pq      priorityqueue[*Coroutine]
	running bool
	autorun func()
	pool    sync.Pool
}

// Autorun sets up an autorun function to calling the Run method automatically
// whenever a [Coroutine] is spawned or resumed.
//
// One must pass a function that calls the Run method.
//
// If f blocks, the Spawn method may block too.
// The best practice is not to block.
func (e *Executor) Autorun(f func()) {
	e.autorun = f
}

// Run pops and runs every [Coroutine] in the queue until the queue is emptied.
//
// Run must not be called twice at the same time.
func (e *Executor) Run() {
	e.mu.Lock()
	e.running = true

	for !e.pq.Empty() {
		co := e.pq.Pop()
		e.runCoroutine(co)
	}

	e.running = false
	e.mu.Unlock()
}

// Spawn creates a [Coroutine] to work on t, using the result of path.Clean(p)
// as its path.
//
// The Coroutine is added in a queue. To run it, either call the Run method, or
// call the Autorun method to set up an autorun function beforehand.
//
// Spawn is safe for concurrent use.
func (e *Executor) Spawn(p string, t Task) {
	var autorun func()

	co := e.newCoroutine().init(e, path.Clean(p), t).recyclable()

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
