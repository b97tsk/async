package async_test

import (
	"testing"

	"github.com/b97tsk/async"
)

func TestSemaphore(t *testing.T) {
	t.Run("Cancelable", func(t *testing.T) {
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

		if !sema.TryAcquire(1) {
			t.Fatal("TryAcquire did not succeed when there are no waiters.")
		}
	})
	t.Run("Fairness", func(t *testing.T) {
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

		if sema.TryAcquire(1) {
			t.Fatal("TryAcquire should not succeed when there are waiters.")
		}

		myExecutor.Spawn(async.Notify(&sig))

		if !sema.TryAcquire(1) {
			t.Fatal("TryAcquire did not succeed when there are no waiters.")
		}
	})
}

func TestMutex(t *testing.T) {
	t.Run("Cancelable", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		var mutex async.Mutex

		myExecutor.Spawn(async.Select(
			async.Block(
				mutex.Lock(),
				mutex.Lock(),
			),
			async.Do(func() { mutex.Unlock() }),
		))

		if !mutex.TryLock() {
			t.Fatal("TryUnlock did not succeed when there are no waiters.")
		}
	})
	t.Run("Fairness", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		var mutex async.Mutex

		var sig async.Signal

		myExecutor.Spawn(async.Select(
			async.Await(&sig),
			async.Block(
				mutex.Lock(),
				mutex.Lock(),
			),
		))

		if mutex.TryLock() {
			t.Fatal("TryLock should not succeed when there are waiters.")
		}

		myExecutor.Spawn(async.Notify(&sig))

		mutex.Unlock()
	})
}
