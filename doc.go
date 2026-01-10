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
// # Spawning Async Tasks vs. Passing Data Over Go Channels
//
// It's not recommended to have channel operations in an async [Task] for
// a [Coroutine] to do, since they tend to block.
// For an [Executor], if one coroutine blocks, no other coroutines can run.
// So instead of passing data around, one would just handle data at places
// where data are available.
// Async tasks are quite flexible and composable. One can even build a state
// machine across goroutine boundaries, which isn't something channels are
// qualified to do. Async tasks are better building blocks than channels.
//
// One of the advantages of passing data over channels is to be able to avoid
// allocation. Unfortunately, async tasks always escape to heap.
// Any variable they capture also escapes to heap. One should stay alert and
// take measures in hot spots, like repeatedly using a same task.
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
// When a [Task] completes, all child coroutines spawned in it are canceled.
//
// Conversely, root coroutines are not cancelable.
// One must cooperatively tell a root coroutine to exit.
// Though, it's possible to just let them rot in the background. The Go runtime
// would happily garbage collect them when there are no references to them.
//
// By default, canceled child coroutines cannot yield.
// All yield points are treated like exit points.
// However, within a [NonCancelable] context, a canceled child coroutine is
// allowed to yield, which would correspondingly cause its parent coroutine to
// yield, too. In such case, the parent coroutine stays suspended until all
// its child coroutines complete.
//
// # Panic Propagation
//
// Child coroutines propagate unrecovered panics to their parent coroutines.
// Root coroutines propagate unrecovered panics to their [Executor], causing
// the [Executor.Run] method to panic when it returns.
//
// If a coroutine spawns multiple child coroutines and one of them panics
// without recovering, the coroutine cancels other child coroutines.
// Then, after all child coroutines complete, the coroutine propagates panics
// to its parent coroutine, or its [Executor] if it's a root coroutine.
package async
