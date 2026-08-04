package terminal

import (
	"regexp"
	"strings"
	"sync"
)

// defaultRingCapacity is the per-session scrollback size for web terminals.
// 256 KB ≈ 2000 lines at 128 bytes/line on average. Sized so 40 concurrent
// sessions × 256 KB ≈ 10 MB of scrollback RAM at steady state.
const defaultRingCapacity = 256 * 1024

// PTY output is stateful. Once the ring evicts the DECSET/DECRST bytes that
// selected an alternate buffer or mouse protocol, a new browser emulator
// cannot reconstruct a long-lived fullscreen TUI from the retained suffix.
// Keep a checkpoint of persistent private modes at the ring's head and replay
// the retained tail from that exact state.
const privateModeScanTailCapacity = 64

var privateModePattern = regexp.MustCompile(`\x1b\[\?([0-9;]+)([hl])`)

var alternateBufferModes = []string{"47", "1047", "1049"}
var mouseEventModes = []string{"9", "1000", "1002", "1003"}
var mouseEncodingModes = []string{"1005", "1015", "1016", "1006"}

type privateModeEvent struct {
	start   int64
	end     int64
	modes   []string
	enabled bool
}

// ringBuffer is a fixed-capacity byte ring. Writes append; when the buffer is
// full the oldest bytes are dropped to make room. Safe for concurrent Write +
// snapshot calls.
type ringBuffer struct {
	mu  sync.Mutex
	cap int
	buf []byte

	streamEnd  int64
	headOffset int64
	scanTail   []byte

	headModes  map[string]bool
	modeEvents []privateModeEvent
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = defaultRingCapacity
	}
	return &ringBuffer{
		cap:        capacity,
		buf:        make([]byte, 0, capacity),
		scanTail:   make([]byte, 0, privateModeScanTailCapacity),
		headModes:  make(map[string]bool),
		modeEvents: make([]privateModeEvent, 0, 16),
	}
}

// Append writes p to the ring, dropping oldest bytes as needed. Satisfies
// realtime.ScrollbackAppender so the drain path can stay interface-shaped.
func (r *ringBuffer) Append(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	chunkStart := r.streamEnd
	r.trackPrivateModesLocked(chunkStart, p)
	r.streamEnd += int64(len(p))

	if len(p) >= r.cap {
		r.buf = r.buf[:r.cap]
		copy(r.buf, p[len(p)-r.cap:])
	} else {
		room := r.cap - len(r.buf)
		if len(p) <= room {
			r.buf = append(r.buf, p...)
		} else {
			shift := len(p) - room
			copy(r.buf, r.buf[shift:])
			r.buf = r.buf[:len(r.buf)-shift]
			r.buf = append(r.buf, p...)
		}
	}

	r.advanceHeadLocked(r.streamEnd - int64(len(r.buf)))
}

func isReplayablePrivateMode(mode string) bool {
	switch mode {
	case "1", "9", "25", "47", "1000", "1002", "1003", "1004",
		"1005", "1006", "1015", "1016", "1047", "1049", "2004":
		return true
	default:
		return false
	}
}

func mutuallyExclusivePrivateModes(mode string) []string {
	switch mode {
	case "47", "1047", "1049":
		return alternateBufferModes
	case "9", "1000", "1002", "1003":
		return mouseEventModes
	case "1005", "1006", "1015", "1016":
		return mouseEncodingModes
	default:
		return nil
	}
}

// trackPrivateModesLocked records complete DECSET/DECRST events with absolute
// stream offsets. scanTail bridges sequences split across PTY read chunks;
// matches ending before this chunk are ignored because they were already
// recorded on the prior call.
func (r *ringBuffer) trackPrivateModesLocked(chunkStart int64, p []byte) {
	combinedStart := chunkStart - int64(len(r.scanTail))
	combined := make([]byte, 0, len(r.scanTail)+len(p))
	combined = append(combined, r.scanTail...)
	combined = append(combined, p...)

	for _, match := range privateModePattern.FindAllSubmatchIndex(combined, -1) {
		if len(match) != 6 {
			continue
		}
		start := combinedStart + int64(match[0])
		end := combinedStart + int64(match[1])
		if end <= chunkStart {
			continue
		}
		params := string(combined[match[2]:match[3]])
		r.modeEvents = append(r.modeEvents, privateModeEvent{
			start:   start,
			end:     end,
			modes:   strings.Split(params, ";"),
			enabled: combined[match[4]] == 'h',
		})
	}

	start := len(combined) - privateModeScanTailCapacity
	if start < 0 {
		start = 0
	}
	r.scanTail = append(r.scanTail[:0], combined[start:]...)
}

func (r *ringBuffer) applyPrivateModeEventLocked(event privateModeEvent) {
	for _, mode := range event.modes {
		if !isReplayablePrivateMode(mode) {
			continue
		}
		if event.enabled {
			if group := mutuallyExclusivePrivateModes(mode); group != nil {
				for _, peer := range group {
					r.headModes[peer] = peer == mode
				}
				continue
			}
		}
		r.headModes[mode] = event.enabled
	}
}

// advanceHeadLocked folds mode transitions that are no longer present in the
// byte tail into the head checkpoint. If eviction cut through a private-mode
// sequence, discard the remaining fragment too so replay never begins with a
// malformed control sequence.
func (r *ringBuffer) advanceHeadLocked(target int64) {
	if target < r.headOffset {
		return
	}

	consumed := 0
	for consumed < len(r.modeEvents) {
		event := r.modeEvents[consumed]
		switch {
		case event.end <= target:
			r.applyPrivateModeEventLocked(event)
			consumed++
		case event.start < target:
			extra := event.end - target
			if extra > 0 {
				r.dropFrontLocked(extra)
				target = event.end
			}
			r.applyPrivateModeEventLocked(event)
			consumed++
		default:
			r.headOffset = target
			r.modeEvents = r.modeEvents[consumed:]
			return
		}
	}

	r.headOffset = target
	r.modeEvents = r.modeEvents[consumed:]
}

func (r *ringBuffer) dropFrontLocked(count int64) {
	if count <= 0 || len(r.buf) == 0 {
		return
	}
	if count >= int64(len(r.buf)) {
		r.buf = r.buf[:0]
		return
	}
	n := int(count)
	copy(r.buf, r.buf[n:])
	r.buf = r.buf[:len(r.buf)-n]
}

// ReplaySnapshot returns the terminal-mode checkpoint at the ring head and a
// copy of the retained bytes. The caller must write checkpoint, then clear and
// home the selected screen buffer, then write body.
func (r *ringBuffer) ReplaySnapshot() (checkpoint []byte, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Alternate-buffer state comes first so the caller's clear/home applies to
	// the correct buffer. For mutually-exclusive groups, reset stale peers
	// before setting the selected mode; emitting a peer reset afterward can
	// disable the selection in terminal emulators.
	checkpoint = r.appendModeGroupCheckpointLocked(checkpoint, alternateBufferModes)
	checkpoint = r.appendModeCheckpointLocked(checkpoint, "1")
	checkpoint = r.appendModeCheckpointLocked(checkpoint, "25")
	checkpoint = r.appendModeGroupCheckpointLocked(checkpoint, mouseEventModes)
	checkpoint = r.appendModeCheckpointLocked(checkpoint, "1004")
	checkpoint = r.appendModeGroupCheckpointLocked(checkpoint, mouseEncodingModes)
	checkpoint = r.appendModeCheckpointLocked(checkpoint, "2004")

	if len(r.buf) > 0 {
		body = make([]byte, len(r.buf))
		copy(body, r.buf)
	}
	return checkpoint, body
}

func (r *ringBuffer) appendModeGroupCheckpointLocked(dst []byte, modes []string) []byte {
	for _, enabled := range []bool{false, true} {
		for _, mode := range modes {
			state, seen := r.headModes[mode]
			if !seen || state != enabled {
				continue
			}
			dst = appendPrivateModeSequence(dst, mode, state)
		}
	}
	return dst
}

func (r *ringBuffer) appendModeCheckpointLocked(dst []byte, mode string) []byte {
	enabled, seen := r.headModes[mode]
	if !seen {
		return dst
	}
	return appendPrivateModeSequence(dst, mode, enabled)
}

func appendPrivateModeSequence(dst []byte, mode string, enabled bool) []byte {
	dst = append(dst, "\x1b[?"...)
	dst = append(dst, mode...)
	if enabled {
		return append(dst, 'h')
	}
	return append(dst, 'l')
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
