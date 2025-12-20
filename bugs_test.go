package async_test

import (
	"testing"

	"github.com/b97tsk/async"
)

func TestBugs(t *testing.T) {
	t.Run("Semaphore-1", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		sema := async.NewSemaphore(1)

		myExecutor.Spawn(async.Select(
			async.Block(
				sema.Acquire(1),
				sema.Acquire(1),
			),
			async.Do(func() { sema.Release(1) }),
		))

		var acquired bool

		myExecutor.Spawn(async.Block(
			sema.Acquire(1),
			async.Do(func() { acquired = true }),
		))

		if !acquired {
			t.Error("Acquire did not succeed when there are no waiters.")
		}
	})
	t.Run("Semaphore-2", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		sema := async.NewSemaphore(10)

		var sig async.Signal

		myExecutor.Spawn(async.Select(
			async.Await(&sig),
			async.Block(
				sema.Acquire(1),
				sema.Acquire(10),
			),
		))

		var acquired bool

		myExecutor.Spawn(async.Block(
			sema.Acquire(1),
			async.Do(func() { acquired = true }),
		))

		if acquired {
			t.Error("Acquire should not succeed when there are waiters.")
		}

		myExecutor.Spawn(async.Do(sig.Notify))

		if !acquired {
			t.Error("Acquire did not succeed when there are no waiters.")
		}
	})
}
