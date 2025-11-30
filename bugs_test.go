package async_test

import (
	"testing"

	"github.com/b97tsk/async"
)

func TestBugs(t *testing.T) {
	t.Run("Semaphore", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		lock := async.NewSemaphore(1)

		myExecutor.Spawn(async.Select(
			async.Block(
				lock.Acquire(1),
				lock.Acquire(1),
			),
			async.Do(func() { lock.Release(1) }),
		))

		var acquired bool

		myExecutor.Spawn(async.Block(
			lock.Acquire(1),
			async.Do(func() { acquired = true }),
		))

		if !acquired {
			t.Fail()
		}
	})
}
