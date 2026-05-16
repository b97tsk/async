// Package async is a library for asynchronous programming.
//
// Since Go has already done a great job in bringing green/virtual threads
// into life, this library only implements a single-threaded [Executor] type,
// which some refer to as an async runtime.
// One can create as many executors as they like.
//
// While Go excels at forking, async, on the other hand, excels at joining.
//
// # Use Case #1: Fan-In Executing Code From Goroutines
//
// Wanted to execute pieces of code from goroutines in a single-threaded way?
//
// An [Executor] is designed to be able to run tasks spawned in goroutines
// sequentially.
// This comes in handy when one wants to do a series of operations
// on a single thread, for example, to read or update states that are not
// safe for concurrent access, to write data to the console or a file, to
// update one's user interfaces, etc.
//
// Be aware that there is no back pressure.
// [Task] spawning isn't designed to block.
// If spawning outruns execution, an [Executor] can easily consume a lot of
// memory over time.
// To mitigate, one could introduce a semaphore per hot spot.
//
// # Use Case #2: Event-Driven Reactiveness
//
// An async [Task] can be reactive.
//
// An async [Task] is spawned with a [Coroutine] to take care of it.
// In this user-provided function, one can return a specific [Result] to tell
// a coroutine to watch and await some events (e.g. [Signal], [State], etc.),
// and the coroutine can just re-run the task whenever any of these events
// notifies.
//
// This is useful when one wants to do something repeatedly.
// It works like a loop. To exit this loop, just return a Result that does
// something different from within the task function. Simple.
//
// # Use Case #3: Easy State Machines Across Goroutine Boundaries
//
// A [Coroutine] can also make a transition from one [Task] to another, just
// like a state machine can make a transition from one state to another.
// This is done by returning another specific [Result] from within a task
// function.
// A coroutine can transition from one task to another until a task ends it.
//
// With the ability to transition, async is able to provide more advanced
// control structures, like [Block], [Loop] and [Func], to ease the process of
// writing async code. The experience now feels similar to that of writing sync
// code.
//
// # The Essentiality of Structured Concurrency
//
// Async encourages non-blocking programming, which makes structured concurrency
// quite essential to this library.
// At some point, one might want to know when an [Executor] stops operating.
//
// In Go, all Go code can only be executed by goroutines.
// Async tasks are just ordinary Go functions, too.
// If one keeps track of every goroutine that spawns async tasks, and waits for
// them to finish, then the exact right time when an [Executor] stops operating
// can be determined too.
// Essentially, it creates a synchronization point between the spawned tasks
// and the rest of the code after the waiting.
//
// # Root/Child Coroutines
//
// Coroutines spawned by an [Executor] are root coroutines.
// Coroutines spawned by the [Coroutine.Spawn] method, in a [Task] function,
// are child coroutines.
//
// Child coroutines are Task-scoped and, therefore, cancelable.
// When a coroutine resumes, or finishes a [Task], all spawned child coroutines
// are canceled.
//
// Conversely, root coroutines are not cancelable.
// One must cooperatively tell a root coroutine to exit.
// Though, it's possible to just let them rot in the background. The Go runtime
// would happily garbage collect them when there are no references to them.
//
// When a child coroutine is canceled, it runs to completion with all yield
// points treated like exit points.
// However, within a [NonCancelable] context, a canceled child coroutine is
// allowed to yield, which would correspondingly cause its parent coroutine to
// yield, too. In such case, the parent coroutine stays suspended until all its
// child coroutines are completed.
//
// Further more, a (child) coroutine can also perform hard yields at any time.
// A hard yield is non-cancelable and does not require a [NonCancelable]
// context.
//
// # Panic Propagation
//
// Child coroutines propagate unrecovered panics to their parent coroutines.
// Root coroutines propagate unrecovered panics to their [Executor], causing
// the [Executor.Run] method to panic when it returns.
//
// If a coroutine spawns multiple child coroutines and one of them panics
// without recovering, the coroutine cancels other child coroutines.
// Then, after all child coroutines are completed, the coroutine propagates
// panics to its parent coroutine, or its [Executor] if it's a root coroutine.
//
// # Coroutine Unwinding
//
// Coroutine unwinding is the process of removing structure controllers,
// such as [Func], from the controller stack.
// Deferred tasks are run in last-in-first-out (LIFO) order, hence, an unwinding
// coroutine may still have a chance to yield.
//
// Here is the list of operations that may cause a coroutine to unwind:
//   - Returning: unstoppable, suspendable, [Func]-scoped;
//   - Exiting: unstoppable, suspendable;
//   - Panicking: stoppable with panic recovery, suspendable;
//   - Canceled by parent (implies Exiting): unstoppable, suspendable with
//     hard yields or [NonCancelable].
package async
