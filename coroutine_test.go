package async_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/b97tsk/async"
)

func TestSpawn(t *testing.T) {
	t.Run("AfterPanic", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		ok := false

		myExecutor.Spawn(async.Block(
			async.Defer(func(co *async.Coroutine) async.Result {
				co.RecoverFunc(func(v any) bool { return v == 42 })
				return co.End()
			}),
			async.Defer(async.Block(
				async.Spawn(async.End()),
				async.Do(func() { ok = true }),
			)),
			async.Panic(42),
		))

		if !ok {
			t.Fatal("Spawn(t) should not unwind when t doesn't panic.")
		}
	})
	t.Run("NoAwait", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		myExecutor.Spawn(async.Block(
			async.Defer(func(co *async.Coroutine) async.Result {
				co.RecoverFunc(func(v any) bool { return v == 42 })
				return co.End()
			}),
			func(co *async.Coroutine) async.Result {
				co.Spawn(async.Block(
					async.Defer(async.Panic(42)),
					async.Await(),
				))
				return co.End() // No awaiting here.
			},
			async.Do(func() { t.Error("co.Spawn(t) did not unwind when t panics.") }),
		))
	})
}

func TestSleep(t *testing.T) {
	t.Run("NonCancelable", func(t *testing.T) {
		var wg sync.WaitGroup // For keeping track of goroutines.

		var myExecutor async.Executor

		myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

		n := 0

		myExecutor.Spawn(async.Select(
			async.Sleep(context.Background(), &wg, 100*time.Millisecond),
			async.NonCancelable(async.Block(
				async.Sleep(context.Background(), &wg, 200*time.Millisecond),
				async.Do(func() { n++ }),
			)),
		))

		wg.Wait()

		if n != 1 {
			t.Fatalf("want 1, got %v", n)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		myExecutor.Spawn(async.NonCancelable(async.Block(
			async.Sleep(ctx, &wg, 200*time.Millisecond), // Still cancelable with a context.Context.
			async.Do(func() { n++ }),                    // Didn't run.
		)))

		wg.Wait()

		if n != 1 {
			t.Fatalf("want 1, got %v", n)
		}
	})
}
