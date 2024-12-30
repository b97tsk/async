package async

import "testing"

func TestPriorityQueue(t *testing.T) {
	t.Run("Overall", func(t *testing.T) {
		var pq priorityqueue[*Coroutine]

		for _, r := range "abcdefgh" {
			pq.Push(&Coroutine{path: string(r)})
		}

		for _, r := range "abcd" {
			if co := pq.Pop(); co.path != string(r) {
				t.FailNow()
			}
		}

		for _, r := range "ijk" {
			pq.Push(&Coroutine{path: string(r)})
		}

		pq.Push(&Coroutine{path: "d"})

		if co := pq.Pop(); co.path != "d" {
			t.FailNow()
		}

		pq.Push(&Coroutine{path: "g"})
		pq.Push(&Coroutine{path: "f"})

		for _, r := range "effgghijk" {
			if co := pq.Pop(); co.path != string(r) {
				t.FailNow()
			}
		}

		if !pq.Empty() {
			t.FailNow()
		}
	})
	t.Run("FIFO", func(t *testing.T) {
		var pq priorityqueue[*Coroutine]

		co1 := &Coroutine{path: "/"}
		co2 := &Coroutine{path: "/"}
		co3 := &Coroutine{path: "/"}

		pq.Push(co1)
		pq.Push(co2)
		pq.Push(co3)

		if pq.Pop() != co1 || pq.Pop() != co2 || pq.Pop() != co3 {
			t.FailNow()
		}
	})
}
