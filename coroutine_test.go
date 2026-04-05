package async_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/b97tsk/async"
)

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
