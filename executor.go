package async

import (
	"path"
	"sync"
)

// An Executor is a [Task] spawner, and a Task runner.
//
// When a Task is spawned or resumed, it is added into an internal queue.
// The Run method then pops and runs each of them from the queue until
// the queue is emptied.
// It is done in a single-threaded manner.
// If one Task blocks, no other Tasks can run.
// The best practice is not to block.
//
// The internal queue is a priority queue.
// Tasks added in the queue are sorted by their paths.
// Tasks with the same path are sorted by their arrival order (FIFO).
// Popping the queue removes the first Task with the least path.
//
// Manually calling the Run method is usually not desired.
// One would instead use the Autorun method to set up an autorun function to
// calling the Run method automatically whenever a Task is spawned or resumed.
// The Executor never calls the autorun function twice at the same time.
type Executor struct {
	mu      sync.Mutex
	pq      priorityqueue[*Task]
	running bool
	autorun func()
	pool    sync.Pool
}

// Autorun sets up an autorun function to calling the Run method automatically
// whenever a [Task] is spawned or resumed.
//
// One must pass a function that calls the Run method.
//
// If f blocks, the Spawn method may block too.
// The best practice is not to block.
func (e *Executor) Autorun(f func()) {
	e.autorun = f
}

// Run pops and runs every [Task] in the queue until the queue is emptied.
//
// Run must not be called twice at the same time.
func (e *Executor) Run() {
	e.mu.Lock()
	e.running = true

	for !e.pq.Empty() {
		t := e.pq.Pop()
		e.runTask(t)
	}

	e.running = false
	e.mu.Unlock()
}

// Spawn creates a [Task] to work on op, using the result of path.Clean(p) as
// its path.
//
// The Task is added in a queue. To run it, either call the Run method, or
// call the Autorun method to set up an autorun function beforehand.
//
// Spawn is safe for concurrent use.
func (e *Executor) Spawn(p string, op Operation) {
	t := e.newTask().init(e, path.Clean(p), op).recyclable()
	e.resumeTask(t)
}

func (e *Executor) resumeTask(t *Task) {
	var autorun func()

	e.mu.Lock()

	if !e.running && e.autorun != nil {
		e.running = true
		autorun = e.autorun
	}

	e.pq.Push(t)
	e.mu.Unlock()

	if autorun != nil {
		autorun()
	}
}
