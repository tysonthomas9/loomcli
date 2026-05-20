package terminal

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck

	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestHandleTerminalWSRelayHappyPath(t *testing.T) {
	att := &relayAttachment{
		connID:     "conn-1",
		out:        make(chan []byte, 2),
		scrollback: []byte("scrollback"),
		exitReason: "exited",
	}
	manager := &relayPTYSource{att: att, max: 2, detachedCh: make(chan struct{})}
	server := httptest.NewServer(HandleTerminalWS(manager, nil, nil, "", nil, nil, nil, time.Now()))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?session=shell&cols=120&rows=40"
	conn, _, err := websocket.Dial(ctx, wsURL, nil) //nolint:staticcheck
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }() //nolint:staticcheck

	_, replay, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read scrollback replay: %v", err)
	}
	if string(replay) != "scrollback" {
		t.Fatalf("scrollback replay = %q", replay)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte("\x1b[RESIZE:100;30]")); err != nil { //nolint:staticcheck
		t.Fatalf("write resize: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil { //nolint:staticcheck
		t.Fatalf("write input: %v", err)
	}
	att.out <- []byte("live-output")
	_, live, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read live output: %v", err)
	}
	if string(live) != "live-output" {
		t.Fatalf("live output = %q", live)
	}
	close(att.out)
	_, _, _ = conn.Read(ctx)

	select {
	case <-manager.detachedCh:
	case <-ctx.Done():
		t.Fatal("terminal manager Detach was not called")
	}
	if !bytes.Contains(att.written, []byte("hello")) {
		t.Fatalf("attachment input = %q, want hello", att.written)
	}
	if att.resizeConnID != "conn-1" || att.cols != 100 || att.rows != 30 {
		t.Fatalf("resize = conn:%q %dx%d, want conn-1 100x30", att.resizeConnID, att.cols, att.rows)
	}
	if manager.attachCols != 120 || manager.attachRows != 40 {
		t.Fatalf("initial size = %dx%d, want 120x40", manager.attachCols, manager.attachRows)
	}
}

type relayPTYSource struct {
	att        *relayAttachment
	has        bool
	detached   bool
	detachedCh chan struct{}
	attachCols uint16
	attachRows uint16
	max        int
}

func (f *relayPTYSource) AttachSession(_ webuterminal.SessionKey, cols, rows uint16, _ *webuterminal.LaunchSpec) (webuterminal.Attachment, bool, error) {
	f.has = true
	f.attachCols = cols
	f.attachRows = rows
	return f.att, false, nil
}
func (f *relayPTYSource) Detach(webuterminal.SessionKey, string) {
	f.detached = true
	if f.detachedCh != nil {
		close(f.detachedCh)
	}
}
func (f *relayPTYSource) Kill(webuterminal.SessionKey) error      { return nil }
func (f *relayPTYSource) HasSession(webuterminal.SessionKey) bool { return f.has }
func (f *relayPTYSource) SessionClosed(webuterminal.SessionKey) bool {
	return false
}
func (f *relayPTYSource) AttachmentCount(webuterminal.SessionKey) int { return 0 }
func (f *relayPTYSource) SessionCount() int                           { return 0 }
func (f *relayPTYSource) SessionCountFor(string) int                  { return 0 }
func (f *relayPTYSource) MaxSessions() int {
	if f.max == 0 {
		return 1
	}
	return f.max
}

type relayAttachment struct {
	connID       string
	out          chan []byte
	written      []byte
	scrollback   []byte
	exitReason   string
	resizeConnID string
	cols         uint16
	rows         uint16
}

func (f *relayAttachment) ConnID() string { return f.connID }
func (f *relayAttachment) Output() <-chan []byte {
	return f.out
}
func (f *relayAttachment) WriteInput(p []byte) (int, error) {
	f.written = append(f.written, p...)
	return len(p), nil
}
func (f *relayAttachment) Scrollback() []byte { return f.scrollback }
func (f *relayAttachment) Resize(connID string, cols, rows uint16) error {
	f.resizeConnID = connID
	f.cols = cols
	f.rows = rows
	return nil
}
func (f *relayAttachment) ExitReason() string { return f.exitReason }
