package async

import "slices"

// Semaphore provides a way to bound asynchronous access to a resource.
// The callers can request access with a given weight.
//
// Note that this Semaphore type does not provide backpressure for spawning
// a lot of Tasks. One should instead look for a sync implementation.
//
// A Semaphore must not be shared by more than one [Executor].
type Semaphore struct {
	size    int64
	cur     int64
	waiters []*waiter
}

type waiter struct {
	Signal
	n int64
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
			w := &waiter{n: n}
			s.waiters = append(s.waiters, w)
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
		w.Notify()
	}
	s.waiters = slices.Delete(s.waiters, 0, i)
}
