package async

import "path"

// A Memo is a State-like structure that carries a value that can only be set
// in a Task-like function.
//
// A memo is designed to have a value that is computed from other states.
// What make a memo useful are that:
//   - A memo can prevent unnecessary computations when it isn't used;
//   - A memo can prevent unnecessary propagations when its value isn't
//     changed.
//
// To create a memo, use [NewMemo] or [NewStrictMemo].
//
// A Memo must not be shared by more than one [Executor].
type Memo[T any] struct {
	state  State[T]
	co     Coroutine
	stale  bool
	strict bool
}

// NewMemo returns a new non-strict [Memo].
// The arguments are used to initialize an internal coroutine.
//
// One must pass a function that watches some states, computes a value from
// these states, and then updates the given state if the value differs.
//
// Like any [Event], a memo can be watched by multiple coroutines.
// The watch list increases and decreases over time.
// For a non-strict memo, when the last coroutine in the list unwatches it,
// it does not immediately end its internal coroutine.
// Ending the internal coroutine would only put the memo into a stale state
// because the memo no longer detects dependency changes.
// By not immediately ending the internal coroutine, a non-strict memo prevents
// an extra computation when a new coroutine watches it, provided that there
// are no dependency changes.
//
// On the other hand, a strict memo immediately ends its internal coroutine
// whenever the last coroutine in the watch list unwatches it. The memo becomes
// stale. The next time a new coroutine watches it, it has to make a fresh
// computation.
func NewMemo[T any](e *Executor, p string, f func(co *Coroutine, s *State[T])) *Memo[T] {
	return new(Memo[T]).init(e, p, f, false)
}

// NewStrictMemo returns a new strict [Memo].
//
// See [NewMemo] for more information.
func NewStrictMemo[T any](e *Executor, p string, f func(co *Coroutine, s *State[T])) *Memo[T] {
	return new(Memo[T]).init(e, p, f, true)
}

func (m *Memo[T]) init(e *Executor, p string, f func(co *Coroutine, s *State[T]), strict bool) *Memo[T] {
	m.co.init(e, path.Clean(p), func(co *Coroutine) Result {
		if !m.stale && len(m.state.listeners) == 0 {
			m.stale = true
			return co.End()
		}

		if m.stale {
			listeners := m.state.listeners
			defer func() { m.state.listeners = listeners }()
			m.state.listeners = nil
			m.stale = false
		}

		f(co, &m.state)

		return co.Await()
	})

	m.stale = true
	m.strict = strict

	return m
}

func (m *Memo[T]) addListener(co *Coroutine) {
	m.state.addListener(co)

	if m.stale {
		m.co.run()
	}
}

func (m *Memo[T]) pauseListener(co *Coroutine) {
	m.state.pauseListener(co)
}

func (m *Memo[T]) removeListener(co *Coroutine) {
	m.state.removeListener(co)

	if len(m.state.listeners) == 0 && m.strict {
		m.stale = true
		m.co.end()
	}
}

// Get retrieves the value of m.
//
// One should only call this method in a [Task] function.
func (m *Memo[T]) Get() T {
	if m.stale {
		m.co.run()
	}
	return m.state.value
}
