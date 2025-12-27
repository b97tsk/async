package async_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/b97tsk/async"
)

func Example() {
	// Create an executor.
	var myExecutor async.Executor

	// Set up an autorun function to run an executor automatically whenever a coroutine is spawned or resumed.
	// The best practice is to pass a function that does not block. See Example (NonBlocking).
	myExecutor.Autorun(myExecutor.Run)

	// Create some states.
	s1, s2 := async.NewState(1), async.NewState(2)
	op := async.NewState('+')

	// Although states can be created without the help of executors,
	// they might only be safe for use by one and only one executor due to the concern of data races.
	// Without proper synchronization, it's better only to spawn coroutines to read or update states.

	// Create a coroutine to print the sum or the product of s1 and s2, depending on what op is.
	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Watch(op) // Let co depend on op, so co can re-run whenever op changes.

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			// Using a child coroutine to narrow down what has to react whenever a state changes might be a good idea.
			// The following creates a child coroutine, it runs immediately and re-runs whenever s1 or s2 changes.
			co.Spawn(func(co *async.Coroutine) async.Result {
				fmt.Println("s1 + s2 =", s1.Get()+s2.Get())
				return co.Yield(s1, s2) // Yields and awaits s1 and s2.
			})
		case '*':
			co.Spawn(func(co *async.Coroutine) async.Result {
				fmt.Println("s1 * s2 =", s1.Get()*s2.Get())
				return co.Yield(s1, s2)
			})
		}

		return co.Yield() // Yields and awaits anything that has been watched (in this case, op).
	})

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		s1.Set(3)
		s2.Set(4)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		op.Set('*')
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		s1.Set(5)
		s2.Set(6)
	}))

	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
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

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	s1, s2 := async.NewState(1), async.NewState(2)
	op := async.NewState('+')

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			co.Spawn(func(co *async.Coroutine) async.Result {
				fmt.Println("s1 + s2 =", s1.Get()+s2.Get())
				return co.Yield(s1, s2)
			})
		case '*':
			co.Spawn(func(co *async.Coroutine) async.Result {
				fmt.Println("s1 * s2 =", s1.Get()*s2.Get())
				return co.Yield(s1, s2)
			})
		}

		return co.Yield()
	})

	wg.Wait() // Wait for autorun to complete.
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		s1.Set(3)
		s2.Set(4)
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		op.Set('*')
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
		s1.Set(5)
		s2.Set(6)
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Do(func() {
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

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Watch(s1, s2) // Always depends on s1 and s2.

		v := s1.Get() + s2.Get()
		if v%2 == 0 {
			co.Watch(s3) // Conditionally depends on s3.
			v *= s3.Get()
		}

		fmt.Println(v)
		return co.Yield()
	})

	inc := func(i int) int { return i + 1 }

	myExecutor.Spawn(async.Do(func() { s3.Notify() })) // Nothing happens.
	myExecutor.Spawn(async.Do(func() { s1.Update(inc) }))
	myExecutor.Spawn(async.Do(func() { s3.Notify() }))
	myExecutor.Spawn(async.Do(func() { s2.Update(inc) }))
	myExecutor.Spawn(async.Do(func() { s3.Notify() })) // Nothing happens.

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

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return co.Yield()
		}

		return co.End()
	})

	for i := 1; i <= 5; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 5.

	// Output:
	// 0
	// 1
	// 2
	// 3
	// 5
}

// This example demonstrates how a coroutine can transition from one task to
// another.
func Example_transition() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return co.Yield()
		}

		return co.Transition(func(co *async.Coroutine) async.Result {
			co.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(transitioned)")

			if v < 5 {
				return co.Yield()
			}

			return co.End()
		})
	})

	for i := 1; i <= 7; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 7.

	// Output:
	// 0
	// 1
	// 2
	// 3
	// 3 (transitioned)
	// 4 (transitioned)
	// 5 (transitioned)
	// 7
}

// This example demonstrates how to await a state until a condition is met.
func ExampleState_Await() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn(myState.Await(
		func(v int) bool { return v >= 3 },
	).Then(async.Do(func() {
		fmt.Println(myState.Get()) // Prints 3.
	})))

	for i := 1; i <= 5; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 5.

	// Output:
	// 3
	// 5
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
			return co.Yield()
		}

		return co.Transition(func(co *async.Coroutine) async.Result {
			co.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(transitioned)")

			if v < 5 {
				return co.Yield()
			}

			return co.End()
		})
	}

	b := func(co *async.Coroutine) async.Result {
		co.Watch(&myState)

		v := myState.Get()
		fmt.Println(v, "(b)")

		if v < 7 {
			return co.Yield()
		}

		return co.End()
	}

	myExecutor.Spawn(async.Task(a).Then(b))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 0 (a)
	// 1 (a)
	// 2 (a)
	// 3 (a)
	// 3 (transitioned)
	// 4 (transitioned)
	// 5 (transitioned)
	// 5 (b)
	// 6 (b)
	// 7 (b)
	// 9
}

// This example demonstrates how to run a block of tasks.
// A block can have zero or more tasks.
// A block runs tasks in sequence.
func ExampleBlock() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
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
				return co.Transition(t) // Transition to t again to form a loop.
			},
		)

		return co.Transition(t)
	})

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
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

	myExecutor.Spawn(async.Loop(async.Block(
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
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
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

	myExecutor.Spawn(async.LoopN(7, async.Block(
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
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
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

	myExecutor.Spawn(async.Block(
		async.Defer( // Note that spawned tasks are considered surrounded by an invisible async.Func.
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
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
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

	myExecutor.Spawn(async.Block(
		async.Defer( // Note that spawned tasks are considered surrounded by an invisible async.Func.
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
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
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

// This example demonstrates how to make tail-calls in an [async.Func].
// Tail-calls are not recommended and should be avoided when possible.
// Without tail-call optimization, this example shall panic.
func ExampleFunc_tailcall() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	// Case 1: Making tail-call in the last task of a block.
	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		var n int

		var t async.Task

		t = async.Func(async.Block(
			async.End(),
			async.End(),
			async.End(),
			func(co *async.Coroutine) async.Result { // Last task in the block.
				if n < 2000000 {
					n++
					return co.Transition(t) // Tail-call here.
				}
				return co.End()
			},
		))

		return co.Transition(t.Then(async.Do(func() { fmt.Println(n) })))
	})

	// Case 2: Making tail-call anywhere.
	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		var n int

		var t async.Task

		t = async.Func(async.Block(
			func(co *async.Coroutine) async.Result {
				if n < 2000000 {
					n++
					co.Defer(t)        // Tail-call here (using the only defer call as a workaround).
					return co.Return() // Early return.
				}
				return co.End()
			},
			async.End(),
			async.End(),
			async.End(),
		))

		return co.Transition(t.Then(async.Do(func() { fmt.Println(n) })))
	})

	// Output:
	// 2000000
	// 2000000
}

func ExampleFromSeq() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn(async.FromSeq(
		func(yield func(async.Task) bool) {
			await := async.Await(&myState)
			for yield(await) {
				v := myState.Get()
				if v%2 != 0 {
					fmt.Println(v)
				}
				if v >= 7 {
					return
				}
			}
		},
	))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn(async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 1
	// 3
	// 5
	// 7
	// 9
}

func ExampleNonCancelable() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var sig1, sig2 async.Signal

	{
		fmt.Println("without NonCancelable:")

		myExecutor.Spawn(async.Block(
			async.Select(
				async.Await(&sig1), // When sig1 notifies, cancel the following task.
				async.Block(
					async.Defer(async.Block(
						async.Await(&sig2), // Without NonCancelable, canceled coroutines cannot yield.
						async.Do(func() { fmt.Println("after Await") }),
					)),
					async.Await(), // Awaits for cancellation.
				),
			),
			async.Do(func() { fmt.Println("after Select") }),
		))

		myExecutor.Spawn(async.Do(sig1.Notify))
		myExecutor.Spawn(async.Do(sig2.Notify))
	}

	{
		fmt.Println("with NonCancelable:")

		myExecutor.Spawn(async.Block(
			async.Select(
				async.Await(&sig1), // When sig1 notifies, cancel the following task.
				async.Block(
					async.Defer(async.Block(
						// With NonCancelable, even canceled coroutines can yield, too.
						async.NonCancelable(async.Await(&sig2)),
						async.Do(func() { fmt.Println("after Await") }),
					)),
					async.Await(), // Awaits for cancellation.
				),
			),
			async.Do(func() { fmt.Println("after Select") }),
		))

		myExecutor.Spawn(async.Do(sig1.Notify))
		myExecutor.Spawn(async.Do(sig2.Notify))
	}

	{
		fmt.Println("additional tests:")

		for i := range 5 {
			myExecutor.Spawn(async.Block(
				async.Defer(async.Do(func() { fmt.Println(i) })),
				async.LoopN(1, func(co *async.Coroutine) async.Result {
					co.Spawn(async.NonCancelable(async.Await(&sig1)))
					switch i {
					case 0:
						return co.End()
					case 1:
						return co.Break()
					case 2:
						return co.Continue()
					case 3:
						return co.Return()
					default:
						return co.Exit()
					}
				}),
				async.Do(func() { fmt.Println("after LoopN") }),
			))
			myExecutor.Spawn(async.Do(sig1.Notify))
		}
	}

	// Output:
	// without NonCancelable:
	// after Select
	// with NonCancelable:
	// after Await
	// after Select
	// additional tests:
	// after LoopN
	// 0
	// after LoopN
	// 1
	// after LoopN
	// 2
	// 3
	// 4
}

func ExampleJoin() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	var s1, s2 async.State[int]

	myExecutor.Spawn(async.Block(
		async.Join(
			func(co *async.Coroutine) async.Result {
				wg.Go(func() {
					time.Sleep(500 * time.Millisecond) // Heavy work #1 here.
					ans := 15
					myExecutor.Spawn(async.Do(func() { s1.Set(ans) }))
				})
				return co.Await(&s1).End() // Awaits until &s1 notifies, then ends.
			},
			func(co *async.Coroutine) async.Result {
				wg.Go(func() {
					time.Sleep(1500 * time.Millisecond) // Heavy work #2 here.
					ans := 27
					myExecutor.Spawn(async.Do(func() { s2.Set(ans) }))
				})
				return co.Await(&s2).End() // Awaits until &s2 notifies, then ends.
			},
		),
		async.Do(func() { fmt.Println("s1 + s2 =", s1.Get()+s2.Get()) }),
	))

	wg.Wait()

	// Output:
	// s1 + s2 = 42
}

func ExampleSelect() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	var s1, s2 async.State[int]

	myExecutor.Spawn(async.Block(
		async.Select(
			func(co *async.Coroutine) async.Result {
				wg.Go(func() {
					time.Sleep(500 * time.Millisecond) // Heavy work #1 here.
					ans := 15
					myExecutor.Spawn(async.Do(func() { s1.Set(ans) }))
				})
				return co.Await(&s1).End() // Awaits until &s1 notifies, then ends.
			},
			func(co *async.Coroutine) async.Result {
				wg.Go(func() {
					time.Sleep(1500 * time.Millisecond) // Heavy work #2 here.
					ans := 27
					myExecutor.Spawn(async.Do(func() { s2.Set(ans) }))
				})
				return co.Await(&s2).End() // Awaits until &s2 notifies, then ends.
			},
		),
		async.Do(func() { fmt.Println("s1 + s2 =", s1.Get()+s2.Get()) }),
	))

	wg.Wait()

	// Output:
	// s1 + s2 = 15
}

// Without cancellation, ExampleSelect takes the same amount of time as
// ExampleJoin, which is unacceptable.
// The following example fixes that.
func ExampleSelect_withCancel() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	var s1, s2 async.State[int]

	myExecutor.Spawn(async.Block(
		async.Func(
			func(co *async.Coroutine) async.Result {
				ctx, cancel := context.WithCancel(context.Background())
				co.Defer(async.Do(cancel))
				return co.Transition(async.Select(
					func(co *async.Coroutine) async.Result {
						wg.Go(func() {
							select { // Heavy work #1 here.
							case <-time.After(500 * time.Millisecond):
							case <-ctx.Done():
								return // Cancel work when ctx gets canceled.
							}
							ans := 15
							myExecutor.Spawn(async.Do(func() { s1.Set(ans) }))
						})
						return co.Await(&s1).End() // Awaits until &s1 notifies, then ends.
					},
					func(co *async.Coroutine) async.Result {
						wg.Go(func() {
							select { // Heavy work #2 here.
							case <-time.After(1500 * time.Millisecond):
							case <-ctx.Done():
								return // Cancel work when ctx gets canceled.
							}
							ans := 27
							myExecutor.Spawn(async.Do(func() { s2.Set(ans) }))
						})
						return co.Await(&s2).End() // Awaits until &s2 notifies, then ends.
					},
				))
			},
		),
		async.Do(func() { fmt.Println("s1 + s2 =", s1.Get()+s2.Get()) }),
	))

	wg.Wait()

	// Output:
	// s1 + s2 = 15
}

func ExampleSpawn() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	// Exit (async.Exit or (*async.Coroutine).Exit) causes the coroutine that runs it to exit.
	// Tasks after Exit do not run.
	myExecutor.Spawn(async.Exit().Then(async.Do(func() { fmt.Println("after Exit") })))

	// With the help of async.Spawn, Exit only affects child coroutines.
	// The parent one continues to run tasks after async.Spawn.
	myExecutor.Spawn(async.Spawn(async.Exit()).Then(async.Do(func() { fmt.Println("after Spawn") })))

	// Output:
	// after Spawn
}

func ExampleMergeSeq() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	myExecutor.Autorun(func() { wg.Go(myExecutor.Run) })

	sleep := func(d time.Duration) async.Task {
		return func(co *async.Coroutine) async.Result {
			co.Escape()
			wg.Add(1) // Keep track of timers too.
			tm := time.AfterFunc(d, func() {
				defer wg.Done()
				myExecutor.Spawn(async.Do(func() {
					co.Unescape()
					co.Resume()
				}))
			})
			co.CleanupFunc(func() {
				if tm.Stop() {
					wg.Done()
					co.Unescape()
				}
			})
			return co.Await().End()
		}
	}

	myExecutor.Spawn(async.MergeSeq(3, func(yield func(async.Task) bool) {
		defer fmt.Println("done")
		for n := 1; n <= 6; n++ {
			d := time.Duration(n*100) * time.Millisecond
			f := func() { fmt.Println(n) }
			t := sleep(d).Then(async.Do(f))
			if !yield(t) {
				return
			}
		}
	}))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Select(
		sleep(1000*time.Millisecond), // Cancel the following task after a period of time.
		async.MergeSeq(3, func(yield func(async.Task) bool) {
			defer fmt.Println("done")
			for n := 1; ; n++ { // Infinite loop.
				d := time.Duration(n*100) * time.Millisecond
				f := func() { fmt.Println(n) }
				t := sleep(d).Then(async.Do(f))
				if !yield(t) {
					return
				}
			}
		}),
	))

	wg.Wait()

	// Output:
	// 1
	// 2
	// 3
	// 4
	// done
	// 5
	// 6
	// --- SEPARATOR ---
	// 1
	// 2
	// 3
	// 4
	// 5
	// 6
	// done
}

// This example demonstrates how async handles panics.
func Example_panicAndRecover() {
	var wg sync.WaitGroup // For keeping track of goroutines.

	var myExecutor async.Executor

	dummyError := errors.New("dummy")

	myExecutor.Autorun(func() {
		wg.Go(func() {
			defer func() {
				if v := recover(); v != nil {
					err, ok := v.(error)
					if ok && errors.Is(err, dummyError) && strings.Contains(err.Error(), "dummy") {
						fmt.Println("dummy error recovered!")
						return
					}
					panic(v) // Repanic unexpected values.
				}
			}()
			myExecutor.Run()
		})
	})

	sleep := func(d time.Duration) async.Task {
		return func(co *async.Coroutine) async.Result {
			co.Escape()
			wg.Add(1) // Keep track of timers too.
			tm := time.AfterFunc(d, func() {
				defer wg.Done()
				myExecutor.Spawn(async.Do(func() {
					co.Unescape()
					co.Resume()
				}))
			})
			co.CleanupFunc(func() {
				if tm.Stop() {
					wg.Done()
					co.Unescape()
				}
			})
			return co.Await().End()
		}
	}

	recover := func(co *async.Coroutine) async.Result {
		if v := co.Recover(); v != nil {
			fmt.Println(v)
		}
		return co.End()
	}

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		co.Defer(recover)
		panic("A")
	})

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(func(co *async.Coroutine) async.Result {
		// Cleanups are Task-scoped, while defers are Func-scoped.
		co.CleanupFunc(func() { panic("A") }) // Goes out of scope first.
		co.Defer(recover)
		return co.End()
	})

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Join(
		async.Block(
			async.Defer(recover),
			func(co *async.Coroutine) async.Result {
				co.Spawn(func(_ *async.Coroutine) async.Result {
					panic("A") // Child coroutines propagate panics.
				})
				panic("B") // Didn't run.
			},
		),
		async.Block(
			async.Defer(recover),
			func(co *async.Coroutine) async.Result {
				co.Spawn(async.Block(
					sleep(100*time.Millisecond),
					async.Do(func() { panic("A") }), // Panics after 100ms.
				))
				co.Spawn(async.Block(
					async.Defer(async.Do(func() { fmt.Println("canceled") })),
					async.Await(), // This child coroutine never ends, but it can be canceled.
				))
				return co.Await().End()
			},
		),
	))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Join(
		async.Block(
			async.Defer(recover), // Recovers the whole panic stack (but only given the latest one).
			async.Defer(func(_ *async.Coroutine) async.Result {
				panic("B") // Panics stack up.
			}),
			async.Do(func() { panic("A") }),
		),
		async.Block(
			async.Defer(recover), // Recovers "C", while "A" is discarded.
			async.Defer(async.Block(
				// async.Func introduces a new scope for panic recovering.
				async.Func(func(co *async.Coroutine) async.Result {
					co.Defer(recover) // Recovers "B", while "A" remains in the panic stack.
					panic("B")
				}),
				async.Do(func() { panic("C") }), // Stacks up onto "A".
			)),
			async.Do(func() { panic("A") }),
		),
	))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Block(
		async.Defer(recover),
		func(co *async.Coroutine) async.Result {
			return co.Await().Until(func() bool { panic("A") }).End()
		},
	))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Join(
		async.Block(
			async.Defer(recover),
			async.FromSeq(func(yield func(async.Task) bool) {
				panic("A")
			}),
		),
		async.Block(
			async.Defer(recover),
			async.FromSeq(func(yield func(async.Task) bool) {
				yield(async.Return())
				panic("A")
			}),
		),
	))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(async.Join(
		async.Block(
			async.Defer(recover),
			async.Break(), // Break without a loop.
		),
		async.Block(
			async.Defer(recover),
			async.Continue(), // Continue without a loop.
		),
		async.Block(
			async.Defer(recover),
			async.Throw("A"), // Throw is like panic but leaves no stack trace behind.
		),
	))

	wg.Wait()
	fmt.Println("--- SEPARATOR ---")

	myExecutor.Spawn(func(_ *async.Coroutine) async.Result {
		panic(dummyError) // Unrecovered panics get repanicked when (*async.Executor).Run returns.
	})

	wg.Wait()

	// Output:
	// A
	// --- SEPARATOR ---
	// A
	// --- SEPARATOR ---
	// A
	// canceled
	// A
	// --- SEPARATOR ---
	// B
	// B
	// C
	// --- SEPARATOR ---
	// A
	// --- SEPARATOR ---
	// A
	// A
	// --- SEPARATOR ---
	// async: unhandled break action
	// async: unhandled continue action
	// A
	// --- SEPARATOR ---
	// dummy error recovered!
}
