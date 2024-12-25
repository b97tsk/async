package async

import "testing"

func TestPriorityQueue(t *testing.T) {
	t.Run("Overall", func(t *testing.T) {
		var pq priorityqueue[*Task]

		for _, r := range "abcdefgh" {
			pq.Push(&Task{path: string(r)})
		}

		for _, r := range "abcd" {
			if u := pq.Pop(); u.path != string(r) {
				t.FailNow()
			}
		}

		for _, r := range "ijk" {
			pq.Push(&Task{path: string(r)})
		}

		pq.Push(&Task{path: "d"})

		if u := pq.Pop(); u.path != "d" {
			t.FailNow()
		}

		pq.Push(&Task{path: "g"})
		pq.Push(&Task{path: "f"})

		for _, r := range "effgghijk" {
			if u := pq.Pop(); u.path != string(r) {
				t.FailNow()
			}
		}

		if !pq.Empty() {
			t.FailNow()
		}
	})
	t.Run("FIFO", func(t *testing.T) {
		var pq priorityqueue[*Task]

		u := &Task{path: "/"}
		v := &Task{path: "/"}
		w := &Task{path: "/"}

		pq.Push(u)
		pq.Push(v)
		pq.Push(w)

		if pq.Pop() != u || pq.Pop() != v || pq.Pop() != w {
			t.FailNow()
		}
	})
}
