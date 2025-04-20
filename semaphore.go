package async

import "slices"

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
			w := cacheFor[waiter](co, keyFor[waiter](), newFor[waiter]())
			w.s, w.n = s, n
			s.waiters = append(s.waiters, w)
			co.Cleanup(w)
			co.Watch(w)
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
	i := 0
	for i = range s.waiters {
		w := s.waiters[i]
		if s.size-s.cur < w.n {
			break
		}
		s.cur += w.n
		w.n = 0
		w.Notify()
	}
	s.waiters = slices.Delete(s.waiters, 0, i)
}

type waiter struct {
	Signal
	s *Semaphore
	n int64
}

func (w *waiter) Cleanup() {
	if w.n != 0 {
		w.s.removeWaiter(w)
	}
	w.s = nil
}

func (s *Semaphore) removeWaiter(w *waiter) {
	if i := slices.Index(s.waiters, w); i != -1 {
		s.waiters = slices.Delete(s.waiters, i, i+1)
	}
}
