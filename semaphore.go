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
	size    Weight
	cur     Weight
	waiters []*waiter
}

// NewSemaphore creates a new weighted semaphore with the given maximum
// combined weight.
func NewSemaphore(n Weight) *Semaphore {
	return &Semaphore{size: n}
}

// Acquire returns a [Task] that awaits until a weight of n is acquired from
// the semaphore, and then ends.
func (s *Semaphore) Acquire(n Weight) Task {
	if n < 0 {
		panic("async(Semaphore): negative weight")
	}
	return func(co *Coroutine) Result {
		if s.size-s.cur >= n && len(s.waiters) == 0 {
			s.cur += n
			return co.End()
		}
		if n > s.size {
			return co.Yield() // Impossible to success.
		}
		w := waiterPool.Get().(*waiter)
		w.co, w.s, w.n = co, s, n
		s.waiters = append(s.waiters, w)
		co.Cleanup(w)
		return co.Await().End()
	}
}

// TryAcquire acquires the semaphore with a weight of n without blocking.
// On success, returns true.
// On failure, returns false and leaves the semaphore unchanged.
//
// Without proper synchronization, one should only call this method in a [Task]
// function.
func (s *Semaphore) TryAcquire(n Weight) bool {
	if n < 0 {
		panic("async(Semaphore): negative weight")
	}
	success := s.size-s.cur >= n && len(s.waiters) == 0
	if success {
		s.cur += n
	}
	return success
}

// Release releases the semaphore with a weight of n.
//
// One should only call this method in a [Task] function.
func (s *Semaphore) Release(n Weight) {
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
		w.acquired = true
		w.co.Resume()
		n++
	}
	s.waiters = slices.Delete(s.waiters, 0, n)
}

type waiter struct {
	co       *Coroutine
	s        *Semaphore
	n        Weight
	acquired bool
}

func (w *waiter) Cleanup() {
	switch {
	case !w.acquired:
		s := w.s
		if i := slices.Index(s.waiters, w); i != -1 {
			s.waiters = slices.Delete(s.waiters, i, i+1)
			if i == 0 && s.size > s.cur {
				s.notifyWaiters()
			}
		}
	case w.co.Exiting():
		s, n := w.s, w.n
		s.cur -= n
		s.notifyWaiters()
	}
	*w = waiter{}
	waiterPool.Put(w)
}

var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}
