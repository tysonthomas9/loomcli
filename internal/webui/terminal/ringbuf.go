package terminal

import "sync"

// defaultRingCapacity is the per-session scrollback size for web terminals.
// 256 KB ≈ 2000 lines at 128 bytes/line on average. Sized so 40 concurrent
// sessions × 256 KB ≈ 10 MB of scrollback RAM at steady state.
const defaultRingCapacity = 256 * 1024

// ringBuffer is a fixed-capacity byte ring. Writes append; when the buffer is
// full the oldest bytes are dropped to make room. Safe for concurrent Write +
// Bytes calls.
type ringBuffer struct {
	mu  sync.Mutex
	cap int
	buf []byte
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = defaultRingCapacity
	}
	return &ringBuffer{cap: capacity, buf: make([]byte, 0, capacity)}
}

// Write appends p to the ring, dropping oldest bytes as needed.
func (r *ringBuffer) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// If p alone is larger than the capacity, keep only the last cap bytes.
	if len(p) >= r.cap {
		r.buf = r.buf[:r.cap]
		copy(r.buf, p[len(p)-r.cap:])
		return
	}

	room := r.cap - len(r.buf)
	if len(p) <= room {
		r.buf = append(r.buf, p...)
		return
	}

	// Shift out just enough older bytes to make room, then append.
	shift := len(p) - room
	copy(r.buf, r.buf[shift:])
	r.buf = r.buf[:len(r.buf)-shift]
	r.buf = append(r.buf, p...)
}

// Bytes returns a copy of the buffer contents in age order (oldest first).
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// Len returns the current number of bytes stored.
func (r *ringBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}
