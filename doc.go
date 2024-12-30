// Package async is a library for asynchronous programming.
//
// Since Go has already done a great job in bringing green/virtual threads
// into life, this library only implements a single-threaded [Executor] type,
// which some refer to as an async runtime.
// One can create as many Executors as they like.
//
// While Go excels at forking, async, on the other hand, excels at joining.
//
// # Use Case #1: Fan-in Executing Code From Various Goroutines
//
// Wanted to execute pieces of code from various goroutines
// in a single-threaded way?
//
// An [Executor] is designed to be able to run Tasks spawned in various
// goroutines sequentially.
// This comes in handy when one wants to do a series of operations
// on a single thread, for example, to read or update states that are not
// safe for concurrent access, to write data to the console, to update one's
// user interfaces, etc.
//
// No backpressure alert.
// [Task] spawning is designed not to block.
// If spawning outruns execution, an Executor could easily consume a lot of
// memory over time.
// To mitigate, one could introduce a semaphore per hot spot.
//
// # Use Case #2: Event-driven Reactiveness
//
// A [Task] can be reactive.
//
// A Task is spawned with a [Coroutine] to take care of it.
// In this user-provided function, one can return a specific [Result] to tell
// a Coroutine to watch and await some Events (e.g. [Signal], [State] and
// [Memo], etc.), and the Coroutine can just re-run the Task whenever any of
// these Events notifies.
//
// This is useful when one wants to do something repeatedly.
// It works like a loop. To exit this loop, just return a Result that ends
// the Coroutine from within the Task function. Simple.
//
// # Use Case #3: Easy State Machines
//
// A [Coroutine] can also switch from one [Task] to another, just like a state
// machine can transit from one state to another.
// This is done by returning another specific [Result] from within a Task
// function.
// A Coroutine can switch from one Task to another until a Task ends it.
//
// # Spawning Async Tasks vs. Passing Data over Go Channels
//
// It's not recommended to have channel operations in an async [Task] for
// a [Coroutine] to do, since they tend to block.
// For an [Executor], if one Coroutine blocks, no other Coroutines can run.
// So instead of passing data around, one would just handle data in place.
//
// One of the advantages of passing data over channels is to be able to reduce
// allocation. Unfortunately, async Tasks always escape to heap.
// Any variable they captured also escapes to heap.
// One should always stay alert and take measures in hot spot, like repeatedly
// using a same Task.
package async
