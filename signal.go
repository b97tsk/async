package async

// Event is the interface of any type that can be watched by a coroutine.
//
// The following types implement Event: [Signal], [State] and [Memo].
// Any type that embeds [Signal] also implements Event, e.g. [State].
type Event interface {
	addListener(co *Coroutine)
	pauseListener(co *Coroutine)
	removeListener(co *Coroutine)
}

// Signal is a type that implements [Event].
//
// Calling the Notify method of a signal, in a [Task] function, resumes
// any coroutine that is watching the signal.
//
// A Signal must not be shared by more than one [Executor].
type Signal struct {
	listeners map[*Coroutine]bool
}

func (s *Signal) addListener(co *Coroutine) {
	listeners := s.listeners
	if listeners == nil {
		listeners = make(map[*Coroutine]bool)
		s.listeners = listeners
	}
	listeners[co] = true
}

func (s *Signal) pauseListener(co *Coroutine) {
	s.listeners[co] = false
}

func (s *Signal) removeListener(co *Coroutine) {
	delete(s.listeners, co)
}

// Notify resumes any coroutine that is watching s.
//
// One should only call this method in a [Task] function.
func (s *Signal) Notify() {
	var e *Executor

	for co, listening := range s.listeners {
		if !listening {
			continue
		}

		if e == nil {
			e = co.executor

			e.mu.Lock()
			defer e.mu.Unlock()

			if !e.running {
				panic("async(Signal): executor not running")
			}
		}

		if e != co.executor {
			panic("async(Signal): executor inconsistent")
		}

		e.resumeCoroutine(co)
	}
}
