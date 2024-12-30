package async_test

import (
	"fmt"
	"sync"
	"time"

	"github.com/b97tsk/async"
)

func ExampleWaitGroup() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			myExecutor.Run()
		}()
	})

	var myState struct {
		wg     async.WaitGroup
		v1, v2 int
	}

	myState.wg.Add(2) // Note that async.WaitGroup is not safe for concurrent use.

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond) // Heavy work #1 here.
		ans := 15
		myExecutor.Spawn("/", async.Do(func() {
			myState.v1 = ans
			myState.wg.Done()
		}))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond) // Heavy work #2 here.
		ans := 27
		myExecutor.Spawn("/", async.Do(func() {
			myState.v2 = ans
			myState.wg.Done()
		}))
	}()

	myExecutor.Spawn("/", myState.wg.Await().Then(async.Do(func() {
		fmt.Println("v1 + v2 =", myState.v1+myState.v2)
	})))

	wg.Wait()

	// Output:
	// v1 + v2 = 42
}
