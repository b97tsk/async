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

	for b.Loop() {
		myExecutor.SpawnBlocking(async.Sleep(context.Background(), &wg, 0))
	}

	wg.Wait()
}

func BenchmarkTimeAfterFunc(b *testing.B) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	sleep := func(d time.Duration) async.Task {
		return func(co *async.Coroutine) async.Result {
			var sig async.Signal
			wg.Add(1)
			tm := time.AfterFunc(d, func() {
				defer wg.Done()
				myExecutor.Spawn(func(co *async.Coroutine) async.Result {
					sig.Notify()
					return co.End()
				})
			})
			co.CleanupFunc(func() {
				if tm.Stop() {
					wg.Done()
				}
			})
			return co.Await(&sig).End()
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		myExecutor.SpawnBlocking(sleep(0))
	}

	wg.Wait()
}
