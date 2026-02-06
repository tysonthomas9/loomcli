package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestHandleTerminalWS_NilManagerWithSession tests nil manager with session param returns 503.
// The nil manager check happens before parameter validation.
func TestHandleTerminalWS_NilManagerWithSession(t *testing.T) {
	handler := handleTerminalWS(nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d for nil manager, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

// TestHandleTerminalWS_NilManager tests that nil manager returns 503.
func TestHandleTerminalWS_NilManager(t *testing.T) {
	handler := handleTerminalWS(nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error 'terminal manager not initialized', got %q", resp["error"])
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestHandleTerminalWS_MissingSessionWithManager tests missing session with a manager present.
func TestHandleTerminalWS_MissingSessionWithManager(t *testing.T) {
	// Create a real manager - this will fail if tmux is not installed,
	// so we skip if that's the case
	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, "")

	// Create request without session parameter
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "missing session parameter" {
		t.Errorf("expected error 'missing session parameter', got %q", resp["error"])
	}
}

// TestHandleTerminalWS_InvalidSessionName tests invalid session name validation.
func TestHandleTerminalWS_InvalidSessionName(t *testing.T) {
	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, "")

	tests := []struct {
		name    string
		session string
	}{
		{"contains space", "test session"},
		{"contains slash", "test/session"},
		{"contains dot", "test.session"},
		{"contains colon", "test:session"},
		{"contains semicolon", "test;session"},
		{"contains at sign", "test@session"},
		{"contains special chars", "test!#$%"},
		{"empty after trim", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// URL-encode the session parameter to handle special characters
			encodedSession := url.QueryEscape(tt.session)
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session="+encodedSession, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Empty/whitespace-only might be caught by "missing" check
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d for session %q, got %d", http.StatusBadRequest, tt.session, w.Code)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}

			if resp["success"] != false {
				t.Error("expected success to be false")
			}
		})
	}
}

// TestHandleTerminalWS_ValidSessionNames tests that valid session names pass validation.
func TestHandleTerminalWS_ValidSessionNames(t *testing.T) {
	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, "")

	tests := []struct {
		name    string
		session string
	}{
		{"alphanumeric", "test123"},
		{"with hyphen", "test-session"},
		{"with underscore", "test_session"},
		{"mixed", "Test-Session_123"},
		{"numbers only", "12345"},
		{"single char", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// URL-encode the session parameter
			encodedSession := url.QueryEscape(tt.session)
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session="+encodedSession, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Valid session names should NOT return 400
			// They might return other errors (WebSocket upgrade fails without proper headers)
			// but they should pass validation
			if w.Code == http.StatusBadRequest {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
					if errMsg, ok := resp["error"].(string); ok {
						if errMsg == "missing session parameter" || errMsg == "invalid session name: must match [a-zA-Z0-9_-]+" {
							t.Errorf("valid session %q was rejected with: %s", tt.session, errMsg)
						}
					}
				}
			}
		})
	}
}

// TestHandleTerminalWS_WebSocketUpgrade tests that WebSocket upgrade succeeds with valid params.
func TestHandleTerminalWS_WebSocketUpgrade(t *testing.T) {
	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, "")

	// Create a test server for WebSocket testing
	server := httptest.NewServer(handler)
	defer server.Close()

	// Convert http URL to ws URL
	wsURL := "ws" + server.URL[4:] + "?session=testws"

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dial the WebSocket
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// If tmux session creation fails, that's expected in test environment
		// The key is that we got past parameter validation
		t.Logf("WebSocket dial failed (expected if tmux unavailable): %v", err)
		if resp != nil && resp.StatusCode == http.StatusBadRequest {
			t.Errorf("unexpected 400 Bad Request - should have passed validation")
		}
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")

	t.Log("WebSocket connection established successfully")
}

// TestResizeMessageFormat tests the resize message format constants and parsing.
func TestResizeMessageFormat(t *testing.T) {
	// Test that constants are correct
	if resizeMsgMarker != 0x01 {
		t.Errorf("resizeMsgMarker = %#x, want %#x", resizeMsgMarker, 0x01)
	}
	if resizeMsgLen != 5 {
		t.Errorf("resizeMsgLen = %d, want %d", resizeMsgLen, 5)
	}

	// Test resize message parsing logic (same as in wsToPTY)
	tests := []struct {
		name     string
		data     []byte
		isResize bool
		cols     uint16
		rows     uint16
	}{
		{
			name:     "valid resize 80x24",
			data:     makeResizeMessage(80, 24),
			isResize: true,
			cols:     80,
			rows:     24,
		},
		{
			name:     "valid resize 120x40",
			data:     makeResizeMessage(120, 40),
			isResize: true,
			cols:     120,
			rows:     40,
		},
		{
			name:     "valid resize max values",
			data:     makeResizeMessage(65535, 65535),
			isResize: true,
			cols:     65535,
			rows:     65535,
		},
		{
			name:     "not resize - wrong marker",
			data:     []byte{0x02, 0x00, 80, 0x00, 24},
			isResize: false,
		},
		{
			name:     "not resize - too short",
			data:     []byte{0x01, 0x00, 80, 0x00},
			isResize: false,
		},
		{
			name:     "not resize - too long",
			data:     []byte{0x01, 0x00, 80, 0x00, 24, 0x00},
			isResize: false,
		},
		{
			name:     "not resize - empty",
			data:     []byte{},
			isResize: false,
		},
		{
			name:     "not resize - regular input",
			data:     []byte("hello"),
			isResize: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isResize := len(tt.data) == resizeMsgLen && len(tt.data) > 0 && tt.data[0] == resizeMsgMarker
			if isResize != tt.isResize {
				t.Errorf("isResize = %v, want %v", isResize, tt.isResize)
			}

			if isResize {
				cols := binary.BigEndian.Uint16(tt.data[1:3])
				rows := binary.BigEndian.Uint16(tt.data[3:5])
				if cols != tt.cols {
					t.Errorf("cols = %d, want %d", cols, tt.cols)
				}
				if rows != tt.rows {
					t.Errorf("rows = %d, want %d", rows, tt.rows)
				}
			}
		})
	}
}

// makeResizeMessage creates a resize message in the expected format.
func makeResizeMessage(cols, rows uint16) []byte {
	msg := make([]byte, 5)
	msg[0] = resizeMsgMarker
	binary.BigEndian.PutUint16(msg[1:3], cols)
	binary.BigEndian.PutUint16(msg[3:5], rows)
	return msg
}

// TestValidTerminalSessionRegex tests the session name validation regex.
func TestValidTerminalSessionRegex(t *testing.T) {
	tests := []struct {
		name    string
		session string
		valid   bool
	}{
		{"simple alpha", "test", true},
		{"simple numeric", "123", true},
		{"alphanumeric", "test123", true},
		{"with hyphen", "test-session", true},
		{"with underscore", "test_session", true},
		{"mixed case", "TestSession", true},
		{"all valid chars", "Test_Session-123", true},
		{"single char", "a", true},
		{"single number", "1", true},

		{"empty", "", false},
		{"space", " ", false},
		{"with space", "test session", false},
		{"leading space", " test", false},
		{"trailing space", "test ", false},
		{"with dot", "test.session", false},
		{"with slash", "test/session", false},
		{"with backslash", "test\\session", false},
		{"with colon", "test:session", false},
		{"with at", "test@session", false},
		{"with bang", "test!session", false},
		{"with hash", "test#session", false},
		{"with dollar", "test$session", false},
		{"with percent", "test%session", false},
		{"unicode", "test\u00e9", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validTerminalSession.MatchString(tt.session)
			if got != tt.valid {
				t.Errorf("validTerminalSession.MatchString(%q) = %v, want %v", tt.session, got, tt.valid)
			}
		})
	}
}

// TestTerminalReadBufSize tests the buffer size constant.
func TestTerminalReadBufSize(t *testing.T) {
	// Buffer should be reasonable size (4KB)
	if terminalReadBufSize != 4096 {
		t.Errorf("terminalReadBufSize = %d, want %d", terminalReadBufSize, 4096)
	}
}

// wsConn is an interface for WebSocket operations to enable testing.
type wsConn interface {
	Write(ctx context.Context, typ websocket.MessageType, data []byte) error
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
}

// mockWebSocket implements wsConn interface for testing.
type mockWebSocket struct {
	writeErr   error
	readErr    error
	readData   []byte
	readType   websocket.MessageType
	writeCalls int
	readCalls  int
	mu         sync.Mutex
}

func (m *mockWebSocket) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++
	if m.writeErr != nil {
		return m.writeErr
	}
	return nil
}

func (m *mockWebSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readCalls++

	// Check if context is cancelled first
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	default:
	}

	if m.readErr != nil {
		return 0, nil, m.readErr
	}
	return m.readType, m.readData, nil
}

// Ensure *websocket.Conn implements wsConn interface
var _ wsConn = (*websocket.Conn)(nil)

// testPtyToWS is a test wrapper for ptyToWS that accepts wsConn interface
func testPtyToWS(ctx context.Context, cancel context.CancelFunc, conn wsConn, session *TerminalSession) {
	buf := make([]byte, terminalReadBufSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := session.PTY.Read(buf)
		if err != nil {
			// PTY closed or error - cancel context to unblock wsToPTY
			cancel()
			return
		}

		if n > 0 {
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				// WebSocket write failed - client disconnected
				return
			}
		}
	}
}

// testWsToPTY is a test wrapper for wsToPTY that accepts wsConn interface.
// Mirrors the production wsToPTY: accepts both text and binary messages.
func testWsToPTY(ctx context.Context, conn wsConn, session *TerminalSession, manager *TerminalManager, connID string) {
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
			if len(data) == resizeMsgLen && data[0] == resizeMsgMarker {
				cols := binary.BigEndian.Uint16(data[1:3])
				rows := binary.BigEndian.Uint16(data[3:5])

				if cols > 0 && rows > 0 && cols <= maxTerminalCols && rows <= maxTerminalRows {
					if err := manager.Resize(connID, cols, rows); err != nil {
						log.Printf("Failed to resize terminal connection %q: %v", connID, err)
					}
				}
				continue
			}
		}

		// Text and non-resize binary data - write to PTY
		if _, err := session.PTY.Write(data); err != nil {
			// PTY write failed
			return
		}
	}
}

// TestPtyToWS_PTYReadError verifies that when PTY.Read() returns an error,
// the context cancel function is called to unblock wsToPTY.
func TestPtyToWS_PTYReadError(t *testing.T) {
	// Create an os.Pipe to simulate PTY
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	// Create mock WebSocket
	mockWS := &mockWebSocket{}

	// Create mock session
	session := &TerminalSession{
		Name: "test",
		PTY:  r, // Use read end as the PTY that ptyToWS will read from
	}

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Track whether cancel was called
	cancelCalled := make(chan struct{})
	wrappedCancel := func() {
		cancel()
		close(cancelCalled)
	}

	// Run testPtyToWS in goroutine
	done := make(chan struct{})
	go func() {
		testPtyToWS(ctx, wrappedCancel, mockWS, session)
		close(done)
	}()

	// Close the write end to cause Read to return EOF
	w.Close()

	// Wait for cancel to be called or timeout
	select {
	case <-cancelCalled:
		// Success - cancel was called
	case <-time.After(1 * time.Second):
		t.Fatal("cancel() was not called within timeout after PTY read error")
	}

	// Wait for testPtyToWS to finish
	select {
	case <-done:
		// Success - testPtyToWS returned
	case <-time.After(1 * time.Second):
		t.Fatal("testPtyToWS did not return within timeout after PTY read error")
	}
}

// TestPtyToWS_ContextCancelledBeforeRead verifies that ptyToWS exits cleanly
// when the context is cancelled before any PTY read.
func TestPtyToWS_ContextCancelledBeforeRead(t *testing.T) {
	// Create an os.Pipe to simulate PTY (would block on read)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	mockWS := &mockWebSocket{}
	session := &TerminalSession{
		Name: "test",
		PTY:  r,
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Run testPtyToWS
	done := make(chan struct{})
	go func() {
		testPtyToWS(ctx, cancel, mockWS, session)
		close(done)
	}()

	// Should return immediately due to cancelled context
	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("testPtyToWS did not return within timeout when context was pre-cancelled")
	}

	// Should not have made any WebSocket writes
	if mockWS.writeCalls > 0 {
		t.Errorf("expected 0 WebSocket writes with cancelled context, got %d", mockWS.writeCalls)
	}
}

// TestPtyToWS_WebSocketWriteError verifies that ptyToWS exits when
// WebSocket write fails (client disconnected).
func TestPtyToWS_WebSocketWriteError(t *testing.T) {
	// Create an os.Pipe and write data to it
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	// Create mock WebSocket that fails on write
	mockWS := &mockWebSocket{
		writeErr: errors.New("websocket: close sent"),
	}

	session := &TerminalSession{
		Name: "test",
		PTY:  r,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run testPtyToWS
	done := make(chan struct{})
	go func() {
		testPtyToWS(ctx, cancel, mockWS, session)
		close(done)
	}()

	// Write some data to trigger read and write
	go func() {
		w.Write([]byte("hello terminal"))
		time.Sleep(100 * time.Millisecond)
		w.Close()
	}()

	// Should return after WebSocket write fails
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("testPtyToWS did not return within timeout after WebSocket write error")
	}

	// Should have attempted to write to WebSocket
	if mockWS.writeCalls == 0 {
		t.Error("expected at least 1 WebSocket write attempt")
	}
}

// TestPtyToWS_SuccessfulDataRelay verifies that ptyToWS successfully
// reads from PTY and writes to WebSocket.
func TestPtyToWS_SuccessfulDataRelay(t *testing.T) {
	testData := []byte("terminal output data")

	// Create an os.Pipe
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	mockWS := &mockWebSocket{}

	session := &TerminalSession{
		Name: "test",
		PTY:  r,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run testPtyToWS
	done := make(chan struct{})
	go func() {
		testPtyToWS(ctx, cancel, mockWS, session)
		close(done)
	}()

	// Write data and then close
	go func() {
		w.Write(testData)
		time.Sleep(50 * time.Millisecond)
		w.Close()
	}()

	// Wait for completion
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("testPtyToWS did not complete within timeout")
	}

	// Verify data was written to WebSocket
	if mockWS.writeCalls == 0 {
		t.Error("expected at least 1 WebSocket write")
	}
}

// TestWsToPTY_ContextCancelled verifies that wsToPTY exits when context
// is cancelled (e.g., by ptyToWS after PTY read error).
func TestWsToPTY_ContextCancelled(t *testing.T) {
	mockWS := &mockWebSocket{
		readType: websocket.MessageBinary,
		readData: []byte("input"),
	}

	// Create an os.Pipe for the PTY
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{
		Name: "test",
		PTY:  w,
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// Create context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Run testWsToPTY
	done := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mockWS, session, manager, "test")
		close(done)
	}()

	// Should return immediately due to cancelled context
	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("testWsToPTY did not return within timeout when context was cancelled")
	}

	// Context should be done
	select {
	case <-ctx.Done():
		// Success
	default:
		t.Error("context should be cancelled")
	}
}

// TestWsToPTY_WebSocketReadError verifies that wsToPTY exits cleanly
// when WebSocket read fails (client disconnected).
func TestWsToPTY_WebSocketReadError(t *testing.T) {
	mockWS := &mockWebSocket{
		readErr: errors.New("websocket: close 1000"),
	}

	// Create an os.Pipe for the PTY
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{
		Name: "test",
		PTY:  w,
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run testWsToPTY
	done := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mockWS, session, manager, "test")
		close(done)
	}()

	// Should return after WebSocket read fails
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("testWsToPTY did not return within timeout after WebSocket read error")
	}
}

// TestPtyToWS_And_WsToPTY_Integration verifies the interaction between
// ptyToWS and wsToPTY: when PTY read fails, context is cancelled and
// wsToPTY exits.
func TestPtyToWS_And_WsToPTY_Integration(t *testing.T) {
	// Create os.Pipes for PTY simulation
	ptyR, ptyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer ptyR.Close()

	// Create mock WebSocket
	mockWS := &mockWebSocket{
		readType: websocket.MessageBinary,
		readData: []byte("input"),
	}

	session := &TerminalSession{
		Name: "test",
		PTY:  ptyR, // testPtyToWS reads from ptyR
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start testPtyToWS goroutine
	ptyDone := make(chan struct{})
	go func() {
		testPtyToWS(ctx, cancel, mockWS, session)
		close(ptyDone)
	}()

	// Start testWsToPTY goroutine
	wsDone := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mockWS, session, manager, "test")
		close(wsDone)
	}()

	// Write some initial data
	ptyW.Write([]byte("initial data"))

	// Simulate PTY closure after a short delay
	time.Sleep(100 * time.Millisecond)
	ptyW.Close() // This causes testPtyToWS to get EOF and call cancel()

	// Both goroutines should exit within timeout
	timeout := time.After(2 * time.Second)

	select {
	case <-ptyDone:
		t.Log("testPtyToWS exited successfully")
	case <-timeout:
		t.Fatal("testPtyToWS did not exit within timeout")
	}

	select {
	case <-wsDone:
		t.Log("testWsToPTY exited successfully after context cancellation")
	case <-timeout:
		t.Fatal("testWsToPTY did not exit within timeout after context cancellation")
	}

	// Verify context was cancelled
	select {
	case <-ctx.Done():
		// Success - context was cancelled
	default:
		t.Error("context was not cancelled after PTY error")
	}
}

// TestWsToPTY_TextMessageForwardedToPTY verifies that text WebSocket messages
// (as sent by xterm.js onData) are forwarded to the PTY, not silently dropped.
func TestWsToPTY_TextMessageForwardedToPTY(t *testing.T) {
	// Create an os.Pipe for the PTY
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	inputData := "hello from xterm"

	// mockWebSocket that returns one text message then an error to stop the loop.
	callCount := 0
	mock := &sequenceMockWS{
		reads: []mockRead{
			{typ: websocket.MessageText, data: []byte(inputData), err: nil},
			{typ: 0, data: nil, err: errors.New("done")},
		},
	}

	_ = callCount // unused

	session := &TerminalSession{
		Name: "test-text",
		PTY:  w, // wsToPTY writes to w; we read from r
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mock, session, manager, "test-text")
		close(done)
	}()

	// Read what wsToPTY wrote to the PTY pipe
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read from PTY pipe: %v", err)
	}

	got := string(buf[:n])
	if got != inputData {
		t.Errorf("PTY received %q, want %q", got, inputData)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("testWsToPTY did not exit within timeout")
	}
}

// TestWsToPTY_BinaryDataForwardedToPTY verifies that non-resize binary messages
// are forwarded to the PTY.
func TestWsToPTY_BinaryDataForwardedToPTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	inputData := []byte("binary input data")

	mock := &sequenceMockWS{
		reads: []mockRead{
			{typ: websocket.MessageBinary, data: inputData, err: nil},
			{typ: 0, data: nil, err: errors.New("done")},
		},
	}

	session := &TerminalSession{
		Name: "test-binary",
		PTY:  w,
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mock, session, manager, "test-binary")
		close(done)
	}()

	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read from PTY pipe: %v", err)
	}

	got := string(buf[:n])
	if got != string(inputData) {
		t.Errorf("PTY received %q, want %q", got, string(inputData))
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("testWsToPTY did not exit within timeout")
	}
}

// TestWsToPTY_ResizeNotForwardedToPTY verifies that binary resize messages
// are handled as resize commands and NOT written to the PTY as data.
func TestWsToPTY_ResizeNotForwardedToPTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	resizeMsg := makeResizeMessage(120, 40)
	followupData := []byte("after-resize")

	mock := &sequenceMockWS{
		reads: []mockRead{
			{typ: websocket.MessageBinary, data: resizeMsg, err: nil},
			{typ: websocket.MessageText, data: followupData, err: nil},
			{typ: 0, data: nil, err: errors.New("done")},
		},
	}

	session := &TerminalSession{
		Name: "test-resize",
		PTY:  w,
	}

	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		testWsToPTY(ctx, mock, session, manager, "test-resize")
		close(done)
	}()

	// The resize message should be consumed; only the follow-up text should reach the PTY.
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read from PTY pipe: %v", err)
	}

	got := string(buf[:n])
	if got != string(followupData) {
		t.Errorf("PTY received %q, want %q (resize data should not be forwarded)", got, string(followupData))
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("testWsToPTY did not exit within timeout")
	}
}

// TestTerminalWebSocket_E2E is an end-to-end test that connects a real WebSocket
// to the terminal handler, sends text input, and verifies output is received.
// Uses gorilla/websocket-style net.Conn for deadline control since nhooyr.io/websocket
// closes connections on context cancellation.
func TestTerminalWebSocket_E2E(t *testing.T) {
	manager, err := NewTerminalManager("", "")
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, "")
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "?session=e2e-test"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	// Send resize (binary) - must succeed
	resizeMsg := makeResizeMessage(80, 24)
	if err := conn.Write(ctx, websocket.MessageBinary, resizeMsg); err != nil {
		t.Fatalf("failed to send resize: %v", err)
	}

	// Let the shell settle and produce initial output
	time.Sleep(500 * time.Millisecond)

	// Use a goroutine to collect all output asynchronously
	outputCh := make(chan string, 100)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			outputCh <- string(data)
		}
	}()

	// Drain initial output
	drainDone := time.After(1 * time.Second)
drainLoop:
	for {
		select {
		case <-outputCh:
			// discard
		case <-drainDone:
			break drainLoop
		}
	}

	// Send a text command (like xterm.js does)
	testCmd := "echo E2E_TERMINAL_TEST_OK\n"
	if err := conn.Write(ctx, websocket.MessageText, []byte(testCmd)); err != nil {
		t.Fatalf("failed to send text command: %v", err)
	}

	// Read output and look for our marker
	found := false
	deadline := time.After(5 * time.Second)
textLoop:
	for {
		select {
		case output := <-outputCh:
			if contains(output, "E2E_TERMINAL_TEST_OK") {
				found = true
				break textLoop
			}
		case <-deadline:
			break textLoop
		}
	}

	if !found {
		t.Error("did not receive expected output 'E2E_TERMINAL_TEST_OK' from terminal after sending text message")
	}

	// Also verify binary data input works
	binaryCmd := []byte("echo E2E_BINARY_TEST_OK\n")
	if err := conn.Write(ctx, websocket.MessageBinary, binaryCmd); err != nil {
		t.Fatalf("failed to send binary command: %v", err)
	}

	foundBinary := false
	deadline2 := time.After(5 * time.Second)
binaryLoop:
	for {
		select {
		case output := <-outputCh:
			if contains(output, "E2E_BINARY_TEST_OK") {
				foundBinary = true
				break binaryLoop
			}
		case <-deadline2:
			break binaryLoop
		}
	}

	if !foundBinary {
		t.Error("did not receive expected output 'E2E_BINARY_TEST_OK' from terminal after sending binary message")
	}
}

// contains checks if s contains substr (simple helper to avoid importing strings).
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockRead represents a single read result from a mock WebSocket.
type mockRead struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

// sequenceMockWS returns a sequence of predetermined read results.
type sequenceMockWS struct {
	reads []mockRead
	idx   int
	mu    sync.Mutex
}

func (m *sequenceMockWS) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	return nil
}

func (m *sequenceMockWS) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx >= len(m.reads) {
		return 0, nil, errors.New("no more reads")
	}
	r := m.reads[m.idx]
	m.idx++
	return r.typ, r.data, r.err
}
