package async

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type paniccatcher struct {
	items []panicitem
}

func (pc *paniccatcher) Reset() {
	pc.items = nil
}

func (pc *paniccatcher) Rethrow() {
	if len(pc.items) != 0 {
		panic(&panicvalue{items: pc.items})
	}
}

func (pc *paniccatcher) TryCatch(f func()) (ok bool) {
	defer func() {
		if !ok {
			v := recover()
			if v == nil {
				panic("async: async does not support runtime.Goexit()")
			}
			pc.items = append(pc.items, panicitem{v, debug.Stack()})
		}
	}()
	f()
	return true
}

type panicvalue struct {
	items []panicitem
	errs  atomic.Pointer[[]error]
}

type panicitem struct {
	value any
	stack []byte
}

func (pv *panicvalue) Error() string {
	var b strings.Builder
	b.WriteString("as follows:")
	for i, p := range pv.items {
		fmt.Fprintf(&b, "\n(%d/%d) panic: %v\n\n", i+1, len(pv.items), p.value)
		b.Write(p.stack)
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
