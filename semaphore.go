package async

import (
	"slices"
	"sync"
)

// Semaphore provides a way to bound asynchronous access to a resource.
// The callers can request access with a given weight.
//
// Note that this Semaphore type does not provide backpressure for spawning
// a lot of tasks. One should instead look for a sync implementation.
//
// A Semaphore must not be shared by more than one [Executor].
type Semaphore struct {
	size    int64
	cur     int64
	waiters []*waiter
}

// NewSemaphore creates a new weighted semaphore with the given maximum
// combined weight.
func NewSemaphore(n int64) *Semaphore {
	return &Semaphore{size: n}
}

// Acquire returns a [Task] that awaits until a weight of n is acquired from
// the semaphore, and then ends.
func (s *Semaphore) Acquire(n int64) Task {
	if n < 0 {
		panic("async(Semaphore): negative weight")
	}
	return func(co *Coroutine) Result {
		if s.size-s.cur < n {
			if n > s.size {
				return co.Await() // Impossible to success.
			}
			w := waiterPool.Get().(*waiter)
			w.co, w.s, w.n = co, s, n
			s.waiters = append(s.waiters, w)
			co.Cleanup(w)
			return co.Yield(End())
		}
		s.cur += n
		return co.End()
	}
}

// Release releases the semaphore with a weight of n.
//
// One should only call this method in a [Task] function.
func (s *Semaphore) Release(n int64) {
	if n < 0 {
		panic("async(Semaphore): negative weight")
	}
	if s.cur >= 0 {
		s.cur -= n
	}
	if s.cur < 0 {
		panic("async(Semaphore): released more than held")
	}
	s.notifyWaiters()
}

func (s *Semaphore) notifyWaiters() {
	n := 0
	for _, w := range s.waiters {
		if s.size-s.cur < w.n {
			break
		}
		s.cur += w.n
		w.n = 0
		w.co.Resume()
		n++
	}
	s.waiters = slices.Delete(s.waiters, 0, n)
}

type waiter struct {
	co *Coroutine
	s  *Semaphore
	n  int64
}

func (w *waiter) Cleanup() {
	if w.n != 0 {
		w.s.removeWaiter(w)
	}
	w.co = nil
	w.s = nil
	waiterPool.Put(w)
}

func (s *Semaphore) removeWaiter(w *waiter) {
	if i := slices.Index(s.waiters, w); i != -1 {
		s.waiters = slices.Delete(s.waiters, i, i+1)
	}
}

var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}
