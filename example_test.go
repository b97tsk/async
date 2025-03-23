package async_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/b97tsk/async"
)

// This example demonstrates how to spawn tasks with different paths.
// The lower path, the higher priority.
// This example creates a task with path "aa" for additional computations
// and another task with path "zz" for printing results.
// The former runs before the latter because "aa" < "zz".
func Example() {
	// Create an executor.
	var myExecutor async.Executor

	// Set up an autorun function to run an executor automatically whenever a coroutine is spawned or resumed.
	// The best practice is to pass a function that does not block. See Example (NonBlocking).
	myExecutor.Autorun(myExecutor.Run)

	// Create two states.
	s1, s2 := async.NewState(1), async.NewState(2)

	// Although states can be created without the help of executors,
	// they might only be safe for use by one and only one executor because of data races.
	// Without proper synchronization, it's better only to spawn coroutines to read or update states.

	var sum, product async.State[int]

	myExecutor.Spawn("aa", func(co *async.Coroutine) async.Result { // The path of co is "aa".
		co.Watch(s1, s2) // Let co depend on s1 and s2, so co can re-run whenever s1 or s2 changes.
		sum.Set(s1.Get() + s2.Get())
		product.Set(s1.Get() * s2.Get())
		return co.Await() // Awaits signals or state changes.
	})

	// The above task re-runs whenever s1 or s2 changes. As an example, this is fine.
	// In practice, one should probably use memos to avoid unnecessary recomputations. See Example (Memo).

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result { // The path of co is "zz".
		co.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			// The path of an inner coroutine is relative to its outer one.
			co.Spawn("sum", func(co *async.Coroutine) async.Result { // The path of inner co is "zz/sum".
				fmt.Println("s1 + s2 =", sum.Get())
				return co.Await(&sum)
			})
		case '*':
			co.Spawn("product", func(co *async.Coroutine) async.Result { // The path of inner co is "zz/product".
				fmt.Println("s1 * s2 =", product.Get())
				return co.Await(&product)
			})
		}

		return co.Await()
	})

	fmt.Println("--- SEPARATOR ---")

	// The followings create several tasks to mutate states.
	// They share the same path, "/", which is lower than "aa" and "zz".
	// Remember that, the lower path, the higher priority.
	// Updating states should have higher priority, so that when there are multiple update tasks,
	// they can run together before any read task.
	// This reduces the number of reads that have to react on update.

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(3)
		s2.Set(4)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		op.Set('*')
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(5)
		s2.Set(6)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(7)
		s2.Set(8)
		op.Set('+')
	}))

	// Output:
	// op = '+'
	// s1 + s2 = 3
	// --- SEPARATOR ---
	// s1 + s2 = 7
	// --- SEPARATOR ---
	// op = '*'
	// s1 * s2 = 12
	// --- SEPARATOR ---
	// s1 * s2 = 30
	// --- SEPARATOR ---
	// op = '+'
	// s1 + s2 = 15
}

// This example demonstrates how to use memos to memoize cheap computations.
// Memos are evaluated lazily. They take effect only when they are acquired.
func Example_memo() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2 := async.NewState(1), async.NewState(2)

	sum := async.NewMemo(&myExecutor, "aa", func(co *async.Coroutine, s *async.State[int]) {
		co.Watch(s1, s2)
		if v := s1.Get() + s2.Get(); v != s.Get() {
			s.Set(v) // Update s only when its value changes to stop unnecessary propagation.
		}
	})

	product := async.NewMemo(&myExecutor, "aa", func(co *async.Coroutine, s *async.State[int]) {
		co.Watch(s1, s2)
		if v := s1.Get() * s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			co.Spawn("sum", func(co *async.Coroutine) async.Result {
				fmt.Println("s1 + s2 =", sum.Get())
				return co.Await(sum)
			})
		case '*':
			co.Spawn("product", func(co *async.Coroutine) async.Result {
				fmt.Println("s1 * s2 =", product.Get())
				return co.Await(product)
			})
		}

		return co.Await()
	})

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(3)
		s2.Set(4)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		op.Set('*')
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(5)
		s2.Set(6)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(7)
		s2.Set(8)
		op.Set('+')
	}))

	// Output:
	// op = '+'
	// s1 + s2 = 3
	// --- SEPARATOR ---
	// s1 + s2 = 7
	// --- SEPARATOR ---
	// op = '*'
	// s1 * s2 = 12
	// --- SEPARATOR ---
	// s1 * s2 = 30
	// --- SEPARATOR ---
	// op = '+'
	// s1 + s2 = 15
}

// This example demonstrates how to set up an autorun function to run
// an executor in a goroutine automatically whenever a coroutine is spawned or
// resumed.
func Example_nonBlocking() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			myExecutor.Run()
		}()
	})

	s1, s2 := async.NewState(1), async.NewState(2)

	sum := async.NewMemo(&myExecutor, "aa", func(co *async.Coroutine, s *async.State[int]) {
		co.Watch(s1, s2)
		if v := s1.Get() + s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	product := async.NewMemo(&myExecutor, "aa", func(co *async.Coroutine, s *async.State[int]) {
		co.Watch(s1, s2)
		if v := s1.Get() * s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			co.Spawn("sum", func(co *async.Coroutine) async.Result {
				fmt.Println("s1 + s2 =", sum.Get())
				return co.Await(sum)
			})
		case '*':
			co.Spawn("product", func(co *async.Coroutine) async.Result {
				fmt.Println("s1 * s2 =", product.Get())
				return co.Await(product)
			})
		}

		return co.Await()
	})

	wg.Wait() // Wait for autorun to complete.
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(3)
		s2.Set(4)
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		op.Set('*')
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(5)
		s2.Set(6)
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn("/", async.Do(func() {
		s1.Set(7)
		s2.Set(8)
		op.Set('+')
	}))

	wg.Wait()

	// Output:
	// op = '+'
	// s1 + s2 = 3
	// --- SEPARATOR ---
	// s1 + s2 = 7
	// --- SEPARATOR ---
	// op = '*'
	// s1 * s2 = 12
	// --- SEPARATOR ---
	// s1 * s2 = 30
	// --- SEPARATOR ---
	// op = '+'
	// s1 + s2 = 15
}

// This example demonstrates how a task can conditionally depend on a state.
func Example_conditional() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2, s3 := async.NewState(1), async.NewState(2), async.NewState(7)

	myExecutor.Spawn("aa", func(co *async.Coroutine) async.Result {
		co.Watch(s1, s2) // Always depends on s1 and s2.

		v := s1.Get() + s2.Get()
		if v%2 == 0 {
			co.Watch(s3) // Conditionally depends on s3.
			v *= s3.Get()
		}

		fmt.Println(v)
		return co.Await()
	})

	inc := func(i int) int { return i + 1 }

	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.
	myExecutor.Spawn("/", async.Do(func() { s1.Update(inc) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() }))
	myExecutor.Spawn("/", async.Do(func() { s2.Update(inc) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.

	// Output:
	// 3
	// 28
	// 28
	// 5
}

// This example demonstrates how a memo can conditionally depend on a state.
func Example_conditionalMemo() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2, s3 := async.NewState(1), async.NewState(2), async.NewState(7)

	m := async.NewMemo(&myExecutor, "aa", func(co *async.Coroutine, s *async.State[int]) {
		co.Watch(s1, s2) // Always depends on s1 and s2.

		v := s1.Get() + s2.Get()
		if v%2 == 0 {
			co.Watch(s3) // Conditionally depends on s3.
			v *= s3.Get()
		}

		s.Set(v)
	})

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(m)
		fmt.Println(m.Get())
		return co.Await()
	})

	inc := func(i int) int { return i + 1 }

	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.
	myExecutor.Spawn("/", async.Do(func() { s1.Update(inc) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() }))
	myExecutor.Spawn("/", async.Do(func() { s2.Update(inc) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.

	// Output:
	// 3
	// 28
	// 28
	// 5
}

// This example demonstrates how to end a task.
// It creates a task that prints the value of a state whenever it changes.
// The task only prints 0, 1, 2 and 3 because it is ended after 3.
func Example_end() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return co.Await()
		}

		return co.End()
	})

	for i := 1; i <= 5; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 5.

	// Output:
	// 0
	// 1
	// 2
	// 3
	// 5
}

// This example demonstrates how to add a function call before a task re-runs,
// or after a task ends.
func Example_cleanup() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		co.Cleanup(func() { fmt.Println(v, myState.Get()) })

		if v < 3 {
			return co.Await()
		}

		return co.End()
	})

	for i := 1; i <= 5; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 5.

	// Output:
	// 0 1
	// 1 2
	// 2 3
	// 3 3
	// 5
}

// This example demonstrates how a coroutine can transit from one task to
// another.
func Example_switch() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return co.Await()
		}

		return co.Transit(func(co *async.Coroutine) async.Result {
			co.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(transited)")

			if v < 5 {
				return co.Await()
			}

			return co.End()
		})
	})

	for i := 1; i <= 7; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 7.

	// Output:
	// 0
	// 1
	// 2
	// 3
	// 3 (transited)
	// 4 (transited)
	// 5 (transited)
	// 7
}

// This example demonstrates how to run a task after another.
// To run multiple tasks in sequence, use [async.Block] instead.
func ExampleTask_Then() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	a := func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v, "(a)")

		if v < 3 {
			return co.Await()
		}

		return co.Transit(func(co *async.Coroutine) async.Result {
			co.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(transited)")

			if v < 5 {
				return co.Await()
			}

			return co.End()
		})
	}

	b := func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v, "(b)")

		if v < 7 {
			return co.Await()
		}

		return co.End()
	}

	myExecutor.Spawn("zz", async.Task(a).Then(b))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 0 (a)
	// 1 (a)
	// 2 (a)
	// 3 (a)
	// 3 (transited)
	// 4 (transited)
	// 5 (transited)
	// 5 (b)
	// 6 (b)
	// 7 (b)
	// 9
}

// This example computes two values in separate goroutines sequentially, then
// prints their sum.
func ExampleAwait() {
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
		async.Signal
		v1, v2 int
	}

	myExecutor.Spawn("/", func(co *async.Coroutine) async.Result {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(500 * time.Millisecond) // Heavy work #1 here.
			ans := 15
			myExecutor.Spawn("/", async.Do(func() {
				myState.v1 = ans
				myState.Notify()
			}))
		}()

		return co.Transit(async.Await(&myState).Then(
			func(co *async.Coroutine) async.Result {
				wg.Add(1)
				go func() {
					defer wg.Done()
					time.Sleep(500 * time.Millisecond) // Heavy work #2 here.
					ans := 27
					myExecutor.Spawn("/", async.Do(func() {
						myState.v2 = ans
						myState.Notify()
					}))
				}()

				return co.Transit(async.Await(&myState).Then(
					async.Do(func() {
						fmt.Println("v1 + v2 =", myState.v1+myState.v2)
					}),
				))
			},
		))
	})

	wg.Wait()

	// Output:
	// v1 + v2 = 42
}

// This example demonstrates how to run a block of tasks.
// A block can have zero or more tasks.
// A block runs tasks in sequence.
func ExampleBlock() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(co *async.Coroutine) async.Result {
		var t async.Task

		t = async.Block(
			async.Await(&myState),
			async.Do(func() {
				if v := myState.Get(); v%2 != 0 {
					fmt.Println(v)
				}
			}),
			func(co *async.Coroutine) async.Result {
				if v := myState.Get(); v >= 7 {
					return co.End()
				}
				return co.Transit(t) // Transit to t again to form a loop.
			},
		)

		return co.Transit(t)
	})

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// 9
}

func ExampleLoop() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", async.Loop(async.Block(
		async.Await(&myState),
		func(co *async.Coroutine) async.Result {
			if v := myState.Get(); v%2 == 0 {
				return co.Continue()
			}
			return co.End()
		},
		async.Do(func() {
			fmt.Println(myState.Get())
		}),
		func(co *async.Coroutine) async.Result {
			if v := myState.Get(); v >= 7 {
				return co.Break()
			}
			return co.End()
		},
	)))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// 9
}

func ExampleLoopN() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", async.LoopN(7, async.Block(
		async.Await(&myState),
		func(co *async.Coroutine) async.Result {
			if v := myState.Get(); v%2 == 0 {
				return co.Continue()
			}
			return co.End()
		},
		async.Do(func() {
			fmt.Println(myState.Get())
		}),
	)))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// 9
}

func ExampleFunc() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", async.Block(
		async.Defer( // Note that spawned tasks are considered surrounded by an invisible [Func].
			async.Do(func() { fmt.Println("defer 1") }),
		),
		async.Func(async.Block( // A block in a function scope.
			async.Defer(
				async.Do(func() { fmt.Println("defer 2") }),
			),
			async.Loop(async.Block(
				async.Await(&myState),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v%2 == 0 {
						return co.Continue()
					}
					return co.End()
				},
				async.Do(func() {
					fmt.Println(myState.Get())
				}),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v >= 7 {
						return co.Return() // Return here.
					}
					return co.End()
				},
			)),
			async.Do(func() { fmt.Println("after Loop") }), // Didn't run due to early return.
		)),
		async.Do(func() { fmt.Println("after Func") }),
	))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// defer 2
	// after Func
	// defer 1
	// 9
}

func ExampleFunc_exit() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", async.Block(
		async.Defer( // Note that spawned tasks are considered surrounded by an invisible [Func].
			async.Do(func() { fmt.Println("defer 1") }),
		),
		async.Func(async.Block( // A block in a function scope.
			async.Defer(
				async.Do(func() { fmt.Println("defer 2") }),
			),
			async.Loop(async.Block(
				async.Await(&myState),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v%2 == 0 {
						return co.Continue()
					}
					return co.End()
				},
				async.Do(func() {
					fmt.Println(myState.Get())
				}),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v >= 7 {
						return co.Exit() // Exit here.
					}
					return co.End()
				},
			)),
			async.Do(func() { fmt.Println("after Loop") }), // Didn't run due to early exit.
		)),
		async.Do(func() { fmt.Println("after Func") }), // Didn't run due to early exit.
	))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// defer 2
	// defer 1
	// 9
}

// This example demonstrates how async can handle panickings.
func ExampleFunc_panic() {
	dummyError := errors.New("dummy")

	var myExecutor async.Executor

	myExecutor.Autorun(func() {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			err, ok := v.(error)
			if ok && errors.Is(err, dummyError) && strings.Contains(err.Error(), "dummy") {
				// Note that use of strings.Contains(...) here is for coverage.
				fmt.Println("recovered dummy error")
				return
			}
			panic(v) // Repanic unexpected recovered value.
		}()
		myExecutor.Run()
	})

	var myState async.State[int]

	myExecutor.Spawn("zz", async.Block(
		async.Defer( // Note that spawned tasks are considered surrounded by an invisible [Func].
			async.Do(func() { fmt.Println("defer 1") }),
		),
		async.Func(async.Block( // A block in a function scope.
			async.Defer(
				async.Do(func() { fmt.Println("defer 2") }),
			),
			async.Loop(async.Block(
				async.Await(&myState),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v%2 == 0 {
						return co.Continue()
					}
					return co.End()
				},
				async.Do(func() {
					fmt.Println(myState.Get())
				}),
				func(co *async.Coroutine) async.Result {
					if v := myState.Get(); v >= 7 {
						panic(dummyError) // Panic here.
					}
					return co.End()
				},
			)),
			async.Do(func() { fmt.Println("after Loop") }), // Didn't run due to early panicking.
		)),
		async.Do(func() { fmt.Println("after Func") }), // Didn't run due to early panicking.
	))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// defer 2
	// defer 1
	// recovered dummy error
	// 9
}

// This example demonstrates how to make tail-calls in an [async.Func].
// Tail-calls are not recommended and should be avoided when possible.
// Without tail-call optimization, this example panics.
// To run this example, add an output comment at the end.
func ExampleFunc_tailcall() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	myExecutor.Spawn("#1", func(co *async.Coroutine) async.Result {
		var n int

		var t async.Task

		t = async.Func(async.Block(
			async.End(),
			async.End(),
			async.End(),
			func(co *async.Coroutine) async.Result { // Last task in the block.
				if n < 5000000 {
					n++
					return co.Transit(t) // Tail-call here.
				}
				return co.End()
			},
		))

		return co.Transit(t)
	})

	myExecutor.Spawn("#2", func(co *async.Coroutine) async.Result {
		var n int

		var t async.Task

		t = async.Func(async.Block(
			func(co *async.Coroutine) async.Result {
				if n < 5000000 {
					n++
					co.Defer(t)        // Tail-call here (workaround).
					return co.Return() // Early return.
				}
				return co.End()
			},
			async.End(),
			async.End(),
			async.End(),
		))

		return co.Transit(t)
	})

	// To run this example, add an output comment here.
}
