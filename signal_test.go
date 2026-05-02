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

	var sig async.Signal

	myExecutor.Spawn(async.LoopN(4, async.Block(
		async.Sleep(100*time.Millisecond, &wg),
		async.Do(sig.Notify),
	)))

	myExecutor.Spawn(async.MergeSeq(10, func(yield func(async.Task) bool) {
		for i := range 100 {
			t := async.Select(
				async.Await(&sig),
				async.Sleep(time.Duration(4+i%5)*10*time.Millisecond, &wg),
			)
			if !yield(t) {
				return
			}
		}
	}))

	wg.Wait()
}
