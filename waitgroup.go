package async

// A WaitGroup is a [Signal] with a counter.
//
// Calling the Add or Done method of a WaitGroup, in a [Task] function,
// updates the counter and, when the counter becomes zero, resumes any
// [Coroutine] that is watching the WaitGroup.
//
// A WaitGroup must not be shared by more than one [Executor].
type WaitGroup struct {
	Signal
	n int
}

// Add adds delta, which may be negative, to the [WaitGroup] counter.
// If the [WaitGroup] counter becomes zero, Add resumes any [Coroutine] that
// is watching wg.
// If the [WaitGroup] counter is negative, Add panics.
func (wg *WaitGroup) Add(delta int) {
	if wg.n >= 0 {
		wg.n += delta
	}
	if wg.n < 0 {
		panic("async(WaitGroup): negative counter")
	}
	if wg.n == 0 && delta != 0 {
		wg.Notify()
	}
}

// Done decrements the [WaitGroup] counter by one.
func (wg *WaitGroup) Done() {
	wg.Add(-1)
}

// Await returns a [Task] that awaits until the [WaitGroup] counter becomes
// zero, and then ends.
func (wg *WaitGroup) Await() Task {
	return func(co *Coroutine) Result {
		if wg.n != 0 {
			return co.Await(wg)
		}
		return co.End()
	}
}
