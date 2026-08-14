package pty

import (
	"sync"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// defaultRingCapacity is the per-session replay size for web terminals.
// 256 KB is about 2000 lines at 128 bytes/line. Resize events carry a small
// accounting charge so a resize-only workload is bounded too.
const defaultRingCapacity = 256 * 1024

const replayResizeCost = 8

// replayBuffer is a bounded ordered PTY timeline. Output and resize events
// must remain interleaved: terminal control sequences emitted after SIGWINCH
// are meaningful only when replayed at the geometry that produced them.
type replayBuffer struct {
	mu       sync.Mutex
	capacity int
	size     int
	events   []interaction.TerminalReplayEvent
}

func newReplayBuffer(capacity int) *replayBuffer {
	if capacity <= 0 {
		capacity = defaultRingCapacity
	}
	return &replayBuffer{capacity: capacity}
}

func (buffer *replayBuffer) AppendOutput(output []byte) {
	if len(output) == 0 {
		return
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	copyOfOutput := append([]byte(nil), output...)
	if len(buffer.events) > 0 {
		last := &buffer.events[len(buffer.events)-1]
		if !last.IsResize() {
			last.Output = append(last.Output, copyOfOutput...)
			buffer.size += len(copyOfOutput)
			buffer.trimLocked()
			return
		}
	}
	buffer.events = append(buffer.events, interaction.TerminalReplayEvent{Output: copyOfOutput})
	buffer.size += len(copyOfOutput)
	buffer.trimLocked()
}

func (buffer *replayBuffer) AppendResize(columns, rows uint16) {
	if columns == 0 || rows == 0 {
		return
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	resize := interaction.TerminalReplayEvent{Columns: columns, Rows: rows}
	if len(buffer.events) > 0 && buffer.events[len(buffer.events)-1].IsResize() {
		buffer.events[len(buffer.events)-1] = resize
		return
	}
	buffer.events = append(buffer.events, resize)
	buffer.size += replayResizeCost
	buffer.trimLocked()
}

func (buffer *replayBuffer) Snapshot() []interaction.TerminalReplayEvent {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if len(buffer.events) == 0 {
		return nil
	}
	result := make([]interaction.TerminalReplayEvent, len(buffer.events))
	for index, event := range buffer.events {
		result[index] = event
		result[index].Output = append([]byte(nil), event.Output...)
	}
	return result
}

func (buffer *replayBuffer) Len() int {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.size
}

func (buffer *replayBuffer) trimLocked() {
	for buffer.size > buffer.capacity && len(buffer.events) > 0 {
		first := &buffer.events[0]
		if first.IsResize() {
			if len(buffer.events) == 1 {
				return
			}
			second := &buffer.events[1]
			if second.IsResize() {
				buffer.size -= replayResizeCost
				buffer.events = buffer.events[1:]
				continue
			}
			// Keep the geometry required to interpret the oldest retained
			// output. Evict output bytes after it until the timeline fits.
			overflow := buffer.size - buffer.capacity
			if overflow < len(second.Output) {
				second.Output = append([]byte(nil), second.Output[overflow:]...)
				buffer.size -= overflow
				return
			}
			buffer.size -= len(second.Output)
			buffer.events = append(buffer.events[:1], buffer.events[2:]...)
			continue
		}
		overflow := buffer.size - buffer.capacity
		if overflow < len(first.Output) {
			first.Output = append([]byte(nil), first.Output[overflow:]...)
			buffer.size -= overflow
			return
		}
		buffer.size -= len(first.Output)
		buffer.events = buffer.events[1:]
	}
}
