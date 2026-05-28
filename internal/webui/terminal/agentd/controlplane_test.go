package agentd

import (
	"context"
	"net"
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

// fakeControlPlane is a minimal in-memory ControlPlaneServer used by the
// integration tests below. Each handler is a func field so individual tests
// can reshape behavior (return NotFound, return READY, return PROVISIONING…)
// without spinning up a fresh server. Counters use atomic.Int32 so a test
// can assert call counts after concurrent attaches without locking.
type fakeControlPlane struct {
	cpb.UnimplementedControlPlaneServer

	resolveFn func(*cpb.ResolveRequest) (*cpb.ResolveResponse, error)
	ensureFn  func(*cpb.EnsureRequest) (*cpb.EnsureResponse, error)
	releaseFn func(*cpb.ReleaseRequest) (*cpb.ReleaseResponse, error)

	resolveCalls atomic.Int32
	ensureCalls  atomic.Int32
	releaseCalls atomic.Int32
}

func (f *fakeControlPlane) ResolveAgent(_ context.Context, req *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
	f.resolveCalls.Add(1)
	if f.resolveFn != nil {
		return f.resolveFn(req)
	}
	return nil, status.Error(codes.Unimplemented, "fake ResolveAgent not configured")
}

func (f *fakeControlPlane) EnsureAlive(_ context.Context, req *cpb.EnsureRequest) (*cpb.EnsureResponse, error) {
	f.ensureCalls.Add(1)
	if f.ensureFn != nil {
		return f.ensureFn(req)
	}
	return nil, status.Error(codes.Unimplemented, "fake EnsureAlive not configured")
}

func (f *fakeControlPlane) ReleaseAttach(_ context.Context, req *cpb.ReleaseRequest) (*cpb.ReleaseResponse, error) {
	f.releaseCalls.Add(1)
	if f.releaseFn != nil {
		return f.releaseFn(req)
	}
	return &cpb.ReleaseResponse{}, nil
}

// startFakeCP spins the fake on an in-memory bufconn listener and returns a
// dialed *AgentdClient ready for AttachSession. The cleanup tears down both
// the gRPC server and the client connection.
func startFakeCP(t *testing.T, fake *fakeControlPlane, certTTL time.Duration) *AgentdClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	cpb.RegisterControlPlaneServer(srv, fake)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("grpc server stopped: %v", err)
		}
	}()

	dialer := func(_ context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(context.Background())
	}
	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient bufconn: %v", err)
	}

	cp := newControlPlaneClientFromConn(conn)
	caPEM, _ := makeSelfSigned(t, "agent.demo.main") // dummy CA root used by tlsConfigFromPEM
	client := newWithControlPlane(cp, []byte(caPEM), certTTL)

	t.Cleanup(func() {
		_ = client.Close()
		srv.Stop()
		_ = lis.Close()
		wg.Wait()
	})
	return client
}

// readyResolveResponse builds a fully-populated ResolveResponse with a
// freshly-minted self-signed cert + key pair so callers don't have to
// repeat the cert plumbing. The cert + key returned here also serve as
// the agentd CA root inside startFakeCP — self-signed certs make this
// trivial.
func readyResolveResponse(t *testing.T, ws, agent string) *cpb.ResolveResponse {
	t.Helper()
	cert, key := makeSelfSigned(t, "agent."+ws+"."+agent)
	return &cpb.ResolveResponse{
		Kind:        cpb.AgentKind_AGENT_PERSISTENT,
		VmHost:      "vm-" + agent + ".local",
		VsockCid:    "tcp://vm-" + agent + ".local:9100",
		AgentdPort:  9100,
		MtlsCertPem: cert,
		MtlsKeyPem:  key,
	}
}

// attachWithStubAgentd wraps a control-plane fake with a stub agentd that
// answers AttachOpen with a vanilla AttachReady. Used by the routing-focused
// tests below so they can assert ResolveAgent / EnsureAlive call counts
// without standing up a custom Attach handler each time.
func attachWithStubAgentd(t *testing.T, cp *fakeControlPlane, certTTL time.Duration, reattached bool) *AgentdClient {
	t.Helper()
	stub := &fakeAgentd{attachFunc: readyOnlyAttach("conn-stub", reattached)}
	return newAttachmentClient(t, cp, stub, certTTL)
}

func TestAttachSession_ResolveHit(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(req *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()), nil
		},
	}
	c := attachWithStubAgentd(t, fake, 0, false)

	att, reattached, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if att == nil {
		t.Fatalf("AttachSession att = nil, want agentdAttachment")
	}
	if reattached {
		t.Errorf("first AttachSession reattached = true, want false")
	}
	if got := fake.resolveCalls.Load(); got != 1 {
		t.Errorf("ResolveAgent calls = %d, want 1", got)
	}
	if got := fake.ensureCalls.Load(); got != 0 {
		t.Errorf("EnsureAlive calls = %d, want 0 on resolve hit", got)
	}
}

func TestAttachSession_ResolveNotFound_FallsBackToEnsure(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return nil, status.Error(codes.NotFound, "no live agent")
		},
		ensureFn: func(req *cpb.EnsureRequest) (*cpb.EnsureResponse, error) {
			return &cpb.EnsureResponse{
				Status:  cpb.AgentStatus_READY,
				Routing: readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()),
			}, nil
		},
	}
	c := attachWithStubAgentd(t, fake, 0, false)

	att, reattached, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if att == nil {
		t.Fatalf("AttachSession att = nil")
	}
	if reattached {
		t.Errorf("reattached = true, want false")
	}
	if got := fake.resolveCalls.Load(); got != 1 {
		t.Errorf("ResolveAgent calls = %d, want 1", got)
	}
	if got := fake.ensureCalls.Load(); got != 1 {
		t.Errorf("EnsureAlive calls = %d, want 1", got)
	}
}

func TestAttachSession_HitCacheOnSecondCall(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(req *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()), nil
		},
	}
	// Stub agentd echoes AttachOpen.expect_replay back as AttachReady.reattached.
	// That makes the routing-cache hit observable end-to-end: the second call
	// should see reattached=true because the cache-hit path requested replay.
	stub := &fakeAgentd{
		attachFunc: func(srv agentdpb.Terminal_AttachServer) error {
			open, err := srv.Recv()
			if err != nil {
				return err
			}
			expect := open.GetOpen().GetExpectReplay()
			if err := srv.Send(&agentdpb.AttachServerMsg{
				Msg: &agentdpb.AttachServerMsg_Ready{Ready: &agentdpb.AttachReady{
					ConnId: "conn-cache", Reattached: expect,
				}},
			}); err != nil {
				return err
			}
			for {
				if _, err := srv.Recv(); err != nil {
					return nil
				}
			}
		},
	}
	c := newAttachmentClient(t, fake, stub, time.Minute)

	key := terminal.SessionKey{Workspace: "demo", Name: "main"}
	if _, _, err := c.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("first AttachSession: %v", err)
	}
	att, reattached, err := c.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("second AttachSession: %v", err)
	}
	if att == nil {
		t.Fatalf("second AttachSession att = nil")
	}
	if !reattached {
		t.Errorf("second AttachSession reattached = false, want true on cache hit")
	}
	if got := fake.resolveCalls.Load(); got != 1 {
		t.Errorf("ResolveAgent calls = %d, want 1 (second call should hit cache)", got)
	}
}

func TestAttachSession_CacheTTLExpiry(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(req *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()), nil
		},
	}
	c := attachWithStubAgentd(t, fake, 10*time.Millisecond, false)

	key := terminal.SessionKey{Workspace: "demo", Name: "main"}
	if _, _, err := c.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("first AttachSession: %v", err)
	}
	// Sleep slightly longer than the cache TTL so the second call must
	// re-hit ResolveAgent.
	time.Sleep(25 * time.Millisecond)
	if _, _, err := c.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("second AttachSession: %v", err)
	}
	if got := fake.resolveCalls.Load(); got != 2 {
		t.Errorf("ResolveAgent calls = %d, want 2 after TTL expiry", got)
	}
}

func TestAttachSession_RejectsEmptyVMHost(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			cert, key := makeSelfSigned(t, "agent.demo.main")
			return &cpb.ResolveResponse{
				Kind:        cpb.AgentKind_AGENT_PERSISTENT,
				VmHost:      "", // invalid
				AgentdPort:  9100,
				MtlsCertPem: cert,
				MtlsKeyPem:  key,
			}, nil
		},
	}
	c := startFakeCP(t, fake, 0)

	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("AttachSession with empty vm_host code = %v, want Internal", got)
	}
}

func TestAttachSession_RejectsZeroPort(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			cert, key := makeSelfSigned(t, "agent.demo.main")
			return &cpb.ResolveResponse{
				VmHost:      "vm.local",
				AgentdPort:  0,
				MtlsCertPem: cert,
				MtlsKeyPem:  key,
			}, nil
		},
	}
	c := startFakeCP(t, fake, 0)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("AttachSession with port=0 code = %v, want Internal", got)
	}
}

func TestAttachSession_RejectsEmptyCert(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return &cpb.ResolveResponse{
				VmHost:      "vm.local",
				AgentdPort:  9100,
				MtlsCertPem: "",
				MtlsKeyPem:  "key",
			}, nil
		},
	}
	c := startFakeCP(t, fake, 0)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("AttachSession with empty cert code = %v, want Internal", got)
	}
}

func TestEnsureAlive_FailedStatusReturnsError(t *testing.T) {
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return nil, status.Error(codes.NotFound, "no live agent")
		},
		ensureFn: func(_ *cpb.EnsureRequest) (*cpb.EnsureResponse, error) {
			return &cpb.EnsureResponse{Status: cpb.AgentStatus_FAILED}, nil
		},
	}
	c := startFakeCP(t, fake, 0)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := codeOf(err); got != codes.FailedPrecondition {
		t.Errorf("AttachSession on EnsureAlive FAILED code = %v, want FailedPrecondition", got)
	}
}

func TestEnsureAlive_TransientStatusRetriesOnce(t *testing.T) {
	// First EnsureAlive returns PROVISIONING (transient); the client must
	// retry exactly once and fail on the second non-terminal response.
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return nil, status.Error(codes.NotFound, "no live agent")
		},
		ensureFn: func(_ *cpb.EnsureRequest) (*cpb.EnsureResponse, error) {
			return &cpb.EnsureResponse{Status: cpb.AgentStatus_PROVISIONING}, nil
		},
	}
	c := startFakeCP(t, fake, 0)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if got := codeOf(err); got != codes.Unavailable {
		t.Errorf("AttachSession on persistent PROVISIONING code = %v, want Unavailable", got)
	}
	if got := fake.ensureCalls.Load(); got != 2 {
		t.Errorf("EnsureAlive calls = %d, want 2 (initial + 1 retry)", got)
	}
}

func TestEnsureAlive_TransientThenReady(t *testing.T) {
	// First EnsureAlive returns PROVISIONING, second returns READY.
	// AttachSession must accept the retry's payload.
	var ensureN atomic.Int32
	fake := &fakeControlPlane{
		resolveFn: func(_ *cpb.ResolveRequest) (*cpb.ResolveResponse, error) {
			return nil, status.Error(codes.NotFound, "no live agent")
		},
		ensureFn: func(req *cpb.EnsureRequest) (*cpb.EnsureResponse, error) {
			n := ensureN.Add(1)
			if n == 1 {
				return &cpb.EnsureResponse{Status: cpb.AgentStatus_PROVISIONING}, nil
			}
			return &cpb.EnsureResponse{
				Status:  cpb.AgentStatus_READY,
				Routing: readyResolveResponse(t, req.GetWorkspace(), req.GetAgent()),
			}, nil
		},
	}
	c := attachWithStubAgentd(t, fake, 0, false)
	att, _, err := c.AttachSession(terminal.SessionKey{Workspace: "demo", Name: "main"}, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if att == nil {
		t.Fatalf("AttachSession att = nil")
	}
}
