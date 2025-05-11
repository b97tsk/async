# async

萬物皆有靈，有靈以為生。

A Go library for asynchronous programming.

This is quite an unusual implementation that coroutines do not exchange data directly.
They just yield on awaiting events and resume on event notifications.
Communications between coroutines are done by sending event notifications to each other.
A coroutine can watch multiple events before yielding and an event notification can resume multiple coroutines.

Continue reading, or visit [Go Reference](https://pkg.go.dev/github.com/b97tsk/async) if you are not there.
