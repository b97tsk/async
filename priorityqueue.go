package async

import "sort"

type lesser[E any] interface {
	less(v E) bool
}

type priorityqueue[E lesser[E]] struct {
	head, tail []E
}

func (q *priorityqueue[E]) Empty() bool {
	return len(q.head) == 0
}

func (q *priorityqueue[E]) Push(v E) {
	headsize, tailsize := len(q.head), len(q.tail)

	n := headsize + tailsize

	i := sort.Search(n, func(i int) bool {
		if i < headsize {
			return v.less(q.head[i])
		}

		i -= headsize

		return v.less(q.tail[i])
	})

	if n == cap(q.tail) {
		var zero E

		s := append(q.tail[:n], zero)[:0]

		if i < headsize {
			s = append(s, q.head[:i]...)
			s = append(s, v)
			s = append(s, q.head[i:]...)
			s = append(s, q.tail...)
		} else {
			i -= headsize
			s = append(s, q.head...)
			s = append(s, q.tail[:i]...)
			s = append(s, v)
			s = append(s, q.tail[i:]...)
		}

		q.head, q.tail = s, s[:0]

		return
	}

	if headsize < cap(q.head) {
		s := q.head
		s = s[:headsize+1]
		copy(s[i+1:], s[i:])
		s[i] = v
		q.head = s
		return
	}

	if i < headsize {
		s := q.head
		u := s[headsize-1]
		copy(s[i+1:], s[i:])
		s[i] = v
		v = u
		i = headsize
	}

	i -= headsize

	s := q.tail
	s = s[:tailsize+1]
	copy(s[i+1:], s[i:])
	s[i] = v
	q.tail = s
}

func (q *priorityqueue[E]) Pop() (v E) {
	q.head[0], v = v, q.head[0]

	if len(q.head) > 1 {
		q.head = q.head[1:]
	} else {
		q.head, q.tail = q.tail, q.tail[:0]
	}

	return v
}
