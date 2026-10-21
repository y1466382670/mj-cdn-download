package download

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type trace struct {
	mu    sync.Mutex
	start time.Time
	lines []string
}

func newTrace() *trace {
	return &trace{start: time.Now()}
}

func (t *trace) logf(format string, args ...any) {
	if t == nil {
		return
	}
	line := fmt.Sprintf("  +%5.2fs  %s", time.Since(t.start).Seconds(), fmt.Sprintf(format, args...))
	t.mu.Lock()
	t.lines = append(t.lines, line)
	t.mu.Unlock()
}

func (t *trace) dump(url string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	lines := append([]string(nil), t.lines...)
	t.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	log.Printf("[mj] 下载失败 %s\n%s", url, strings.Join(lines, "\n"))
}
