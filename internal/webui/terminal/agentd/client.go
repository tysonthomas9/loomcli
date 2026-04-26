// Package agentd provides a PTYSource implementation that proxies terminal
// sessions to a loom-agentd inside a persistent-agent Firecracker microVM via
// gRPC. The webui handler treats it identically to the in-process PTYManager.
//
// Phase 2 (plan-rbp.2) wires the control-plane integration: AttachSession
// resolves the persistent-agent VM through ResolveAgent / EnsureAlive,
// caches the address + short-lived mTLS cert, and produces a *tls.Config
// ready for an agentd stream. The actual stream + Terminal RPC plumbing
// lands in Phase 3 (plan-rbp.3) — until then AttachSession returns a
// placeholder Attachment whose I/O methods report Unimplemented.
package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cpb "github.com/tysonthomas9/loom-control-plane/proto/cpb"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// defaultCertTTL is the in-memory cache lifetime for a (workspace, agent)
// routing tuple. Set strictly less than the control-plane's 2-minute cert
// validity so AttachSession re-mints with margin instead of racing the
// agentd's certificate-expiry check.
const defaultCertTTL = 90 * time.Second

// Options configure a real AgentdClient. ControlPlaneEndpoint is the only
// required field; the others have sensible defaults documented inline.
type Options struct {
	// ControlPlaneEndpoint is the host:port of the loom-control-plane gRPC
	// service (e.g. "control.internal:9200"). Required.
	ControlPlaneEndpoint string

	// ControlPlaneTLS is the mTLS config used to dial the control-plane.
	// nil → the insecure transport, which is intended only for tests with
	// a bufconn-served fake control-plane.
	ControlPlaneTLS *tls.Config

	// AgentdRootCAPEM is the PEM-encoded CA cert used to verify a
	// loom-agentd's *server* cert. Required when ControlPlaneTLS is non-nil
	// (i.e. in production); tests that never actually build a tls.Config
	// can leave it empty.
	AgentdRootCAPEM []byte

	// CertTTL caps how long a cached routing entry survives. 0 → 90 s
	// default (slightly less than the control-plane's 2-minute cert
	// lifetime). Negative values are rejected by New().
	CertTTL time.Duration
}

// AgentdClient is the persistent-agent backend for the web terminal. It is
// constructed once per loomcli process and shared across all WebSocket
// connections — call sites are responsible for routing only persistent-agent
// sessions to it (see plan-rbp.5 for the dispatch factory).
type AgentdClient struct {
	cp        *controlPlaneClient
	cache     *routingCache
	caRootPEM []byte
	certTTL   time.Duration
}

// New constructs an AgentdClient with the given options. The constructor
// dials the control-plane lazily (non-blocking gRPC New), so a returned
// non-nil client is safe to use immediately; the first RPC will surface
// any connectivity issue.
func New(opts Options) (*AgentdClient, error) {
	if opts.ControlPlaneEndpoint == "" {
		return nil, errors.New("agentd: Options.ControlPlaneEndpoint is required")
	}
	if opts.CertTTL < 0 {
		return nil, fmt.Errorf("agentd: Options.CertTTL must be >= 0, got %v", opts.CertTTL)
	}

	ttl := opts.CertTTL
	if ttl == 0 {
		ttl = defaultCertTTL
	}

	cp, err := newControlPlaneClient(context.Background(), opts.ControlPlaneEndpoint, opts.ControlPlaneTLS)
	if err != nil {
		return nil, err
	}

	// Defensive copy: caller may have constructed the slice from a buffer
	// they later mutate. AgentdClient holds the bytes for the lifetime of
	// the process so we never want surprise-rewrites.
	var caCopy []byte
	if len(opts.AgentdRootCAPEM) > 0 {
		caCopy = append([]byte(nil), opts.AgentdRootCAPEM...)
	}

	return &AgentdClient{
		cp:        cp,
		cache:     newRoutingCache(ttl),
		caRootPEM: caCopy,
		certTTL:   ttl,
	}, nil
}

// NewInsecure is a test convenience that builds an AgentdClient against
// endpoint with no transport security. Equivalent to New(Options{
// ControlPlaneEndpoint: endpoint }) — kept as a separate constructor so
// production call sites cannot accidentally invoke it.
func NewInsecure(endpoint string) (*AgentdClient, error) {
	return New(Options{ControlPlaneEndpoint: endpoint})
}

// newWithControlPlane builds an AgentdClient that wraps an already-prepared
// controlPlaneClient instead of dialing one. Used exclusively by tests that
// drive the AgentdClient through a bufconn-served fake server. The constructor
// signature deliberately stays unexported because production code must always
// go through New / NewInsecure (which build the gRPC connection themselves).
func newWithControlPlane(cp *controlPlaneClient, caRootPEM []byte, certTTL time.Duration) *AgentdClient {
	if certTTL <= 0 {
		certTTL = defaultCertTTL
	}
	var caCopy []byte
	if len(caRootPEM) > 0 {
		caCopy = append([]byte(nil), caRootPEM...)
	}
	return &AgentdClient{
		cp:        cp,
		cache:     newRoutingCache(certTTL),
		caRootPEM: caCopy,
		certTTL:   certTTL,
	}
}

// Close tears down the gRPC connection to the control-plane. Returns nil if
// the client was never fully constructed.
func (c *AgentdClient) Close() error {
	if c == nil {
		return nil
	}
	return c.cp.Close()
}

// AttachSession opens or rejoins a session against the persistent agent
// identified by key. Phase 2 only goes far enough to verify connectivity:
//
//  1. Look up the routing cache. A live entry → build a *tls.Config from
//     the cached PEMs and return a placeholder Attachment marked
//     reattached=true (Phase 3 will set reattached based on the agentd's
//     expect_replay response — for now a cache hit is the only signal we
//     have that the session was previously resolved).
//  2. Cache miss → ResolveAgent. NotFound → EnsureAlive. Any other RPC
//     error is returned verbatim (callers translate as needed).
//  3. Validate the routing payload (vm_host non-empty, agentd_port > 0,
//     cert + key non-empty) before caching. Empty fields trip codes.Internal.
//  4. Build the *tls.Config from the freshly-minted PEMs and return a
//     placeholder Attachment with reattached=false.
//
// Returned tls.Configs are not stashed back on the AgentdClient — Phase 3
// will own the agentd connection / stream lifecycle and is the appropriate
// place to make that decision.
func (c *AgentdClient) AttachSession(key terminal.SessionKey, _, _ uint16, _ []string) (terminal.Attachment, bool, error) {
	if key.Workspace == "" {
		return nil, false, status.Error(codes.InvalidArgument, "agentd: SessionKey.Workspace is empty")
	}
	if key.Name == "" {
		return nil, false, status.Error(codes.InvalidArgument, "agentd: SessionKey.Name is empty")
	}

	// AttachSession's signature does not propagate a context (PTYSource is
	// kept narrow on purpose). The control-plane RPCs need one anyway, and
	// we want a hard ceiling on resolve+ensure latency so a stuck server
	// can't wedge a websocket attach. 30 s comfortably exceeds the
	// EnsureAlive 1-second backoff plus any reasonable READY wait.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if entry, ok := c.cache.Get(key.Workspace, key.Name); ok {
		tlsCfg, err := tlsConfigFromPEM(entry.certPEM, entry.keyPEM, c.caRootPEM, key.Workspace, key.Name)
		if err != nil {
			// A cached entry that cannot rebuild a tls.Config is unusable;
			// drop it and fall through to a fresh resolve.
			c.cache.Invalidate(key.Workspace, key.Name)
		} else {
			return newPhase2Attachment(tlsCfg, entry.vmHost, entry.agentdPort), true, nil
		}
	}

	routing, err := c.resolveOrEnsure(ctx, key.Workspace, key.Name)
	if err != nil {
		return nil, false, err
	}
	if err := validateRouting(routing); err != nil {
		return nil, false, err
	}

	c.cache.Put(key.Workspace, key.Name, routing.GetVmHost(), routing.GetAgentdPort(), routing.GetMtlsCertPem(), routing.GetMtlsKeyPem())

	tlsCfg, err := tlsConfigFromPEM(routing.GetMtlsCertPem(), routing.GetMtlsKeyPem(), c.caRootPEM, key.Workspace, key.Name)
	if err != nil {
		// Inconsistent: the control-plane handed us PEMs we can't parse.
		// Surface as Internal so callers don't retry blindly.
		return nil, false, status.Errorf(codes.Internal, "agentd: build tls.Config: %v", err)
	}
	return newPhase2Attachment(tlsCfg, routing.GetVmHost(), routing.GetAgentdPort()), false, nil
}

// resolveOrEnsure runs the Resolve → EnsureAlive fallback. Returns the
// routing payload from whichever call produced one.
func (c *AgentdClient) resolveOrEnsure(ctx context.Context, ws, agent string) (*cpb.ResolveResponse, error) {
	resp, err := c.cp.Resolve(ctx, ws, agent)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	// Cache miss in the control-plane → ask it to provision/warm.
	return c.cp.EnsureAlive(ctx, ws, agent)
}

// validateRouting enforces the minimum invariants AttachSession needs from
// any routing response — empty strings or non-positive ports indicate a
// control-plane bug, which we surface as codes.Internal so callers can
// distinguish "infrastructure broken" from "client misuse".
func validateRouting(r *cpb.ResolveResponse) error {
	if r == nil {
		return status.Error(codes.Internal, "agentd: nil routing response")
	}
	if r.GetVmHost() == "" {
		return status.Error(codes.Internal, "agentd: routing.vm_host is empty")
	}
	if r.GetAgentdPort() <= 0 {
		return status.Errorf(codes.Internal, "agentd: routing.agentd_port = %d, want > 0", r.GetAgentdPort())
	}
	if r.GetMtlsCertPem() == "" {
		return status.Error(codes.Internal, "agentd: routing.mtls_cert_pem is empty")
	}
	if r.GetMtlsKeyPem() == "" {
		return status.Error(codes.Internal, "agentd: routing.mtls_key_pem is empty")
	}
	return nil
}

// Detach will release the named attachment for the session.
func (c *AgentdClient) Detach(_ terminal.SessionKey, _ string) {
	// Phase 2: silent no-op. Once the bidi stream lands (plan-rbp.3) this
	// closes the per-attachment input channel and lets the agentd-side
	// session enter the grace window.
}

// Kill will terminate the persistent-agent session immediately.
func (c *AgentdClient) Kill(_ terminal.SessionKey) error {
	return status.Error(codes.Unimplemented, "agentd: Kill not implemented")
}

// HasSession reports whether a live session exists for key.
func (c *AgentdClient) HasSession(_ terminal.SessionKey) bool {
	return false
}

// AttachmentCount reports the number of live attachments for key.
func (c *AgentdClient) AttachmentCount(_ terminal.SessionKey) int {
	return 0
}

// SessionCount reports the total number of live sessions.
func (c *AgentdClient) SessionCount() int {
	return 0
}

// SessionCountFor returns the live-session count scoped to wsID.
func (c *AgentdClient) SessionCountFor(_ string) int {
	return 0
}

// MaxSessions returns the configured concurrent-session cap. Until
// the per-workspace cap lands the cap is reported as 0, signalling
// "unbounded / unknown" to callers — they should not gate on this
// value yet.
func (c *AgentdClient) MaxSessions() int {
	return 0
}

var _ terminal.PTYSource = (*AgentdClient)(nil)

// phase2Attachment is the placeholder Attachment AttachSession returns
// while Phase 3 is unimplemented. It satisfies the terminal.Attachment
// contract with the obvious zero values: a closed Output() channel, a
// generated ConnID, nil scrollback, and Unimplemented errors from any
// method that would otherwise touch a stream.
//
// The tls.Config + agentd address are stashed on the value so Phase 3
// can promote phase2Attachment to a real bidi-stream owner without a
// signature change to AttachSession.
type phase2Attachment struct {
	connID     string
	output     chan []byte
	tlsCfg     *tls.Config
	vmHost     string
	agentdPort int32
}

// connSeq is incremented monotonically for fallback ConnID generation when
// uuid.NewRandom fails (which is effectively never on Linux, but defensive
// programming prefers a deterministic suffix to a panic).
var connSeq atomic.Uint64

func newPhase2Attachment(tlsCfg *tls.Config, vmHost string, port int32) *phase2Attachment {
	out := make(chan []byte)
	close(out)

	id := uuid.NewString()
	if id == "" {
		id = fmt.Sprintf("agentd-phase2-%d", connSeq.Add(1))
	}

	return &phase2Attachment{
		connID:     id,
		output:     out,
		tlsCfg:     tlsCfg,
		vmHost:     vmHost,
		agentdPort: port,
	}
}

func (a *phase2Attachment) ConnID() string         { return a.connID }
func (a *phase2Attachment) Output() <-chan []byte  { return a.output }
func (a *phase2Attachment) WriteInput(_ []byte) (int, error) {
	return 0, status.Error(codes.Unimplemented, "agentd: WriteInput not implemented (phase 3)")
}
func (a *phase2Attachment) Scrollback() []byte { return nil }
func (a *phase2Attachment) Resize(_ string, _, _ uint16) error {
	return status.Error(codes.Unimplemented, "agentd: Resize not implemented (phase 3)")
}
func (a *phase2Attachment) ExitReason() string { return "" }

// Compile-time assertions: phase2Attachment must satisfy terminal.Attachment
// so AttachSession's return type compiles even before Phase 3 promotes it.
var _ terminal.Attachment = (*phase2Attachment)(nil)
