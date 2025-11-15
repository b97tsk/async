package async

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type panicstack []panicitem

func (ps panicstack) Repanic() {
	if len(ps) != 0 {
		panic(&panicvalue{items: ps})
	}
}

type dummy struct{}

func (ps *panicstack) Try(f func()) (ok bool) {
	defer func() {
		if !ok {
			v := recover()
			if v == nil {
				panic("async: async does not support runtime.Goexit()")
			}
			if _, ok := v.(dummy); ok {
				return // Ignore dummy values.
			}
			ps.push(v, debug.Stack())
		}
	}()
	f()
	return true
}

func (ps *panicstack) push(v any, stack []byte) {
	s := *ps
	n := len(s)
	repanicked := n != 0 && equal(v, s[n-1].value)
	s = append(s, panicitem{v, stack, repanicked, false})
	*ps = s
}

func equal(a, b any) bool {
	defer func() { _ = recover() }()
	return a == b
}

type panicitem struct {
	value      any
	stack      []byte
	repanicked bool
	recovered  bool
}

type panicvalue struct {
	items []panicitem
	errs  atomic.Pointer[[]error]
}

func (pv *panicvalue) Error() string {
	var b strings.Builder
	b.WriteString("as follows:")
	for i, p := range pv.items {
		fmt.Fprintf(&b, "\n(%d/%d) panic: %v", i+1, len(pv.items), p.value)
		switch {
		case p.repanicked && p.recovered:
			b.WriteString(" (repanicked, recovered)")
		case p.repanicked:
			b.WriteString(" (repanicked)")
		case p.recovered:
			b.WriteString(" (recovered)")
		}
		if p.stack != nil {
			b.WriteString("\n\n")
			b.Write(p.stack)
		}
	}
	return b.String()
}

func (pv *panicvalue) Unwrap() []error {
	if p := pv.errs.Load(); p != nil {
		return *p
	}
	var errs []error
	for _, p := range pv.items {
		if err, ok := p.value.(error); ok {
			errs = append(errs, err)
		}
	}
	pv.errs.Store(&errs)
	return errs
}
