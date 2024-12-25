package async

// Event is the interface of any type that can be watched by a [Task].
//
// The following types implement Event: [Signal], [State] and [Memo].
type Event interface {
	addListener(t *Task)
	removeListener(t *Task)
}

// Signal is a type that implements [Event].
//
// Calling the Notify method of a Signal, in an [Operation] function, resumes
// any [Task] that is watching the Signal.
//
// A Signal must not be shared by more than one [Executor].
type Signal struct {
	listeners map[*Task]struct{}
}

func (s *Signal) addListener(t *Task) {
	listeners := s.listeners
	if listeners == nil {
		listeners = make(map[*Task]struct{})
		s.listeners = listeners
	}
	listeners[t] = struct{}{}
}

func (s *Signal) removeListener(t *Task) {
	delete(s.listeners, t)
}

// Notify resumes any [Task] that is watching s.
//
// One should only call this method in an [Operation] function.
func (s *Signal) Notify() {
	for t := range s.listeners {
		t.wake()
	}
}
