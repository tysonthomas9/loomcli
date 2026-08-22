package terminal

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/creack/pty"
)

const (
	writerQueueBytes = 256 * 1024
	resizeItemBytes  = 4
)

type terminalPTY interface {
	io.Reader
	io.Writer
	io.Closer
	SetSize(cols, rows uint16) error
}

type fileTerminalPTY struct {
	file    *os.File
	ioctlMu sync.Mutex
}

func (p *fileTerminalPTY) Read(data []byte) (int, error)  { return p.file.Read(data) }
func (p *fileTerminalPTY) Write(data []byte) (int, error) { return p.file.Write(data) }

// Fd (used by pty.Setsize) is not safe against a concurrent File.Close.
// Serialize those two operations without holding a lock across blocking PTY
// reads or writes; os.File.Close remains able to interrupt either syscall.
func (p *fileTerminalPTY) Close() error {
	p.ioctlMu.Lock()
	defer p.ioctlMu.Unlock()
	return p.file.Close()
}

func (p *fileTerminalPTY) SetSize(cols, rows uint16) error {
	p.ioctlMu.Lock()
	defer p.ioctlMu.Unlock()
	return pty.Setsize(p.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func makeGeneration() (Generation, error) {
	var generation Generation
	if _, err := rand.Read(generation[:]); err != nil {
		return Generation{}, fmt.Errorf("terminal generation: %w", err)
	}
	return generation, nil
}

type writerItemKind uint8

const (
	writerInput writerItemKind = iota + 1
	writerResize
)

type writerItem struct {
	kind       writerItemKind
	data       []byte
	cols, rows uint16
	cost       int
}

// writerFIFO is owner-fed and writer-drained. Inputs reserve one resize item
// of capacity so a geometry operation ordered after a full input queue can
// always enter the FIFO; adjacent resizes coalesce without crossing input.
type writerFIFO struct {
	pty terminalPTY

	mu          sync.Mutex
	queue       []writerItem
	queuedBytes int
	wake        chan struct{}
	closedCh    chan struct{}
	closeOnce   sync.Once
	closed      bool
}

func newWriterFIFO(device terminalPTY) *writerFIFO {
	return &writerFIFO{
		pty:      device,
		wake:     make(chan struct{}, 1),
		closedCh: make(chan struct{}),
	}
}

func (q *writerFIFO) enqueueInput(data []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.queuedBytes+len(data) > writerQueueBytes-resizeItemBytes {
		return false
	}
	q.queue = append(q.queue, writerItem{kind: writerInput, data: data, cost: len(data)})
	q.queuedBytes += len(data)
	q.signal()
	return true
}

func (q *writerFIFO) enqueueResize(cols, rows uint16) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if n := len(q.queue); n > 0 && q.queue[n-1].kind == writerResize {
		q.queue[n-1].cols = cols
		q.queue[n-1].rows = rows
		return
	}
	if q.queuedBytes+resizeItemBytes > writerQueueBytes {
		return // enqueueInput's reserved capacity makes this unreachable.
	}
	q.queue = append(q.queue, writerItem{
		kind: writerResize,
		cols: cols,
		rows: rows,
		cost: resizeItemBytes,
	})
	q.queuedBytes += resizeItemBytes
	q.signal()
}

func (q *writerFIFO) run() {
	for {
		item, ok := q.next()
		if !ok {
			return
		}
		switch item.kind {
		case writerInput:
			q.writeAll(item.data)
		case writerResize:
			_ = q.pty.SetSize(item.cols, item.rows)
		}
	}
}

func (q *writerFIFO) next() (writerItem, bool) {
	for {
		q.mu.Lock()
		if len(q.queue) > 0 {
			item := q.queue[0]
			q.queue[0] = writerItem{}
			q.queue = q.queue[1:]
			q.queuedBytes -= item.cost
			q.mu.Unlock()
			return item, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return writerItem{}, false
		}
		select {
		case <-q.wake:
		case <-q.closedCh:
			return writerItem{}, false
		}
	}
}

func (q *writerFIFO) writeAll(data []byte) {
	for len(data) > 0 {
		n, err := q.pty.Write(data)
		if err != nil || n == 0 {
			return
		}
		data = data[n:]
	}
}

func (q *writerFIFO) close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.queue = nil
		q.queuedBytes = 0
		q.mu.Unlock()
		close(q.closedCh)
	})
}

func (q *writerFIFO) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
