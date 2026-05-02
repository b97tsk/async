package async_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/b97tsk/async"
)

func BenchmarkAsyncGo(b *testing.B) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	b.ReportAllocs()

	for b.Loop() {
		myExecutor.SpawnBlocking(async.Go(
			context.Background(), &wg,
			func(context.Context) async.Task {
				return nil
			},
		))
	}

	wg.Wait()
}

func BenchmarkAsyncSleep(b *testing.B) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	b.ReportAllocs()

	sleep := func(d time.Duration) async.Task {
		return async.Sleep(d, &wg)
	}

	for b.Loop() {
		myExecutor.SpawnBlocking(sleep(0))
	}

	wg.Wait()
}

func BenchmarkAsyncGoWithContext(b *testing.B) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	b.ReportAllocs()

	sleep := func(d time.Duration) async.Task {
		ctx, _ := context.WithTimeout(context.Background(), d)
		return async.Go(ctx, &wg, func(ctx context.Context) async.Task {
			<-ctx.Done()
			return nil
		})
	}

	for b.Loop() {
		myExecutor.SpawnBlocking(sleep(0))
	}

	wg.Wait()
}

func BenchmarkAsyncGoWithTimeAfter(b *testing.B) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	b.ReportAllocs()

	sleep := func(d time.Duration) async.Task {
		return async.Go(
			context.Background(), &wg,
			func(ctx context.Context) async.Task {
				select {
				case <-ctx.Done():
					return async.Exit()
				case <-time.After(d):
					return nil
				}
			},
		)
	}

	for b.Loop() {
		myExecutor.SpawnBlocking(sleep(0))
	}

	wg.Wait()
}
