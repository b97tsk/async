package async_test

import (
	"fmt"
	"sync"
	"time"

	"async"
)

// This example demonstrates how to create async Tasks with different paths.
// The lower path, the higher priority.
// This example creates a Task with path "aa" for additional computations
// and another Task with path "zz" for printing results.
// The former runs before the latter because "aa" < "zz".
func Example() {
	// Create an Executor.
	var myExecutor async.Executor

	// Set up an autorun function to run an Executor automatically whenever a Task is spawned or resumed.
	// The best practice is to pass a function that does not block. See Example (NonBlocking).
	myExecutor.Autorun(myExecutor.Run)

	// Create two States.
	s1, s2 := async.NewState(1), async.NewState(2)

	// Although States can be created without the help of Executors,
	// they might only be safe for use by one and only one Executor because of data races.
	// Without proper synchronization, it's better only to spawn Tasks to read or update States.

	var sum, product async.State[int]

	myExecutor.Spawn("aa", func(t *async.Task) async.Result { // The path of t is "aa".
		t.Watch(s1, s2) // Let t depend on s1 and s2, so t can re-run whenever s1 or s2 changes.
		sum.Set(s1.Get() + s2.Get())
		product.Set(s1.Get() * s2.Get())
		return t.Await() // Awaits signals or state changes.
	})

	// The above Task re-runs whenever s1 or s2 changes. As an example, this is fine.
	// In practice, one should probably use Memos to avoid unnecessary recomputations. See Example (Memo).

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(t *async.Task) async.Result { // The path of t is "zz".
		t.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			// The path of an inner Task is relative to its outer one.
			t.Spawn("sum", func(t *async.Task) async.Result { // The path of inner t is "zz/sum".
				fmt.Println("s1 + s2 =", sum.Get())
				return t.Await(&sum)
			})
		case '*':
			t.Spawn("product", func(t *async.Task) async.Result { // The path of inner t is "zz/product".
				fmt.Println("s1 * s2 =", product.Get())
				return t.Await(&product)
			})
		}

		return t.Await()
	})

	fmt.Println("--- SEPARATOR ---")

	// The followings create several Tasks to mutate States.
	// They share the same path, "/", which is lower than "aa" and "zz".
	// Remember that, the lower path, the higher priority.
	// Updating States should have higher priority, so that when there are multiple update Tasks,
	// they can run together before any read Task.
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

// This example demonstrates how to use Memos to memoize cheap computations.
// Memos are evaluated lazily. They take effect only when they are acquired.
func Example_memo() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2 := async.NewState(1), async.NewState(2)

	sum := async.NewMemo(&myExecutor, "aa", func(t *async.Task, s *async.State[int]) {
		t.Watch(s1, s2)
		if v := s1.Get() + s2.Get(); v != s.Get() {
			s.Set(v) // Update s only when its value changes to stop unnecessary propagation.
		}
	})

	product := async.NewMemo(&myExecutor, "aa", func(t *async.Task, s *async.State[int]) {
		t.Watch(s1, s2)
		if v := s1.Get() * s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			t.Spawn("sum", func(t *async.Task) async.Result {
				fmt.Println("s1 + s2 =", sum.Get())
				return t.Await(sum)
			})
		case '*':
			t.Spawn("product", func(t *async.Task) async.Result {
				fmt.Println("s1 * s2 =", product.Get())
				return t.Await(product)
			})
		}

		return t.Await()
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
// an Executor in a goroutine automatically whenever a Task is spawned or
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

	sum := async.NewMemo(&myExecutor, "aa", func(t *async.Task, s *async.State[int]) {
		t.Watch(s1, s2)
		if v := s1.Get() + s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	product := async.NewMemo(&myExecutor, "aa", func(t *async.Task, s *async.State[int]) {
		t.Watch(s1, s2)
		if v := s1.Get() * s2.Get(); v != s.Get() {
			s.Set(v)
		}
	})

	op := async.NewState('+')

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(op)

		fmt.Println("op =", "'"+string(op.Get())+"'")

		switch op.Get() {
		case '+':
			t.Spawn("sum", func(t *async.Task) async.Result {
				fmt.Println("s1 + s2 =", sum.Get())
				return t.Await(sum)
			})
		case '*':
			t.Spawn("product", func(t *async.Task) async.Result {
				fmt.Println("s1 * s2 =", product.Get())
				return t.Await(product)
			})
		}

		return t.Await()
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

// This example demonstrates how a Task can conditionally depend on a State.
func Example_conditional() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2, s3 := async.NewState(1), async.NewState(2), async.NewState(7)

	myExecutor.Spawn("aa", func(t *async.Task) async.Result {
		t.Watch(s1, s2) // Always depends on s1 and s2.

		v := s1.Get() + s2.Get()
		if v%2 == 0 {
			t.Watch(s3) // Conditionally depends on s3.
			v *= s3.Get()
		}

		fmt.Println(v)
		return t.Await()
	})

	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.
	myExecutor.Spawn("/", async.Do(func() { s1.Set(s1.Get() + 1) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() }))
	myExecutor.Spawn("/", async.Do(func() { s2.Set(s2.Get() + 1) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.

	// Output:
	// 3
	// 28
	// 28
	// 5
}

// This example demonstrates how a Memo can conditionally depend on a State.
func Example_conditionalMemo() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	s1, s2, s3 := async.NewState(1), async.NewState(2), async.NewState(7)

	m := async.NewMemo(&myExecutor, "aa", func(t *async.Task, s *async.State[int]) {
		t.Watch(s1, s2) // Always depends on s1 and s2.

		v := s1.Get() + s2.Get()
		if v%2 == 0 {
			t.Watch(s3) // Conditionally depends on s3.
			v *= s3.Get()
		}

		s.Set(v)
	})

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(m)
		fmt.Println(m.Get())
		return t.Await()
	})

	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.
	myExecutor.Spawn("/", async.Do(func() { s1.Set(s1.Get() + 1) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() }))
	myExecutor.Spawn("/", async.Do(func() { s2.Set(s2.Get() + 1) }))
	myExecutor.Spawn("/", async.Do(func() { s3.Notify() })) // Nothing happens.

	// Output:
	// 3
	// 28
	// 28
	// 5
}

// This example demonstrates how to end a Task.
// It creates a Task that prints the value of a State whenever it changes.
// The Task only prints 0, 1, 2 and 3 because it is ended after 3.
func Example_end() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return t.Await()
		}

		return t.End()
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

// This example demonstrates how to add a function call before a Task
// re-runs, or after a Task ends.
func Example_defer() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(&myState)

		v := myState.Get()
		t.Defer(func() { fmt.Println(v, myState.Get()) })

		if v < 3 {
			return t.Await()
		}

		return t.End()
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

// This example demonstrates how a Task can switch from one Operation to
// another.
func Example_switch() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", func(t *async.Task) async.Result {
		t.Watch(&myState)

		v := myState.Get()
		fmt.Println(v)

		if v < 3 {
			return t.Await()
		}

		return t.Switch(func(t *async.Task) async.Result {
			t.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(switched)")

			if v < 5 {
				return t.Await()
			}

			return t.End()
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
	// 3 (switched)
	// 4 (switched)
	// 5 (switched)
	// 7
}

// This example demonstrates how to chain multiple Operations together to be
// worked on in sequence by a Task.
func Example_chain() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	myExecutor.Spawn("zz", async.Chain(
		func(t *async.Task) async.Result {
			t.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(first)")

			if v < 3 {
				return t.Await()
			}

			return t.Switch(func(t *async.Task) async.Result {
				t.Watch(&myState)

				v := myState.Get()
				fmt.Println(v, "(switched)")

				if v < 5 {
					return t.Await()
				}

				return t.End()
			})
		},
		func(t *async.Task) async.Result {
			t.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(second)")

			if v < 7 {
				return t.Await()
			}

			return t.End()
		},
	))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 0 (first)
	// 1 (first)
	// 2 (first)
	// 3 (first)
	// 3 (switched)
	// 4 (switched)
	// 5 (switched)
	// 5 (second)
	// 6 (second)
	// 7 (second)
	// 9
}

// This example demonstrates how to yield a Task only for it to resume
// later with another Operation.
// It computes two values in separate goroutines sequentially, then prints
// their sum.
// It showcases what yielding can do, not that it's a useful pattern.
func Example_yield() {
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

	myExecutor.Spawn("/", func(t *async.Task) async.Result {
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

		t.Watch(&myState)

		// Yield preserves Events that are being watched.
		return t.Yield(func(t *async.Task) async.Result {
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

			t.Watch(&myState)

			return t.Yield(async.Do(func() {
				fmt.Println("v1 + v2 =", myState.v1+myState.v2)
			}))
		})
	})

	wg.Wait()

	// Output:
	// v1 + v2 = 42
}

func ExampleOperation_Then() {
	var myExecutor async.Executor

	myExecutor.Autorun(myExecutor.Run)

	var myState async.State[int]

	a := func(t *async.Task) async.Result {
		t.Watch(&myState)

		v := myState.Get()
		fmt.Println(v, "(a)")

		if v < 3 {
			return t.Await()
		}

		return t.Switch(func(t *async.Task) async.Result {
			t.Watch(&myState)

			v := myState.Get()
			fmt.Println(v, "(switched)")

			if v < 5 {
				return t.Await()
			}

			return t.End()
		})
	}

	b := func(t *async.Task) async.Result {
		t.Watch(&myState)

		v := myState.Get()
		fmt.Println(v, "(b)")

		if v < 7 {
			return t.Await()
		}

		return t.End()
	}

	myExecutor.Spawn("zz", async.Operation(a).Then(b))

	for i := 1; i <= 9; i++ {
		myExecutor.Spawn("/", async.Do(func() { myState.Set(i) }))
	}

	fmt.Println(myState.Get()) // Prints 9.

	// Output:
	// 0 (a)
	// 1 (a)
	// 2 (a)
	// 3 (a)
	// 3 (switched)
	// 4 (switched)
	// 5 (switched)
	// 5 (b)
	// 6 (b)
	// 7 (b)
	// 9
}