package realtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestTruncateUTF8_ShortString(t *testing.T) {
	s := "hello"
	got := TruncateUTF8(s, 123)
	if got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
}

func TestTruncateUTF8_ExactLimit(t *testing.T) {
	s := "abc"
	got := TruncateUTF8(s, 3)
	if got != "abc" {
		t.Errorf("expected %q, got %q", "abc", got)
	}
}

func TestTruncateUTF8_TakesTail(t *testing.T) {
	s := "abcdefghij" // 10 bytes
	got := TruncateUTF8(s, 5)
	if got != "fghij" {
		t.Errorf("expected %q, got %q", "fghij", got)
	}
}

func TestTruncateUTF8_MultibyteNotSplit(t *testing.T) {
	// UTF-8 representation of some multi-byte characters
	// Each character below is 3 bytes in UTF-8
	s := "\xe4\xb8\x96\xe7\x95\x8c" // "世界" (6 bytes total)

	// maxBytes=4 => take last 4 bytes => "\x95\x8c" would be invalid start...
	// The function takes tail then skips continuation bytes.
	// Last 4 bytes: \x95 \x8c \xe7... wait, let me recalculate.
	// s = [e4 b8 96 e7 95 8c], len=6
	// s[6-4:] = s[2:] = [96 e7 95 8c]
	// 96 = 10010110 => continuation byte (10xxxxxx), skip
	// e7 = 11100111 => start byte, stop
	// result = [e7 95 8c] = "界"
	got := TruncateUTF8(s, 4)
	if got != "\xe7\x95\x8c" { // "界"
		t.Errorf("expected single char, got %q (len %d)", got, len(got))
	}
}

func TestTruncateUTF8_AllContinuationBytes(t *testing.T) {
	// Edge case: if after truncation we only have continuation bytes
	// (shouldn't happen with valid UTF-8, but test the safety)
	s := "\xc0\x80\x80" // technically invalid UTF-8, but tests the loop
	got := TruncateUTF8(s, 2)
	// last 2 bytes: [80 80], both continuation bytes => skip all => empty
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTruncateUTF8_EmptyString(t *testing.T) {
	got := TruncateUTF8("", 10)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCrashInfo_WSClose_Crashed(t *testing.T) {
	ci := CrashInfo{Crashed: true, Reason: "process died"}
	code, reason := ci.WSClose()
	if code != websocket.StatusCode(WSCloseBackendExited) {
		t.Errorf("expected status %d, got %d", WSCloseBackendExited, code)
	}
	if reason != "process died" {
		t.Errorf("expected 'process died', got %q", reason)
	}
}

func TestCrashInfo_WSClose_Normal(t *testing.T) {
	ci := CrashInfo{Crashed: false}
	code, reason := ci.WSClose()
	if code != websocket.StatusNormalClosure {
		t.Errorf("expected StatusNormalClosure, got %d", code)
	}
	if reason != "session detached" {
		t.Errorf("expected 'session detached', got %q", reason)
	}
}

func TestCrashInfo_WSClose_Killed(t *testing.T) {
	ci := CrashInfo{Killed: true, Reason: "session killed"}
	code, reason := ci.WSClose()
	if code != websocket.StatusCode(WSCloseSessionKilled) {
		t.Errorf("expected status %d, got %d", WSCloseSessionKilled, code)
	}
	if reason != "session killed" {
		t.Errorf("expected 'session killed', got %q", reason)
	}
}

func TestCrashInfo_WSClose_KilledDefaultsReason(t *testing.T) {
	ci := CrashInfo{Killed: true}
	_, reason := ci.WSClose()
	if reason != "session killed" {
		t.Errorf("expected default reason 'session killed', got %q", reason)
	}
}

func TestWSToPTYWritesInputAndHandlesResize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pty bytes.Buffer
	resizer := &fakeResizer{}
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		WSToPTY(ctx, conn, &pty, resizer, "conn-1")
		close(done)
	}))
	defer server.Close()

	conn, _, err := websocket.Dial(ctx, wsURL(server.URL), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("\x1b[RESIZE:120;40]")); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("echo hi\n")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WSToPTY did not exit after client close")
	}
	if pty.String() != "echo hi\n" {
		t.Fatalf("pty data = %q", pty.String())
	}
	if resizer.connID != "conn-1" || resizer.cols != 120 || resizer.rows != 40 {
		t.Fatalf("resize = %+v", resizer)
	}
}

func TestPtyToWSWritesOutputAndTreatsEOFAsCleanExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientCtx, clientCancel := context.WithTimeout(context.Background(), time.Second)
	defer clientCancel()
	done := make(chan CrashInfo, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		done <- PtyToWS(ctx, cancel, conn, strings.NewReader("hello"), "sess", nil, &fakeScrollback{})
	}))
	defer server.Close()

	conn, _, err := websocket.Dial(clientCtx, wsURL(server.URL), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	_, data, err := conn.Read(clientCtx)
	if err != nil {
		t.Fatalf("read pty frame: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("ws data = %q", data)
	}

	select {
	case info := <-done:
		if info.Crashed || info.Killed {
			t.Fatalf("PtyToWS info = %+v", info)
		}
	case <-time.After(time.Second):
		t.Fatal("PtyToWS did not exit after EOF")
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestPtyToWSDetectsCrashedTmuxSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientCtx, clientCancel := context.WithTimeout(context.Background(), time.Second)
	defer clientCancel()
	done := make(chan CrashInfo, 1)
	monitor := &fakeSessionMonitor{hasSession: false, captured: strings.Repeat("x", 200)}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		done <- PtyToWS(ctx, cancel, conn, errReader{}, "sess", monitor, nil)
	}))
	defer server.Close()

	conn, _, err := websocket.Dial(clientCtx, wsURL(server.URL), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()

	select {
	case info := <-done:
		if !info.Crashed || len(info.Reason) > 123 {
			t.Fatalf("PtyToWS crash info = %+v", info)
		}
	case <-time.After(time.Second):
		t.Fatal("PtyToWS did not report crash")
	}
}

func TestAttachmentToWSWritesFramesAndMapsKilledReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientCtx, clientCancel := context.WithTimeout(context.Background(), time.Second)
	defer clientCancel()
	output := make(chan []byte, 1)
	att := &fakeAttachmentExit{output: output, reason: "killed"}
	done := make(chan CrashInfo, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		done <- AttachmentToWS(ctx, cancel, conn, att)
	}))
	defer server.Close()

	conn, _, err := websocket.Dial(clientCtx, wsURL(server.URL), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	output <- []byte("frame")
	_, data, err := conn.Read(clientCtx)
	if err != nil {
		t.Fatalf("read attachment frame: %v", err)
	}
	if string(data) != "frame" {
		t.Fatalf("attachment frame = %q", data)
	}
	close(output)
	_, _, _ = conn.Read(clientCtx)

	select {
	case info := <-done:
		if !info.Killed || info.Reason != "session killed" {
			t.Fatalf("AttachmentToWS info = %+v", info)
		}
	case <-time.After(time.Second):
		t.Fatal("AttachmentToWS did not exit after output close")
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

type fakeResizer struct {
	connID     string
	cols, rows uint16
}

func (f *fakeResizer) Resize(connID string, cols, rows uint16) error {
	f.connID, f.cols, f.rows = connID, cols, rows
	return nil
}

type fakeScrollback struct {
	data []byte
}

func (f *fakeScrollback) Append(data []byte) {
	f.data = append(f.data, data...)
}

type fakeSessionMonitor struct {
	hasSession bool
	paneDead   bool
	captured   string
}

func (f *fakeSessionMonitor) HasSession(string) bool            { return f.hasSession }
func (f *fakeSessionMonitor) PaneDead(string) bool              { return f.paneDead }
func (f *fakeSessionMonitor) CapturePaneRaw(string, int) string { return f.captured }
func (f *fakeAttachmentExit) Output() <-chan []byte             { return f.output }
func (f *fakeAttachmentExit) ExitReason() string                { return f.reason }
func (errReader) Read([]byte) (int, error)                      { return 0, io.ErrUnexpectedEOF }
func wsURL(httpURL string) string                               { return "ws" + strings.TrimPrefix(httpURL, "http") }

type fakeAttachmentExit struct {
	output <-chan []byte
	reason string
}

type errReader struct{}
