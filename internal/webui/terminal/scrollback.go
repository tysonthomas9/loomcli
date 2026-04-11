package terminal

import (
	"strings"
	"sync"
)

// defaultScrollbackMaxLines is the default maximum number of lines retained
// in a per-session scrollback ring buffer.
const defaultScrollbackMaxLines = 10000

// ScrollbackBuffer is a thread-safe ring buffer that captures terminal output
// on a per-line basis. When the buffer reaches its maximum capacity, the oldest
// lines are discarded and a truncation counter is incremented.
type ScrollbackBuffer struct {
	mu             sync.Mutex
	lines          []string
	head           int // index of oldest line in the ring
	count          int // number of valid lines in the ring
	maxLines       int
	truncatedCount int64  // total lines discarded
	partial        string // incomplete line (no trailing newline yet)
}

// NewScrollbackBuffer creates a ring buffer that retains at most maxLines lines.
func NewScrollbackBuffer(maxLines int) *ScrollbackBuffer {
	if maxLines <= 0 {
		maxLines = defaultScrollbackMaxLines
	}
	return &ScrollbackBuffer{
		lines:    make([]string, maxLines),
		maxLines: maxLines,
	}
}

// Append ingests raw bytes from the PTY relay, splitting on newlines and
// buffering any trailing partial line until the next newline arrives.
func (b *ScrollbackBuffer) Append(data []byte) {
	if len(data) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	s := b.partial + string(data)
	for {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			// No more newlines — remainder is a partial line.
			b.partial = s
			return
		}
		line := s[:idx]
		s = s[idx+1:]
		b.appendLine(line)
	}
}

// appendLine adds a single complete line to the ring buffer (caller holds mu).
func (b *ScrollbackBuffer) appendLine(line string) {
	if b.count < b.maxLines {
		idx := (b.head + b.count) % b.maxLines
		b.lines[idx] = line
		b.count++
	} else {
		// Overwrite the oldest entry.
		b.lines[b.head] = line
		b.head = (b.head + 1) % b.maxLines
		b.truncatedCount++
	}
}

// Lines returns a copy of the buffered lines in chronological order.
func (b *ScrollbackBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]string, b.count)
	for i := 0; i < b.count; i++ {
		out[i] = b.lines[(b.head+i)%b.maxLines]
	}
	return out
}

// LineCount returns the number of lines currently in the buffer.
func (b *ScrollbackBuffer) LineCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// TruncatedCount returns the total number of lines that were discarded
// because the buffer exceeded its maximum capacity.
func (b *ScrollbackBuffer) TruncatedCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncatedCount
}

// Clear resets the buffer, discarding all stored lines.
func (b *ScrollbackBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
	b.truncatedCount = 0
	b.partial = ""
	// Zero the backing array so strings can be GC'd.
	for i := range b.lines {
		b.lines[i] = ""
	}
}

// SetScrollbackMaxLines overrides the default scrollback buffer capacity.
// Only affects buffers created after this call.
func (m *TerminalManager) SetScrollbackMaxLines(n int) {
	if n <= 0 {
		n = defaultScrollbackMaxLines
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrollbackMaxLines = n
}

// ScrollbackMaxLines returns the configured scrollback buffer capacity.
func (m *TerminalManager) ScrollbackMaxLines() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scrollbackMaxLines
}

// GetScrollbackBuffer returns the scrollback buffer for the given user-facing
// session name in the given workspace, creating one if it does not already
// exist. Returns nil if wsID is empty.
func (m *TerminalManager) GetScrollbackBuffer(wsID, name string) *ScrollbackBuffer {
	if wsID == "" {
		return nil
	}
	internalName := m.tmuxName(wsID, name)
	m.mu.Lock()
	defer m.mu.Unlock()
	buf, ok := m.scrollbackBuffers[internalName]
	if !ok {
		buf = NewScrollbackBuffer(m.scrollbackMaxLines)
		m.scrollbackBuffers[internalName] = buf
	}
	return buf
}

// LookupScrollbackBuffer returns the scrollback buffer for the given session
// name in the given workspace if one exists, or nil if no buffer has been
// created for this session.
func (m *TerminalManager) LookupScrollbackBuffer(wsID, name string) *ScrollbackBuffer {
	if wsID == "" {
		return nil
	}
	internalName := m.tmuxName(wsID, name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scrollbackBuffers[internalName]
}
