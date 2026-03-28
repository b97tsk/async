package async

import "sort"

type priorityqueue struct {
	head, tail []*Coroutine
}

func (q *priorityqueue) Empty() bool {
	return len(q.head) == 0
}

func (q *priorityqueue) Push(co *Coroutine) {
	headsize, tailsize := len(q.head), len(q.tail)
	n := headsize + tailsize
	i := sort.Search(n, func(i int) bool {
		s := q.head
		if i >= headsize {
			s = q.tail
			i -= headsize
		}
		return co.less(s[i])
	})

	if n == cap(q.tail) {
		s := append(q.tail[:n], nil)[:0]
		if i < headsize {
			s = append(s, q.head[:i]...)
			s = append(s, co)
			s = append(s, q.head[i:]...)
			s = append(s, q.tail...)
		} else {
			i -= headsize
			s = append(s, q.head...)
			s = append(s, q.tail[:i]...)
			s = append(s, co)
			s = append(s, q.tail[i:]...)
		}
		q.head, q.tail = s, s[:0]
		return
	}

	if headsize < cap(q.head) {
		s := q.head
		s = s[:headsize+1]
		copy(s[i+1:], s[i:])
		s[i] = co
		q.head = s
		return
	}

	if i < headsize {
		s := q.head
		v := s[headsize-1]
		copy(s[i+1:], s[i:])
		s[i] = co
		co = v
		i = headsize
	}

	i -= headsize

	s := q.tail
	s = s[:tailsize+1]
	copy(s[i+1:], s[i:])
	s[i] = co
	q.tail = s
}

func (q *priorityqueue) Pop() (co *Coroutine) {
	q.head[0], co = co, q.head[0]
	if len(q.head) > 1 {
		q.head = q.head[1:]
	} else {
		q.head, q.tail = q.tail, q.tail[:0]
	}
	return co
}
