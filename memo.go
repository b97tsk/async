package async

import "path"

// A Memo is a State-like structure that carries a value that can only be set
// in an Operation-like function.
//
// A Memo is designed to have a value that is computed from other States.
// What make a Memo useful are that:
//   - A Memo can prevent unnecessary computations when it isn't used;
//   - A Memo can prevent unnecessary propagations when its value isn't
//     changed.
//
// To create a Memo, use [NewMemo] or [NewStrictMemo].
//
// A Memo must not be shared by more than one [Executor].
type Memo[T any] struct {
	state  State[T]
	task   Task
	stale  bool
	strict bool
}

// NewMemo returns a new non-strict [Memo].
// The arguments are used to initialize an internal [Task].
//
// One must pass a function that watches some States, computes a value from
// these States, and then updates the provided [State] if the value differs.
//
// Like any [Event], a Memo can be watched by multiple Tasks.
// The watch list increases and decreases over time.
// For a non-strict Memo, when the last Task in the list unwatches it,
// it does not immediately end its internal Task.
// Ending the internal Task would only put the Memo into a stale state because
// the Memo no longer detects dependency changes.
// By not immediately ending the internal Task, a non-strict Memo prevents
// an extra computation when a new Task watches it, provided that there are no
// dependency changes.
//
// On the other hand, a strict Memo immediately ends its internal Task whenever
// the last Task in the watch list unwatches it. The Memo becomes stale.
// The next time a new Task watches it, it has to make a fresh computation.
func NewMemo[T any](e *Executor, p string, f func(t *Task, s *State[T])) *Memo[T] {
	return new(Memo[T]).init(e, p, f, false)
}

// NewStrictMemo returns a new strict [Memo].
//
// See [NewMemo] for more information.
func NewStrictMemo[T any](e *Executor, p string, f func(t *Task, s *State[T])) *Memo[T] {
	return new(Memo[T]).init(e, p, f, true)
}

func (m *Memo[T]) init(e *Executor, p string, f func(t *Task, s *State[T]), strict bool) *Memo[T] {
	m.task.init(e, path.Clean(p), func(t *Task) Result {
		if !m.stale && len(m.state.listeners) == 0 {
			m.stale = true
			return t.End()
		}

		if m.stale {
			listeners := m.state.listeners
			defer func() { m.state.listeners = listeners }()
			m.state.listeners = nil
			m.stale = false
		}

		f(t, &m.state)

		return t.Await()
	})

	m.stale = true
	m.strict = strict

	return m
}

func (m *Memo[T]) addListener(t *Task) {
	m.state.addListener(t)

	if m.stale {
		m.task.run()
	}
}

func (m *Memo[T]) removeListener(t *Task) {
	m.state.removeListener(t)

	if len(m.state.listeners) == 0 && m.strict {
		m.stale = true
		m.task.end()
	}
}

// Get retrieves the value of m.
//
// One should only call this method in an [Operation] function.
func (m *Memo[T]) Get() T {
	if m.stale {
		m.task.run()
	}
	return m.state.value
}
