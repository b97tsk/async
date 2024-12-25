package async

// A State is a [Signal] that carries a value.
// To retrieve the value, call the Get method.
//
// Calling the Set method of a State, in an [Operation] function, updates
// the value and resumes any [Task] that is watching the State.
//
// A State must not be shared by more than one [Executor].
type State[T any] struct {
	Signal
	value T
}

// NewState returns a new [State] with its initial value set to v.
func NewState[T any](v T) *State[T] {
	return &State[T]{value: v}
}

// Get retrieves the value of s.
//
// Without proper synchronization, one should only call this method in
// an [Operation] function.
func (s *State[T]) Get() T {
	return s.value
}

// Set updates the value of s and resumes any [Task] that is watching s.
//
// One should only call this method in an [Operation] function.
func (s *State[T]) Set(v T) {
	s.value = v
	s.Notify()
}
