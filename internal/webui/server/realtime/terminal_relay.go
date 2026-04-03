package realtime

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"

	"nhooyr.io/websocket"
)

// SessionMonitor provides methods to check the health of a terminal session.
type SessionMonitor interface {
	HasSession(name string) bool
	PaneDead(name string) bool
	CapturePaneRaw(name string, lines int) string
}

// Resizer provides the ability to resize a terminal session.
type Resizer interface {
	Resize(connID string, cols, rows uint16) error
}

// ScrollbackAppender appends data to a scrollback buffer.
// May be nil to skip scrollback capture.
type ScrollbackAppender interface {
	Append(data []byte)
}

const (
	// WSCloseBackendExited is the WebSocket close code for backend process exit (4000-4999 range).
	WSCloseBackendExited = 4001

	// ResizeMsgLen is the length of an in-band resize message.
	ResizeMsgLen = 5
	// ResizeMsgMarker is the first byte marker for resize messages.
	ResizeMsgMarker = 0x01
	// TerminalReadBufSize is the buffer size for reading from PTY.
	TerminalReadBufSize = 4096
	// MaxTerminalCols is the maximum allowed terminal columns.
	MaxTerminalCols = 500
	// MaxTerminalRows is the maximum allowed terminal rows.
	MaxTerminalRows = 200
	// WSReadLimit is the WebSocket read limit in bytes.
	WSReadLimit = 32768 // 32KB; explicit limit for defense-in-depth
)

// CrashInfo communicates crash state from PtyToWS so the handler sets the right close code.
type CrashInfo struct {
	Crashed bool
	Reason  string
}

// WSClose returns the WebSocket close status code and reason string for a PTY session exit.
func (c CrashInfo) WSClose() (websocket.StatusCode, string) {
	if c.Crashed {
		return websocket.StatusCode(WSCloseBackendExited), c.Reason
	}
	return websocket.StatusNormalClosure, "session detached"
}

// PtyToWS relays PTY data to the WebSocket and detects backend crashes.
// If scrollback is non-nil, PTY output is also captured in the ring buffer.
func PtyToWS(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, pty io.Reader, sessionName string, monitor SessionMonitor, scrollback ScrollbackAppender) CrashInfo {
	buf := make([]byte, TerminalReadBufSize)
	for {
		select {
		case <-ctx.Done():
			return CrashInfo{}
		default:
		}

		n, err := pty.Read(buf)
		if err != nil {
			// PTY closed or error -- check if the backend process has exited.
			sessionGone := !monitor.HasSession(sessionName)
			paneDead := false
			if !sessionGone {
				paneDead = monitor.PaneDead(sessionName)
			}

			cancel()

			if sessionGone || paneDead {
				reason := "backend process exited"
				captured := monitor.CapturePaneRaw(sessionName, 10)
				if captured != "" {
					reason = captured
				}
				// WebSocket close reasons are limited to 123 bytes.
				// Truncate safely at UTF-8 rune boundaries.
				reason = TruncateUTF8(reason, 123)
				return CrashInfo{Crashed: true, Reason: reason}
			}
			return CrashInfo{}
		}

		if n > 0 {
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				// WebSocket write failed - cancel context to unblock WSToPTY
				cancel()
				return CrashInfo{}
			}
			if scrollback != nil {
				scrollback.Append(buf[:n])
			}
		}
	}
}

// WSToPTY reads from the WebSocket and writes to the PTY.
// Handles the in-band resize protocol.
func WSToPTY(ctx context.Context, conn *websocket.Conn, pty io.Writer, resizer Resizer, connID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := conn.Read(ctx)
		if err != nil {
			// WebSocket read failed - client disconnected
			return
		}

		// Binary messages may carry the in-band resize protocol.
		if msgType == websocket.MessageBinary {
			if len(data) == ResizeMsgLen && data[0] == ResizeMsgMarker {
				cols := binary.BigEndian.Uint16(data[1:3])
				rows := binary.BigEndian.Uint16(data[3:5])

				if cols > 0 && rows > 0 && cols <= MaxTerminalCols && rows <= MaxTerminalRows {
					if err := resizer.Resize(connID, cols, rows); err != nil {
						slog.Error("failed to resize terminal session", "conn_id", connID, "err", err)
					}
				}
				continue
			}
		}

		// Text and non-resize binary data - write to PTY
		if _, err := pty.Write(data); err != nil {
			// PTY write failed
			return
		}
	}
}

// TruncateUTF8 truncates s to at most maxBytes bytes, keeping the last portion
// and ensuring the result is valid UTF-8 (doesn't split multi-byte characters).
func TruncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Take the tail
	s = s[len(s)-maxBytes:]
	// Skip any leading bytes that are continuation bytes (10xxxxxx)
	// to avoid splitting a multi-byte UTF-8 sequence.
	for len(s) > 0 && s[0]&0xC0 == 0x80 {
		s = s[1:]
	}
	return s
}
