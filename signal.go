package async

// Event is the interface of any type that can be watched by a [Coroutine].
//
// The following types implement Event: [Signal], [State] and [Memo].
// Any type that embeds [Signal] also implements Event, e.g. [State].
type Event interface {
	addListener(co *Coroutine)
	removeListener(co *Coroutine)
}

// Signal is a type that implements [Event].
//
// Calling the Notify method of a Signal, in a [Task] function, resumes
// any [Coroutine] that is watching the Signal.
//
// A Signal must not be shared by more than one [Executor].
type Signal struct {
	listeners map[*Coroutine]struct{}
}

func (s *Signal) addListener(co *Coroutine) {
	listeners := s.listeners
	if listeners == nil {
		listeners = make(map[*Coroutine]struct{})
		s.listeners = listeners
	}
	listeners[co] = struct{}{}
}

func (s *Signal) removeListener(co *Coroutine) {
	delete(s.listeners, co)
}

// Notify resumes any [Coroutine] that is watching s.
//
// One should only call this method in a [Task] function.
func (s *Signal) Notify() {
	for co := range s.listeners {
		co.resume()
	}
}
