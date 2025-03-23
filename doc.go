// Package async is a library for asynchronous programming.
//
// Since Go has already done a great job in bringing green/virtual threads
// into life, this library only implements a single-threaded [Executor] type,
// which some refer to as an async runtime.
// One can create as many executors as they like.
//
// While Go excels at forking, async, on the other hand, excels at joining.
//
// # Use Case #1: Fan-in Executing Code From Various Goroutines
//
// Wanted to execute pieces of code from various goroutines
// in a single-threaded way?
//
// An [Executor] is designed to be able to run tasks spawned in various
// goroutines sequentially.
// This comes in handy when one wants to do a series of operations
// on a single thread, for example, to read or update states that are not
// safe for concurrent access, to write data to the console, to update one's
// user interfaces, etc.
//
// No backpressure alert.
// [Task] spawning is designed not to block.
// If spawning outruns execution, an executor could easily consume a lot of
// memory over time.
// To mitigate, one could introduce a semaphore per hot spot.
//
// # Use Case #2: Event-driven Reactiveness
//
// A [Task] can be reactive.
//
// A task is spawned with a [Coroutine] to take care of it.
// In this user-provided function, one can return a specific [Result] to tell
// a coroutine to watch and await some events (e.g. [Signal], [State] and
// [Memo], etc.), and the coroutine can just re-run the task whenever any of
// these events notifies.
//
// This is useful when one wants to do something repeatedly.
// It works like a loop. To exit this loop, just return a Result that ends
// the coroutine from within the task function. Simple.
//
// # Use Case #3: Easy State Machines
//
// A [Coroutine] can also transit from one [Task] to another, just like a state
// machine can transit from one state to another.
// This is done by returning another specific [Result] from within a task
// function.
// A coroutine can transit from one task to another until a task ends it.
//
// With the ability to transit, async is able to provide more advanced control
// structures, like [Block], [Loop] and [Func], to ease the process of writing
// async code. The experience now feels similar to that of writing sync code.
//
// # Spawning Async Tasks vs. Passing Data over Go Channels
//
// It's not recommended to have channel operations in an async [Task] for
// a [Coroutine] to do, since they tend to block.
// For an [Executor], if one coroutine blocks, no other coroutines can run.
// So instead of passing data around, one would just handle data in place.
//
// One of the advantages of passing data over channels is to be able to reduce
// allocation. Unfortunately, async tasks always escape to heap.
// Any variable they captured also escapes to heap.
// One should always stay alert and take measures in hot spot, like repeatedly
// using a same task.
package async
