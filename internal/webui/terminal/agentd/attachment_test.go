package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	agentdpb "github.com/tysonthomas9/loom-agentd/proto/agentdpb"
	cpb "github.com/tysonthomas9/loom-control-plane/proto/cpb"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// fakeAgentd is a minimal in-memory TerminalServer used by the attachment
// tests below. Each handler is a func field so individual tests can reshape
// the server-side behaviour (return a Replay, abort with Unavailable, accept
// 3 inputs and echo them back…) without spinning up a fresh gRPC server.
type fakeAgentd struct {
	agentdpb.UnimplementedTerminalServer

	attachFunc func(srv agentdpb.Terminal_AttachServer) error
	listFunc   func(ctx context.Context, req *agentdpb.ListRequest) (*agentdpb.ListResponse, error)
	killFunc   func(ctx context.Context, req *agentdpb.KillRequest) (*agentdpb.KillResponse, error)

	killCalls   atomic.Int32
	listCalls   atomic.Int32
	attachCalls atomic.Int32

	// killReceived snapshots the last KillRequest the fake observed. Tests
	// read it after Kill returns to assert the session field round-tripped.
	mu           sync.Mutex
	killReceived *agentdpb.KillRequest
}

func (f *fakeAgentd) Attach(srv agentdpb.Terminal_AttachServer) error {
	f.attachCalls.Add(1)
	if f.attachFunc != nil {
		return f.attachFunc(srv)
	}
	return status.Error(codes.Unimplemented, "fake Attach not configured")
}

func (f *fakeAgentd) List(ctx context.Context, req *agentdpb.ListRequest) (*agentdpb.ListResponse, error) {
	f.listCalls.Add(1)
	if f.listFunc != nil {
		return f.listFunc(ctx, req)
	}
	return &agentdpb.ListResponse{}, nil
}

func (f *fakeAgentd) Kill(ctx context.Context, req *agentdpb.KillRequest) (*agentdpb.KillResponse, error) {
	f.killCalls.Add(1)
	f.mu.Lock()
	f.killReceived = req
	f.mu.Unlock()
	if f.killFunc != nil {
		return f.killFunc(ctx, req)
	}
	return &agentdpb.KillResponse{Killed: true}, nil
}

// startFakeAgentd spins up the fake agentd on a bufconn listener and
// returns a dialer that can be plugged into AgentdClient.withDialer or
// invoked directly. The returned cleanup runs via t.Cleanup so individual
// tests don't need to manage server / listener lifetimes manually.
type fakeAgentdHandle struct {
	server *fakeAgentd
	dialer agentdDialer
}

func startFakeAgentd(t *testing.T, fake *fakeAgentd) *fakeAgentdHandle {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	agentdpb.RegisterTerminalServer(srv, fake)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("agentd grpc server stopped: %v", err)
		}
	}()

	dialer := func(_ context.Context, _ string, _ int32, _ *tls.Config) (*agentdConn, error) {
		ctxDialer := func(_ context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}
		conn, err := grpc.NewClient(
			"passthrough://bufconn-agentd",
			grpc.WithContextDialer(ctxDialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return nil, err
		}
		return dialAgentdFromConn(conn), nil
	}

	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
		wg.Wait()
	})

	return &fakeAgentdHandle{server: fake, dialer: dialer}
}

// newAttachmentClient is the per-test combo helper that boots both the fake
// control-plane (for routing) and the fake agentd (for stream/Kill/List),
// stitches them together with a dialer override, and returns a ready-to-go
// AgentdClient.
func newAttachmentClient(t *testing.T, cp *fakeControlPlane, agent *fakeAgentd, certTTL time.Duration) *AgentdClient {
	t.Helper()
	cpClient := startFakeCP(t, cp, certTTL)
	agentdHandle := startFakeAgentd(t, agent)
	return cpClient.withDialer(agentdHandle.dialer)
}

// readyOnlyAttach is a small Attach handler that responds with AttachReady
// (no replay, no output) and then blocks until the client closes the
// stream. Used by the controlplane tests that only need the handshake to
// succeed.
func readyOnlyAttach(connID string, reattached bool) func(agentdpb.Terminal_AttachServer) error {
	return func(srv agentdpb.Terminal_AttachServer) error {
		// Wait for AttachOpen.
		open, err := srv.Recv()
		if err != nil {
			return err
		}
		if open.GetOpen() == nil {
			return status.Error(codes.FailedPrecondition, "expected AttachOpen first")
		}
		if err := srv.Send(&agentdpb.AttachServerMsg{
			Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{
				ConnId: connID, Cols: open.GetOpen().GetCols(), Rows: open.GetOpen().GetRows(), Reattached: reattached,
			}},
		}); err != nil {
			return err
		}
		// Park until the client tears the stream down.
		for {
			if _, err := srv.Recv(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}
}

// readN drains n payloads off ch with a deadline, returning them in order.
// A test failure is reported (via t.Fatalf) if fewer than n arrive in time.
func readN(t *testing.T, ch <-chan []byte, n int, timeout time.Duration) [][]byte {
	t.Helper()
	out := make([][]byte, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for i := 0; i < n; i++ {
		select {
		case b, ok := <-ch:
			if !ok {
				t.Fatalf("output channel closed after %d/%d frames", i, n)
			}
			out = append(out, b)
		case <-deadline.C:
			t.Fatalf("readN: timeout after %d/%d frames", i, n)
		}
	}
	return out
}

// awaitClosed waits until ch is closed, returning whatever payload (possibly
// empty) was draining at the time. Fails the test on timeout.
func awaitClosed(t *testing.T, ch <-chan []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline.C:
			t.Fatalf("awaitClosed: channel still open after %s", timeout)
		}
	}
}

// firstAttachClient is a tiny helper: drives AttachSession, t.Fatal on
// error, and returns the live attachment + reattached flag.
func firstAttachClient(t *testing.T, c *AgentdClient, key terminal.SessionKey) (terminal.Attachment, bool) {
	t.Helper()
	att, reattached, err := c.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if att == nil {
		t.Fatalf("AttachSession att = nil")
	}
	return att, reattached
}

func TestAgentdAttachment_HappyPath(t *testing.T) {
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			open, err := srv.Recv()
			if err != nil {
				return err
			}
			if open.GetOpen().GetSession() != "main" {
				return status.Errorf(codes.InvalidArgument, "session = %q, want main", open.GetOpen().GetSession())
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-happy"}},
			}); err != nil {
				return err
			}
			for _, p := range [][]byte{[]byte("hello "), []byte("world")} {
				if err := srv.Send(&agentdpb.AttachServerMsg{
					Msg: &agentdpb.AttachServerMsg_Output{Output: &agentdpb.AttachOutput{Data: p}},
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cpFake := &fakeControlPlane{
		resolveFn: resolveOK(t),
	}
	c := newAttachmentClient(t, cpFake, fakeAg, 0)

	att, reattached := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})
	if reattached {
		t.Errorf("reattached = true, want false on first attach")
	}
	if att.ConnID() != "conn-happy" {
		t.Errorf("ConnID = %q, want %q", att.ConnID(), "conn-happy")
	}

	frames := readN(t, att.Output(), 2, 2*time.Second)
	if string(frames[0]) != "hello " {
		t.Errorf("frame[0] = %q, want %q", frames[0], "hello ")
	}
	if string(frames[1]) != "world" {
		t.Errorf("frame[1] = %q, want %q", frames[1], "world")
	}

	awaitClosed(t, att.Output(), 2*time.Second)
	if got := att.ExitReason(); got != "" {
		t.Errorf("ExitReason = %q after clean EOF, want empty", got)
	}
}

func TestAgentdAttachment_Reattach_WithReplay(t *testing.T) {
	replayBytes := []byte("\x1b[2J\x1b[Hpartial-screen-restore")
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-replay", Reattached: true}},
			}); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Replay{Replay: &agentdpb.AttachReplay{Data: replayBytes}},
			}); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Output{Output: &agentdpb.AttachOutput{Data: []byte("live!")}},
			}); err != nil {
				return err
			}
			return nil
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)

	att, reattached := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})
	if !reattached {
		t.Errorf("reattached = false, want true")
	}

	frames := readN(t, att.Output(), 1, 2*time.Second)
	if string(frames[0]) != "live!" {
		t.Errorf("output frame = %q, want %q", frames[0], "live!")
	}
	awaitClosed(t, att.Output(), 2*time.Second)

	// Replay frame should be exposed verbatim (including the reset escape).
	if got := att.Scrollback(); string(got) != string(replayBytes) {
		t.Errorf("Scrollback = %q, want %q", got, replayBytes)
	}
}

func TestAgentdAttachment_KilledFrame(t *testing.T) {
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-killed"}},
			}); err != nil {
				return err
			}
			return srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Killed{Killed: &agentdpb.AttachKilled{Reason: "idle_reap"}},
			})
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)
	att, _ := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})

	awaitClosed(t, att.Output(), 2*time.Second)
	if got := att.ExitReason(); got != "idle_reap" {
		t.Errorf("ExitReason = %q, want %q", got, "idle_reap")
	}
}

func TestAgentdAttachment_WriteInput(t *testing.T) {
	received := make(chan []byte, 8)
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-input"}},
			}); err != nil {
				return err
			}
			for {
				msg, err := srv.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}
					return err
				}
				if in := msg.GetInput(); in != nil {
					received <- append([]byte(nil), in.GetData()...)
				}
			}
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)
	att, _ := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})

	chunks := [][]byte{[]byte("ls\n"), []byte("pwd\n"), []byte("exit\n")}
	for _, ch := range chunks {
		n, err := att.WriteInput(ch)
		if err != nil {
			t.Fatalf("WriteInput(%q): %v", ch, err)
		}
		if n != len(ch) {
			t.Errorf("WriteInput(%q) n = %d, want %d", ch, n, len(ch))
		}
	}

	for i, want := range chunks {
		select {
		case got := <-received:
			if string(got) != string(want) {
				t.Errorf("server received chunk[%d] = %q, want %q", i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for chunk[%d]", i)
		}
	}
}

func TestAgentdAttachment_Resize_StreamSend(t *testing.T) {
	resizes := make(chan *agentdpb.AttachResize, 4)
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-resize"}},
			}); err != nil {
				return err
			}
			for {
				msg, err := srv.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}
					return err
				}
				if r := msg.GetResize(); r != nil {
					resizes <- r
				}
			}
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)
	att, _ := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})

	if err := att.Resize("ignored", 132, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	select {
	case r := <-resizes:
		if r.GetCols() != 132 || r.GetRows() != 50 {
			t.Errorf("server received cols=%d rows=%d, want 132/50", r.GetCols(), r.GetRows())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for AttachResize")
	}
}

func TestAgentdAttachment_FirstFrameNotReady_Errors(t *testing.T) {
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			// Wrong order: send Output before Ready.
			return srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Output{Output: &agentdpb.AttachOutput{Data: []byte("nope")}},
			})
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("AttachSession error code = %v, want FailedPrecondition (err=%v)", got, err)
	}
}

func TestAgentdAttachment_StreamErrorClosesOutput(t *testing.T) {
	fakeAg := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{ConnId: "conn-aborted"}},
			}); err != nil {
				return err
			}
			// Abort with a non-EOF, non-cancellation error.
			return status.Error(codes.Unavailable, "agentd rolling restart")
		},
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, 0)
	att, _ := firstAttachClient(t, c, terminal.SessionKey{Workspace: "demo", Name: "main"})

	awaitClosed(t, att.Output(), 2*time.Second)
	got := att.ExitReason()
	if !strings.HasPrefix(got, "stream_error:") {
		t.Errorf("ExitReason = %q, want prefix %q", got, "stream_error:")
	}
}

func TestAgentdClient_Kill_DispatchesToAgentd(t *testing.T) {
	fakeAg := &fakeAgentd{
		attachFunc: readyOnlyAttach("conn-kill", false),
	}
	c := newAttachmentClient(t, &fakeControlPlane{resolveFn: resolveOK(t)}, fakeAg, time.Minute)

	key := terminal.SessionKey{Workspace: "demo", Name: "main"}
	att, _ := firstAttachClient(t, c, key)

	if err := c.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := fakeAg.killCalls.Load(); got != 1 {
		t.Errorf("Kill call count = %d, want 1", got)
	}
	fakeAg.mu.Lock()
	got := fakeAg.killReceived
	fakeAg.mu.Unlock()
	if got == nil {
		t.Fatalf("fake agentd never recorded a KillRequest")
	}
	if got.GetSession() != key.Name {
		t.Errorf("KillRequest.Session = %q, want %q", got.GetSession(), key.Name)
	}
	if !got.GetForce() {
		t.Errorf("KillRequest.Force = false, want true")
	}

	// Tearing down the attachment is the cleanup contract; pretty much a
	// behavioral check that close() doesn't deadlock when the server-side
	// stream has already been canceled by the prior Kill RPC closing the
	// fake agentd's view of the session (in this fake the streams are
	// independent, but the close path must still be safe).
	if a, ok := att.(*agentdAttachment); ok {
		a.close("test-cleanup")
	}
}

// resolveOK returns a fakeControlPlane resolveFn that hands back a
// readyResolveResponse for the requested workspace / agent. Pulled out so
// tests don't all repeat the same closure.
func resolveOK(t *testing.T) func(*cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
	t.Helper()
	return func(req *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
		return readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()), nil
	}
}
