package async

import (
	"slices"
	"sync"
)

// Semaphore provides a way to bound asynchronous access to a resource.
// The callers can request access with a given weight.
//
// A Semaphore must not be shared by more than one [Executor].
type Semaphore struct {
	_ noCopy

	size    Weight64
	cur     Weight64
	waiters []*waiter
}

type Weight64 int64

// NewSemaphore creates a new weighted semaphore with the given maximum
// combined weight.
func NewSemaphore(n Weight64) *Semaphore {
	return &Semaphore{size: n}
}

// Acquire returns a [Task] that awaits until a weight of n is acquired from
// the semaphore, and then ends.
func (s *Semaphore) Acquire(n Weight64) Task {
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
		w.sm, w.co, w.n = s, co, n
		s.waiters = append(s.waiters, w)
		co.Cleanup(w)
		return co.Await().End()
	}
}

// TryAcquire acquires the semaphore with a weight of n without blocking.
// On success, returns true.
// On failure, returns false and leaves the semaphore unchanged.
func (s *Semaphore) TryAcquire(n Weight64) bool {
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
func (s *Semaphore) Release(n Weight64) {
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

func (s *Semaphore) cancel(w *waiter) {
	switch {
	case !w.acquired:
		if i := slices.Index(s.waiters, w); i != -1 {
			s.waiters = slices.Delete(s.waiters, i, i+1)
			if i == 0 && s.size > s.cur {
				s.notifyWaiters()
			}
		}
	case w.co.Exiting():
		s.cur -= w.n
		s.notifyWaiters()
	}
}

// A Mutex is a mutual exclusion lock.
// The zero value for a Mutex is an unlocked mutex.
//
// A Mutex must not be shared by more than one [Executor].
type Mutex struct {
	_ noCopy

	locked  bool
	waiters []*waiter
}

// Lock returns a [Task] that locks m.
// If the lock is already in use, the calling coroutine
// awaits until the mutex is available.
func (m *Mutex) Lock() Task {
	return func(co *Coroutine) Result {
		if !m.locked && len(m.waiters) == 0 {
			m.locked = true
			return co.End()
		}
		w := waiterPool.Get().(*waiter)
		w.sm, w.co = m, co
		m.waiters = append(m.waiters, w)
		co.Cleanup(w)
		return co.Await().End()
	}
}

// TryLock tries to lock m and reports whether it succeeded.
func (m *Mutex) TryLock() bool {
	success := !m.locked && len(m.waiters) == 0
	if success {
		m.locked = true
	}
	return success
}

// Unlock unlocks m.
// Unlock panics if m isn't locked.
func (m *Mutex) Unlock() {
	if !m.locked {
		panic("async(Mutex): unlock of unlocked mutex")
	}
	m.locked = false
	m.notifyWaiters()
}

func (m *Mutex) notifyWaiters() {
	if len(m.waiters) == 0 {
		return
	}
	w := m.waiters[0]
	w.acquired = true
	w.co.Resume()
	m.locked = true
	m.waiters = slices.Delete(m.waiters, 0, 1)
}

func (m *Mutex) cancel(w *waiter) {
	switch {
	case !w.acquired:
		if i := slices.Index(m.waiters, w); i != -1 {
			m.waiters = slices.Delete(m.waiters, i, i+1)
		}
	case w.co.Exiting():
		m.locked = false
		m.notifyWaiters()
	}
}

type waiter struct {
	sm       interface{ cancel(w *waiter) }
	co       *Coroutine
	n        Weight64
	acquired bool
}

func (w *waiter) Cleanup() {
	w.sm.cancel(w)
	*w = waiter{}
	waiterPool.Put(w)
}

var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}
