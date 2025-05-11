package async

import "testing"

func TestPriorityQueue(t *testing.T) {
	t.Run("Overall", func(t *testing.T) {
		var pq priorityqueue[*Coroutine]

		for _, lv := range []uint32{1, 2, 3, 4, 5, 6, 7, 8} {
			pq.Push(&Coroutine{level: lv})
		}

		for _, lv := range []uint32{1, 2, 3, 4} {
			if co := pq.Pop(); co.level != lv {
				t.FailNow()
			}
		}

		for _, lv := range []uint32{9, 10, 11} {
			pq.Push(&Coroutine{level: lv})
		}

		pq.Push(&Coroutine{level: 4})

		if co := pq.Pop(); co.level != 4 {
			t.FailNow()
		}

		pq.Push(&Coroutine{level: 7})
		pq.Push(&Coroutine{level: 6})

		for _, lv := range []uint32{5, 6, 6, 7, 7, 8, 9, 10, 11} {
			if co := pq.Pop(); co.level != lv {
				t.FailNow()
			}
		}

		if !pq.Empty() {
			t.FailNow()
		}
	})
	t.Run("FIFO", func(t *testing.T) {
		var pq priorityqueue[*Coroutine]

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
