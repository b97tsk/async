package async_test

import (
	"fmt"
	"sync"
	"time"

	"github.com/b97tsk/async"
)

func ExampleSemaphore() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			myExecutor.Run()
		}()
	})

	mySemaphore := async.NewSemaphore(12)

	for n := int64(1); n <= 8; n++ {
		myExecutor.Spawn(mySemaphore.Acquire(n).Then(async.Do(func() {
			fmt.Println(n)
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(100 * time.Millisecond)
				myExecutor.Spawn(async.Do(func() { mySemaphore.Release(n) }))
			}()
		})))
	}

	wg.Wait()

	// Output:
	// 1
	// 2
	// 3
	// 4
	// 5
	// 6
	// 7
	// 8
}

func ExampleSemaphore_cancel() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			myExecutor.Run()
		}()
	})

	mySemaphore := async.NewSemaphore(3)

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		// Four Acquire calls, only two of them can succeed;
		// the other two get canceled later when co ends.
		for n := int64(1); n <= 4; n++ {
			co.Spawn(mySemaphore.Acquire(n).Then(async.Do(func() {
				fmt.Println(n)
			})))
		}

		co.Escape()
		wg.Add(1)
		go func(outer *async.Coroutine) {
			defer wg.Done()
			time.Sleep(100 * time.Millisecond)
			myExecutor.Spawn(async.Do(func() {
				outer.Unescape()
				outer.Resume()
			}))
		}(co)

		return co.Await().End()
	})

	wg.Wait()

	// Output:
	// 1
	// 2
}
