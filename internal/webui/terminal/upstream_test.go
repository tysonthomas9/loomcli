package terminal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	sdktypes "github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

type fakeUpstream struct {
	output chan []byte

	mu      sync.Mutex
	writes  [][]byte
	resizes []fakeResize
}

type fakeResize struct {
	cols uint16
	rows uint16
}

func newFakeUpstream() *fakeUpstream {
	return &fakeUpstream{output: make(chan []byte)}
}

func (u *fakeUpstream) Output() <-chan []byte {
	return u.output
}

func (u *fakeUpstream) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	u.mu.Lock()
	u.writes = append(u.writes, cp)
	u.mu.Unlock()
	return len(p), nil
}

func (u *fakeUpstream) Resize(_ context.Context, cols, rows uint16) error {
	u.mu.Lock()
	u.resizes = append(u.resizes, fakeResize{cols: cols, rows: rows})
	u.mu.Unlock()
	return nil
}

func (u *fakeUpstream) Close() error {
	return nil
}

func (u *fakeUpstream) snapshot() ([][]byte, []fakeResize) {
	u.mu.Lock()
	defer u.mu.Unlock()
	writes := append([][]byte(nil), u.writes...)
	resizes := append([]fakeResize(nil), u.resizes...)
	return writes, resizes
}

type fakeDaytonaPTYHandle struct {
	data chan []byte

	mu                sync.Mutex
	dataChanCalls     int
	writes            [][]byte
	resizes           []fakeResize
	disconnectCalls   int
	killCalls         int
	waitCalls         int
	sendErr           error
	resizeErr         error
	disconnectErr     error
	waitErr           error
	connectionWaitHit chan struct{}
}

func newFakeDaytonaPTYHandle() *fakeDaytonaPTYHandle {
	return &fakeDaytonaPTYHandle{data: make(chan []byte)}
}

func (h *fakeDaytonaPTYHandle) DataChan() <-chan []byte {
	h.mu.Lock()
	h.dataChanCalls++
	h.mu.Unlock()
	return h.data
}

func (h *fakeDaytonaPTYHandle) SendInput(p []byte) error {
	if h.sendErr != nil {
		return h.sendErr
	}
	cp := append([]byte(nil), p...)
	h.mu.Lock()
	h.writes = append(h.writes, cp)
	h.mu.Unlock()
	return nil
}

func (h *fakeDaytonaPTYHandle) Resize(_ context.Context, cols, rows int) (*sdktypes.PtySessionInfo, error) {
	if h.resizeErr != nil {
		return nil, h.resizeErr
	}
	h.mu.Lock()
	h.resizes = append(h.resizes, fakeResize{cols: uint16(cols), rows: uint16(rows)})
	h.mu.Unlock()
	return &sdktypes.PtySessionInfo{ID: DefaultDaytonaLeadPTYSessionID, Cols: cols, Rows: rows}, nil
}

func (h *fakeDaytonaPTYHandle) Disconnect() error {
	h.mu.Lock()
	h.disconnectCalls++
	h.mu.Unlock()
	return h.disconnectErr
}

func (h *fakeDaytonaPTYHandle) WaitForConnection(context.Context) error {
	h.mu.Lock()
	h.waitCalls++
	h.mu.Unlock()
	if h.connectionWaitHit != nil {
		close(h.connectionWaitHit)
	}
	return h.waitErr
}

func (h *fakeDaytonaPTYHandle) Kill(context.Context) error {
	h.mu.Lock()
	h.killCalls++
	h.mu.Unlock()
	return nil
}

func (h *fakeDaytonaPTYHandle) snapshot() (dataChanCalls int, writes [][]byte, resizes []fakeResize, disconnects, kills, waits int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	writes = append([][]byte(nil), h.writes...)
	resizes = append([]fakeResize(nil), h.resizes...)
	return h.dataChanCalls, writes, resizes, h.disconnectCalls, h.killCalls, h.waitCalls
}

type fakeDaytonaPTYConnector struct {
	sessions    []daytonaPTYSession
	handle      *fakeDaytonaPTYHandle
	listErr     error
	connectErr  error
	connectID   string
	connectCall int
}

func (c *fakeDaytonaPTYConnector) ListPtySessions(context.Context) ([]daytonaPTYSession, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return append([]daytonaPTYSession(nil), c.sessions...), nil
}

func (c *fakeDaytonaPTYConnector) ConnectPty(_ context.Context, sessionID string) (daytonaPTYHandle, error) {
	c.connectID = sessionID
	c.connectCall++
	if c.connectErr != nil {
		return nil, c.connectErr
	}
	return c.handle, nil
}

func TestPtySessionPumpsUpstreamFanoutAndDelegatesInput(t *testing.T) {
	upstream := newFakeUpstream()
	key := SessionKey{Workspace: "ws1", Name: "fake"}
	ended := make(chan SessionKey, 1)

	session := newPtySession(key, upstream, func(k SessionKey) {
		ended <- k
	})
	sink := make(chan []byte, 1)
	session.frameSink = func(p []byte) {
		sink <- append([]byte(nil), p...)
	}
	go session.drain()

	att1 := session.attachNew("conn-1")
	if att1 == nil {
		t.Fatal("first attach returned nil")
	}
	att2 := session.attachNew("conn-2")
	if att2 == nil {
		t.Fatal("second attach returned nil")
	}

	chunk := []byte("hello from upstream\n")
	select {
	case upstream.output <- chunk:
	case <-time.After(time.Second):
		t.Fatal("timeout sending fake upstream output")
	}

	if got := readOutputFrame(t, att1.Output(), time.Second); !bytes.Equal(got, chunk) {
		t.Fatalf("first attachment output = %q; want %q", got, chunk)
	}
	if got := readOutputFrame(t, att2.Output(), time.Second); !bytes.Equal(got, chunk) {
		t.Fatalf("second attachment output = %q; want %q", got, chunk)
	}
	if got := readOutputFrame(t, sink, time.Second); !bytes.Equal(got, chunk) {
		t.Fatalf("frameSink output = %q; want %q", got, chunk)
	}

	att3 := session.attachNew("conn-3")
	if att3 == nil {
		t.Fatal("third attach returned nil")
	}
	replay := att3.Scrollback()
	if !bytes.HasPrefix(replay, screenResetSeq) {
		t.Fatalf("replay prefix = %q; want screen reset", replay[:min(len(replay), len(screenResetSeq))])
	}
	if !bytes.Contains(replay, chunk) {
		t.Fatalf("replay = %q; want prior chunk", replay)
	}

	if n, err := att1.WriteInput([]byte("typed")); err != nil || n != len("typed") {
		t.Fatalf("WriteInput n=%d err=%v; want n=%d nil", n, err, len("typed"))
	}
	if err := att1.Resize("ignored", 120, 45); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	writes, resizes := upstream.snapshot()
	if len(writes) != 1 || !bytes.Equal(writes[0], []byte("typed")) {
		t.Fatalf("upstream writes = %q; want typed", writes)
	}
	if len(resizes) != 1 || resizes[0] != (fakeResize{cols: 120, rows: 45}) {
		t.Fatalf("upstream resizes = %+v; want 120x45", resizes)
	}

	close(upstream.output)
	select {
	case got := <-ended:
		if got != key {
			t.Fatalf("upstream end key = %+v; want %+v", got, key)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for upstream-end callback")
	}

	if err := session.close(ExitReasonExited); err != nil {
		t.Fatalf("session close: %v", err)
	}
	if reason := att1.ExitReason(); reason != ExitReasonExited {
		t.Fatalf("attachment exit reason = %q; want %q", reason, ExitReasonExited)
	}
}

func TestPtySessionCloseSuppressesUpstreamEndAfterDone(t *testing.T) {
	upstream := newFakeUpstream()
	key := SessionKey{Workspace: "ws1", Name: "fake"}
	ended := make(chan SessionKey, 1)
	drained := make(chan struct{})

	session := newPtySession(key, upstream, func(k SessionKey) {
		ended <- k
	})
	go func() {
		session.drain()
		close(drained)
	}()

	if err := session.close(ExitReasonKilled); err != nil {
		t.Fatalf("session close: %v", err)
	}
	close(upstream.output)

	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for drain to exit")
	}
	select {
	case got := <-ended:
		t.Fatalf("upstream-end callback fired after close: %+v", got)
	default:
	}
}

func TestPtySessionDrainOrdersRingSinkAndFanout(t *testing.T) {
	upstream := newFakeUpstream()
	key := SessionKey{Workspace: "ws1", Name: "fake"}
	session := newPtySession(key, upstream, func(SessionKey) {})
	drained := make(chan struct{})
	att := session.attachNew("conn-1")
	if att == nil {
		t.Fatal("attach returned nil")
	}

	sinkEntered := make(chan struct{})
	sinkMayReturn := make(chan struct{})
	sinkSawScrollback := make(chan bool, 1)
	session.frameSink = func(p []byte) {
		_, body := session.scrollback.ReplaySnapshot()
		sinkSawScrollback <- bytes.Contains(body, p)
		close(sinkEntered)
		<-sinkMayReturn
	}
	go func() {
		session.drain()
		close(drained)
	}()

	chunk := []byte("ordered frame\n")
	select {
	case upstream.output <- chunk:
	case <-time.After(time.Second):
		t.Fatal("timeout sending fake upstream output")
	}
	select {
	case <-sinkEntered:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for frameSink")
	}
	if !<-sinkSawScrollback {
		t.Fatal("frameSink ran before scrollback append")
	}
	select {
	case got := <-att.Output():
		t.Fatalf("attachment received frame before frameSink returned: %q", got)
	default:
	}

	close(sinkMayReturn)
	if got := readOutputFrame(t, att.Output(), time.Second); !bytes.Equal(got, chunk) {
		t.Fatalf("attachment output = %q; want %q", got, chunk)
	}
	if err := session.close(ExitReasonShutdown); err != nil {
		t.Fatalf("session close: %v", err)
	}
	close(upstream.output)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for drain to exit")
	}
}

func TestHostUpstreamCloseUnblocksReaderBlockedOnSend(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = writeFile.Close() })

	upstream := newHostUpstream(readFile, nil)
	_ = upstream.Output()
	if _, err := writeFile.Write([]byte("blocked frame")); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
	waitUntil(t, hostUpstreamReaderBlockedOnSend, time.Second, "host upstream reader blocked on output send")

	closed := make(chan error, 1)
	go func() {
		closed <- upstream.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Close to unblock reader")
	}
	select {
	case _, ok := <-upstream.Output():
		if ok {
			t.Fatal("output channel still open after Close")
		}
	default:
		t.Fatal("output channel not closed after Close")
	}
}

func TestHostUpstreamCloseIsIdempotent(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = writeFile.Close() })

	upstream := newHostUpstream(readFile, nil)
	first := upstream.Close()
	second := upstream.Close()
	if first != second {
		t.Fatalf("second close error = %v; want same as first %v", second, first)
	}
	if second != nil {
		t.Fatalf("second close returned error: %v", second)
	}
}

func TestHostUpstreamOutputResizeAndClose(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "printf host-upstream-ready; sleep 30") //nolint:norawexec // deterministic local test command
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("pty.StartWithSize: %v", err)
	}
	upstream := newHostUpstream(ptmx, cmd)
	defer func() { _ = upstream.Close() }()

	if err := upstream.Resize(context.Background(), 100, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if !readOutputContains(t, upstream.Output(), []byte("host-upstream-ready"), 2*time.Second) {
		t.Fatal("host upstream output missing ready marker")
	}
	if err := upstream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	waitUntil(t, func() bool {
		select {
		case _, ok := <-upstream.Output():
			return !ok
		default:
			return false
		}
	}, 2*time.Second, "host upstream output channel to close")
}

func TestDaytonaPTYUpstreamConnectsExistingSessionOnly(t *testing.T) {
	handle := newFakeDaytonaPTYHandle()
	connector := &fakeDaytonaPTYConnector{
		sessions: []daytonaPTYSession{{ID: "other"}, {ID: DefaultDaytonaLeadPTYSessionID}},
		handle:   handle,
	}

	upstream, err := newDaytonaPTYUpstreamFromConnector(context.Background(), connector, "sandbox-1", DefaultDaytonaLeadPTYSessionID)
	if err != nil {
		t.Fatalf("newDaytonaPTYUpstreamFromConnector: %v", err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	if connector.connectID != DefaultDaytonaLeadPTYSessionID || connector.connectCall != 1 {
		t.Fatalf("connect pty id/calls = %q/%d, want %q/1", connector.connectID, connector.connectCall, DefaultDaytonaLeadPTYSessionID)
	}
	_, _, _, _, _, waits := handle.snapshot()
	if waits != 1 {
		t.Fatalf("WaitForConnection calls = %d, want 1", waits)
	}
}

func TestDaytonaPTYUpstreamMissingSessionDoesNotConnect(t *testing.T) {
	connector := &fakeDaytonaPTYConnector{
		sessions: []daytonaPTYSession{{ID: "other"}},
		handle:   newFakeDaytonaPTYHandle(),
	}

	_, err := newDaytonaPTYUpstreamFromConnector(context.Background(), connector, "sandbox-1", DefaultDaytonaLeadPTYSessionID)
	if !errors.Is(err, ErrDaytonaPTYSessionNotFound) {
		t.Fatalf("error = %v, want ErrDaytonaPTYSessionNotFound", err)
	}
	if connector.connectCall != 0 {
		t.Fatalf("ConnectPty calls = %d, want 0 for absent session", connector.connectCall)
	}
}

func TestDaytonaPTYUpstreamOutputStartsSingleReaderAndPreservesOrder(t *testing.T) {
	handle := newFakeDaytonaPTYHandle()
	upstream := newDaytonaPTYUpstream(handle)
	out1 := upstream.Output()
	out2 := upstream.Output()
	if out1 != out2 {
		t.Fatal("Output returned different channels across calls")
	}
	waitUntil(t, func() bool {
		calls, _, _, _, _, _ := handle.snapshot()
		return calls == 1
	}, time.Second, "daytona DataChan to be consumed once")

	chunks := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	for _, chunk := range chunks {
		select {
		case handle.data <- chunk:
		case <-time.After(time.Second):
			t.Fatal("timeout sending daytona output")
		}
		if got := readOutputFrame(t, out1, time.Second); !bytes.Equal(got, chunk) {
			t.Fatalf("output chunk = %q, want %q", got, chunk)
		}
	}
	if err := upstream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDaytonaPTYUpstreamWriteResizeAndCloseDisconnectOnly(t *testing.T) {
	handle := newFakeDaytonaPTYHandle()
	upstream := newDaytonaPTYUpstream(handle)

	if n, err := upstream.Write([]byte("typed")); err != nil || n != len("typed") {
		t.Fatalf("Write n=%d err=%v, want %d nil", n, err, len("typed"))
	}
	if err := upstream.Resize(context.Background(), 100, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := upstream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := upstream.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	_, writes, resizes, disconnects, kills, _ := handle.snapshot()
	if len(writes) != 1 || !bytes.Equal(writes[0], []byte("typed")) {
		t.Fatalf("writes = %q, want typed", writes)
	}
	if len(resizes) != 1 || resizes[0] != (fakeResize{cols: 100, rows: 40}) {
		t.Fatalf("resizes = %+v, want 100x40", resizes)
	}
	if disconnects != 1 {
		t.Fatalf("Disconnect calls = %d, want 1", disconnects)
	}
	if kills != 0 {
		t.Fatalf("Kill calls = %d, want 0; Close must not kill remote PTY", kills)
	}
}

func TestDaytonaPTYUpstreamCloseReturnsFirstDisconnectError(t *testing.T) {
	want := errors.New("disconnect failed")
	handle := newFakeDaytonaPTYHandle()
	handle.disconnectErr = want
	upstream := newDaytonaPTYUpstream(handle)

	if err := upstream.Close(); !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want %v", err, want)
	}
	if err := upstream.Close(); !errors.Is(err, want) {
		t.Fatalf("second Close error = %v, want %v", err, want)
	}
	_, _, _, disconnects, _, _ := handle.snapshot()
	if disconnects != 1 {
		t.Fatalf("Disconnect calls = %d, want 1", disconnects)
	}
}

func TestDaytonaPTYUpstreamCloseUnblocksReaderBlockedOnSend(t *testing.T) {
	handle := newFakeDaytonaPTYHandle()
	upstream := newDaytonaPTYUpstream(handle)
	out := upstream.Output()

	select {
	case handle.data <- []byte("blocked frame"):
	case <-time.After(time.Second):
		t.Fatal("timeout sending daytona output")
	}
	waitUntil(t, daytonaPTYUpstreamReaderBlockedOnSend, time.Second, "daytona upstream reader blocked on output send")

	closed := make(chan error, 1)
	go func() {
		closed <- upstream.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Close to unblock reader")
	}
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("output channel still open after Close")
		}
	default:
		t.Fatal("output channel not closed after Close")
	}
}

func hostUpstreamReaderBlockedOnSend() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
		header, _, _ := strings.Cut(stack, "\n")
		if strings.Contains(header, "[select]") &&
			strings.Contains(stack, "terminal.(*hostUpstream).readOutput") {
			return true
		}
	}
	return false
}

func daytonaPTYUpstreamReaderBlockedOnSend() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
		header, _, _ := strings.Cut(stack, "\n")
		if strings.Contains(header, "[select]") &&
			strings.Contains(stack, "terminal.(*daytonaPTYUpstream).readOutput") {
			return true
		}
	}
	return false
}

func readOutputFrame(t *testing.T, ch <-chan []byte, deadline time.Duration) []byte {
	t.Helper()
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("output channel closed")
		}
		return got
	case <-time.After(deadline):
		t.Fatal("timeout waiting for output frame")
	}
	return nil
}

func readOutputContains(t *testing.T, ch <-chan []byte, needle []byte, deadline time.Duration) bool {
	t.Helper()
	timeout := time.After(deadline)
	var out []byte
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return bytes.Contains(out, needle)
			}
			out = append(out, chunk...)
			if bytes.Contains(out, needle) {
				return true
			}
		case <-timeout:
			return bytes.Contains(out, needle)
		}
	}
}
