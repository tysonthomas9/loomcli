package wrapper

import "sync"

type recentOutputBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func newRecentOutput(limit int) *recentOutputBuffer {
	return &recentOutputBuffer{limit: limit}
}

func (b *recentOutputBuffer) Write(p []byte) {
	if b == nil || b.limit <= 0 || len(p) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return
	}
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		copy(b.buf, b.buf[over:])
		b.buf = b.buf[:b.limit]
	}
}

func (b *recentOutputBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
