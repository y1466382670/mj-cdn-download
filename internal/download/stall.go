package download

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// stallGuard 在正文完全没有新字节时取消请求。
// 固定的总超时会把「代理慢但还在传」误判成失败。
type stallGuard struct {
	last atomic.Int64
	stop sync.Once
	done chan struct{}
}

func startStallGuard(cancel func(), idle time.Duration) *stallGuard {
	g := &stallGuard{done: make(chan struct{})}
	g.last.Store(time.Now().UnixNano())
	tick := idle / 4
	if tick < time.Second {
		tick = time.Second
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-g.done:
				return
			case now := <-t.C:
				if now.Sub(time.Unix(0, g.last.Load())) > idle {
					cancel()
					return
				}
			}
		}
	}()
	return g
}

func (g *stallGuard) watch(r io.Reader) io.Reader {
	return &guardedReader{guard: g, inner: r}
}

func (g *stallGuard) stopGuard() {
	g.stop.Do(func() { close(g.done) })
}

type guardedReader struct {
	guard *stallGuard
	inner io.Reader
}

func (r *guardedReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.guard.last.Store(time.Now().UnixNano())
	}
	return n, err
}
