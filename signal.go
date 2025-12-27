package async

// Event is the interface of any type that can be watched by a coroutine.
//
// The following types implement Event: [Signal] and [State].
// Any type that embeds [Signal] also implements Event, e.g. [State].
type Event interface {
	addListener(co *Coroutine)
	removeListener(co *Coroutine)
}

// Signal is a type that implements [Event].
//
// Calling the Notify method of a signal, in a [Task] function, resumes
// any coroutine that is watching the signal.
//
// A Signal must not be shared by more than one [Executor].
type Signal struct {
	_ noCopy

	a [1]*Coroutine
	m map[*Coroutine]struct{}
}

func (s *Signal) addListener(co *Coroutine) {
	for i, v := range &s.a {
		if v == nil {
			s.a[i] = co
			return
		}
	}
	m := s.m
	if m == nil {
		m = make(map[*Coroutine]struct{})
		s.m = m
	}
	m[co] = struct{}{}
}

func (s *Signal) removeListener(co *Coroutine) {
	for i, v := range &s.a {
		if v == co {
			s.a[i] = nil
			return
		}
	}
	delete(s.m, co)
}

// Notify resumes any coroutine that is watching s.
//
// One should only call this method in a [Task] function.
func (s *Signal) Notify() {
	if s.anyListeners() {
		s.notify()
	}
}

func (s *Signal) anyListeners() bool {
	for _, co := range &s.a {
		if co != nil {
			return true
		}
	}
	return len(s.m) != 0
}

func (s *Signal) notify() {
	var e *Executor
	defer func() {
		if e != nil {
			e.mu.Unlock()
		}
	}()
	s.walk(func(co *Coroutine) {
		if e == nil {
			e = co.executor
			e.mu.Lock()
			if !e.running {
				panic("async(Signal): executor not running")
			}
		}
		if e != co.executor {
			panic("async(Signal): executor inconsistent")
		}
		e.resumeCoroutine(co, false)
	})
}

func (s *Signal) walk(f func(co *Coroutine)) {
	for _, co := range &s.a {
		if co != nil {
			f(co)
		}
	}
	for co := range s.m {
		f(co)
	}
}
