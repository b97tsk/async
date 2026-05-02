package async_test

import (
	"testing"

	"github.com/b97tsk/async"
)

func TestSpawn(t *testing.T) {
	t.Run("AfterCanceled", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		myExecutor.Spawn(async.Select(
			async.Block(
				async.Defer(async.Spawn(func(co *async.Coroutine) async.Result {
					if !co.Canceled() {
						t.Error("Child coroutines should be canceled when parent is.")
					}
					return co.End()
				})),
				async.Await(),
			),
			async.End(),
		))
	})
	t.Run("AfterPanic", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		ok := false

		myExecutor.Spawn(async.Block(
			async.Defer(func(co *async.Coroutine) async.Result {
				co.RecoverFunc(func(v any) bool { return v == 42 })
				return co.End()
			}),
			async.Defer(async.Block(
				async.Spawn(async.End()),
				async.Do(func() { ok = true }),
			)),
			async.Panic(42),
		))

		if !ok {
			t.Fatal("Spawn(t) should not unwind when t doesn't panic.")
		}
	})
	t.Run("NoAwait", func(t *testing.T) {
		var myExecutor async.Executor

		myExecutor.Autorun(myExecutor.Run)

		myExecutor.Spawn(async.Block(
			async.Defer(func(co *async.Coroutine) async.Result {
				co.RecoverFunc(func(v any) bool { return v == 42 })
				return co.End()
			}),
			func(co *async.Coroutine) async.Result {
				co.Spawn(async.Block(
					async.Defer(async.Panic(42)),
					async.Await(),
				))
				return co.End() // No awaiting here.
			},
			async.Do(func() { t.Error("co.Spawn(t) did not unwind when t panics.") }),
		))
	})
}
