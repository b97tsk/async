package async

import "testing"

func TestPriorityQueue(t *testing.T) {
	t.Run("Overall", func(t *testing.T) {
		var pq priorityqueue

		for _, w := range []Weight{1, 2, 3, 4, 5, 6, 7, 8} {
			pq.Push(&Coroutine{weight: w})
		}

		for _, w := range []Weight{8, 7, 6, 5} {
			if co := pq.Pop(); co.weight != w {
				t.FailNow()
			}
		}

		for _, w := range []Weight{0, -1, -2} {
			pq.Push(&Coroutine{weight: w})
		}

		pq.Push(&Coroutine{weight: 5})

		if co := pq.Pop(); co.weight != 5 {
			t.FailNow()
		}

		pq.Push(&Coroutine{weight: 2})
		pq.Push(&Coroutine{weight: 3})

		for _, w := range []Weight{4, 3, 3, 2, 2, 1, 0, -1, -2} {
			if co := pq.Pop(); co.weight != w {
				t.FailNow()
			}
		}

		if !pq.Empty() {
			t.FailNow()
		}
	})
	t.Run("FIFO", func(t *testing.T) {
		var pq priorityqueue

		co1 := new(Coroutine)
		co2 := new(Coroutine)
		co3 := new(Coroutine)

		pq.Push(co1)
		pq.Push(co2)
		pq.Push(co3)

		if pq.Pop() != co1 || pq.Pop() != co2 || pq.Pop() != co3 {
			t.FailNow()
		}
	})
}
