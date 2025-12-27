package async_test

import (
	"sync"
	"testing"
	"time"

	"github.com/b97tsk/async"
)

func TestSignal(t *testing.T) {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	sleep := func(d time.Duration) async.Task {
		return func(co *async.Coroutine) async.Result {
			var sig async.Signal
			wg.Add(1) // Keep track of timers too.
			tm := time.AfterFunc(d, func() {
				defer wg.Done()
				myExecutor.Spawn(async.Do(sig.Notify))
			})
			co.CleanupFunc(func() {
				if tm.Stop() {
					wg.Done()
				}
			})
			return co.Await(&sig).End()
		}
	}

	var sig async.Signal

	myExecutor.Spawn(async.LoopN(4, async.Block(
		sleep(100*time.Millisecond),
		async.Do(sig.Notify),
	)))

	myExecutor.Spawn(async.MergeSeq(10, func(yield func(async.Task) bool) {
		for i := range 100 {
			t := async.Select(
				async.Await(&sig),
				sleep(time.Duration(4+i%5)*10*time.Millisecond),
			)
			if !yield(t) {
				return
			}
		}
	}))

	wg.Wait()
}
