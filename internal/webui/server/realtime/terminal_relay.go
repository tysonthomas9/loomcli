package realtime

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"nhooyr.io/websocket"
)

// SessionMonitor provides methods to check the health of a tmux-backed
// terminal session. Pass nil for the PTYManager-backed web path where there
// is no tmux to inspect — PtyToWS will then report non-crash shell exit as a
// clean close.
type SessionMonitor interface {
	HasSession(name string) bool
	PaneDead(name string) bool
	CapturePaneRaw(name string, lines int) string
}

// Resizer is the resize surface WSToPTY calls when it parses a \x1b[RESIZE:
// escape. Both PTYManager and AgentTmuxManager satisfy it.
type Resizer interface {
	Resize(connID string, cols, rows uint16) error
}

// ScrollbackAppender appends data to a scrollback buffer. May be nil.
type ScrollbackAppender interface {
	Append(data []byte)
}

const (
	// WSCloseBackendExited is the WebSocket close code for backend process exit (4000-4999 range).
	WSCloseBackendExited = 4001

	// TerminalReadBufSize is the buffer size for reading from PTY.
	TerminalReadBufSize = 4096
	// MaxTerminalCols is the maximum allowed terminal columns.
	MaxTerminalCols = 500
	// MaxTerminalRows is the maximum allowed terminal rows.
	MaxTerminalRows = 200
	// WSReadLimit is the WebSocket read limit in bytes.
	WSReadLimit = 32768 // 32KB; explicit limit for defense-in-depth
)

// resizeRE matches the wterm in-band resize escape: \x1b[RESIZE:<cols>;<rows>]
// (matches the wterm local example server/client wire format verbatim).
var resizeRE = regexp.MustCompile(`^\x1b\[RESIZE:(\d+);(\d+)\]$`)

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
// If monitor is nil (PTYManager path), any read error is treated as a normal
// shell exit — there is no tmux pane to inspect. If monitor is non-nil and
// the tmux session is gone or the pane is dead, the close is reported as a
// crash with up to 10 lines of captured pane output as the reason.
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
			cancel()
			if monitor == nil {
				// PTYManager path: no tmux to inspect; treat as clean exit.
				return CrashInfo{}
			}
			sessionGone := !monitor.HasSession(sessionName)
			paneDead := false
			if !sessionGone {
				paneDead = monitor.PaneDead(sessionName)
			}
			if sessionGone || paneDead {
				reason := "backend process exited"
				captured := monitor.CapturePaneRaw(sessionName, 10)
				if captured != "" {
					reason = captured
				}
				reason = TruncateUTF8(reason, 123)
				return CrashInfo{Crashed: true, Reason: reason}
			}
			return CrashInfo{}
		}

		if n > 0 {
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
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
//
// Mirrors wterm/examples/local/server.ts: every message is treated as a
// UTF-8 string; a message starting with "\x1b[RESIZE:" is parsed as a
// resize request (decimal cols;rows); anything else is written to the PTY
// verbatim. No separate binary framing is used — wterm-react sends
// keystrokes as strings and the resize escape is also a string.
func WSToPTY(ctx context.Context, conn *websocket.Conn, pty io.Writer, resizer Resizer, connID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		if len(data) > 0 && strings.HasPrefix(string(data), "\x1b[RESIZE:") {
			if m := resizeRE.FindStringSubmatch(string(data)); m != nil {
				cols, _ := strconv.Atoi(m[1])
				rows, _ := strconv.Atoi(m[2])
				if cols > 0 && rows > 0 && cols <= MaxTerminalCols && rows <= MaxTerminalRows {
					if err := resizer.Resize(connID, uint16(cols), uint16(rows)); err != nil {
						slog.Error("failed to resize terminal session", "conn_id", connID, "err", err)
					}
				}
				continue
			}
		}

		if _, err := pty.Write(data); err != nil {
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
	s = s[len(s)-maxBytes:]
	for len(s) > 0 && s[0]&0xC0 == 0x80 {
		s = s[1:]
	}
	return s
}
