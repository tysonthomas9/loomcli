// Package agentd provides a PTYSource implementation that proxies terminal
// sessions to a loom-agentd inside a persistent-agent Firecracker microVM via
// gRPC. The webui handler treats it identically to the in-process PTYManager.
//
// Phase 2 (plan-rbp.2) wires the control-plane integration: AttachSession
// resolves the persistent-agent VM through ResolveAgent / EnsureAlive and
// caches the address + short-lived mTLS cert. Phase 3 (plan-rbp.3) replaces
// the previous placeholder Attachment with a real bidi gRPC stream against
// the agentd's loom.agentd.v1.Terminal service — including Kill / List
// dispatch over the same routing tuple — so the WebSocket terminal handler
// can drive a persistent-agent VM end-to-end.
package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentdpb "github.com/tysonthomas9/loom-agentd/proto/agentdpb"
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

// agentdDialer is the abstraction tests use to inject a bufconn-backed
// agentdConn instead of dialing a real network. Production sets dialer to
// nil and AttachSession falls back to dialAgentd.
type agentdDialer func(ctx context.Context, vmHost string, port int32, tlsCfg *tls.Config) (*agentdConn, error)

// AgentdClient is the persistent-agent backend for the web terminal. It is
// constructed once per loomcli process and shared across all WebSocket
// connections — call sites are responsible for routing only persistent-agent
// sessions to it (see plan-rbp.5 for the dispatch factory).
type AgentdClient struct {
	cp        *controlPlaneClient
	cache     *routingCache
	caRootPEM []byte
	certTTL   time.Duration

	// dialer is non-nil only in tests; production code calls dialAgentd
	// directly. Centralizing the indirection here keeps AttachSession /
	// Kill / HasSession / AttachmentCount sharing the exact same
	// connection-bring-up path.
	dialer agentdDialer
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
	// A non-nil ControlPlaneTLS signals "production wiring is in place" —
	// in that case the caller MUST also provide AgentdRootCAPEM so we
	// can verify agentd's server cert. Without the CA bytes,
	// tlsConfigFromPEM later returns an error at AttachSession time;
	// surface it earlier so misconfiguration fails at startup rather
	// than at the first attach.
	if opts.ControlPlaneTLS != nil && len(opts.AgentdRootCAPEM) == 0 {
		return nil, errors.New("agentd: Options.AgentdRootCAPEM is required when ControlPlaneTLS is set")
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

// withDialer injects a custom agentd dialer (used by Phase 3 bufconn tests).
// Returns the receiver so test setup can chain it onto newWithControlPlane.
func (c *AgentdClient) withDialer(d agentdDialer) *AgentdClient {
	c.dialer = d
	return c
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
// identified by key. The flow is:
//
//  1. Look up the routing cache. A live entry → reuse the cached vmHost,
//     port, and PEMs. A cache miss → ResolveAgent (NotFound → EnsureAlive).
//  2. Validate the routing payload, build the *tls.Config, and dial agentd.
//  3. Open the Terminal/Attach bidi stream and exchange AttachOpen /
//     AttachReady. The reattached flag returned to callers comes from the
//     AttachReady frame — agentd is authoritative.
//
// On any error after dial the agentd connection is closed before the
// function returns so we don't leak conns on the failure path.
func (c *AgentdClient) AttachSession(key terminal.SessionKey, cols, rows uint16, argv []string) (terminal.Attachment, bool, error) {
	if key.Workspace == "" {
		return nil, false, status.Error(codes.InvalidArgument, "agentd: SessionKey.Workspace is empty")
	}
	if key.Name == "" {
		return nil, false, status.Error(codes.InvalidArgument, "agentd: SessionKey.Name is empty")
	}

	// AttachSession's signature does not propagate a context (PTYSource is
	// kept narrow on purpose). The control-plane RPCs need one anyway, and
	// we want a hard ceiling on resolve+ensure+attach latency so a stuck
	// server can't wedge a websocket attach. 30 s comfortably exceeds the
	// EnsureAlive 1-second backoff plus any reasonable READY wait.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tlsCfg, vmHost, port, expectReplay, err := c.routingTLS(ctx, key)
	if err != nil {
		return nil, false, err
	}

	conn, err := c.dialAgentd(ctx, vmHost, port, tlsCfg)
	if err != nil {
		return nil, false, err
	}

	// Capture the dialer + routing tuple onto the attachment so its recv
	// loop can transparently rebuild the stream against the same agentd on
	// a transient close (Phase 4 — plan-rbp.4.2). Reconnect deliberately
	// does NOT go back through control-plane Resolve: the cache + the
	// original tuple are sufficient until vm-host migration lands.
	att, reattached, err := newAgentdAttachment(ctx, c.dialAgentd, conn, vmHost, port, tlsCfg, key, cols, rows, expectReplay, argv)
	if err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	return att, reattached, nil
}

// routingTLS returns the tls.Config + agentd address for key, falling back
// to ResolveAgent / EnsureAlive on a cache miss. The expectReplay flag is
// derived from the cache-hit signal: a cached entry means we've previously
// seen this session, so the next attach should request the replay frame.
// On a fresh resolve we skip the replay request — there's nothing to replay
// for a brand-new session.
func (c *AgentdClient) routingTLS(ctx context.Context, key terminal.SessionKey) (cfg *tls.Config, vmHost string, port int32, expectReplay bool, err error) {
	if entry, ok := c.cache.Get(key.Workspace, key.Name); ok {
		cfg, err := tlsConfigFromPEM(entry.certPEM, entry.keyPEM, c.caRootPEM, key.Workspace, key.Name)
		if err == nil {
			return cfg, entry.vmHost, entry.agentdPort, true, nil
		}
		// A cached entry that cannot rebuild a tls.Config is unusable; drop
		// it and fall through to a fresh resolve.
		c.cache.Invalidate(key.Workspace, key.Name)
	}

	routing, err := c.resolveOrEnsure(ctx, key.Workspace, key.Name)
	if err != nil {
		return nil, "", 0, false, err
	}
	if err := validateRouting(routing); err != nil {
		return nil, "", 0, false, err
	}

	c.cache.Put(key.Workspace, key.Name, routing.GetVmHost(), routing.GetAgentdPort(), routing.GetMtlsCertPem(), routing.GetMtlsKeyPem())

	cfg, err = tlsConfigFromPEM(routing.GetMtlsCertPem(), routing.GetMtlsKeyPem(), c.caRootPEM, key.Workspace, key.Name)
	if err != nil {
		// Inconsistent: the control-plane handed us PEMs we can't parse.
		// Surface as Internal so callers don't retry blindly.
		return nil, "", 0, false, status.Errorf(codes.Internal, "agentd: build tls.Config: %v", err)
	}
	return cfg, routing.GetVmHost(), routing.GetAgentdPort(), false, nil
}

// dialAgentd is the dialer-respecting wrapper used by every code path that
// needs a per-call agentdConn. Tests inject c.dialer to swap in a bufconn
// connection; production leaves it nil and goes through the real grpc.NewClient.
func (c *AgentdClient) dialAgentd(ctx context.Context, vmHost string, port int32, tlsCfg *tls.Config) (*agentdConn, error) {
	if c.dialer != nil {
		return c.dialer(ctx, vmHost, port, tlsCfg)
	}
	return dialAgentd(ctx, vmHost, port, tlsCfg)
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

// Detach is a documented no-op. The agentd-side detach-with-grace is
// triggered by closing the Terminal/Attach stream, which the attachment
// owns and handles in its close path. Phase 3 keeps Detach in the
// PTYSource interface as a no-op so the local PTYManager and the agentd
// backend remain interchangeable from the WS handler's perspective.
func (c *AgentdClient) Detach(_ terminal.SessionKey, _ string) {
	// Intentionally empty: see doc comment.
}

// Kill terminates the persistent-agent session immediately by issuing the
// Terminal/Kill RPC against the agentd that owns it. Routing is taken from
// the cache; a cache miss falls through to a single ResolveAgent. NotFound
// at any layer (cache, control-plane, agentd) is treated as "already gone"
// and returns nil — Kill is documented as idempotent in both the proto and
// the PTYSource interface.
func (c *AgentdClient) Kill(key terminal.SessionKey) error {
	if key.Workspace == "" {
		return status.Error(codes.InvalidArgument, "agentd: SessionKey.Workspace is empty")
	}
	if key.Name == "" {
		return status.Error(codes.InvalidArgument, "agentd: SessionKey.Name is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tlsCfg, vmHost, port, _, err := c.routingTLS(ctx, key)
	if err != nil {
		// NotFound at the routing layer means the session isn't known to
		// the control plane — equivalent to "already dead" from a Kill
		// caller's standpoint.
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	}

	conn, err := c.dialAgentd(ctx, vmHost, port, tlsCfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.client.Kill(ctx, &agentdpb.KillRequest{Session: key.Name, Force: true})
	if err != nil && status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

// HasSession reports whether a live session exists for key by issuing a
// Terminal/List RPC against the cached agentd. A cache miss returns false:
// without prior routing info we have no agentd to ask, and the WS handler
// should treat the session as "not currently mapped to this loomcli".
func (c *AgentdClient) HasSession(key terminal.SessionKey) bool {
	infos, err := c.listSessions(key)
	if err != nil {
		return false
	}
	for _, info := range infos {
		if info.GetSession() == key.Name {
			return true
		}
	}
	return false
}

// AttachmentCount reports the number of live attachments for key as
// reported by Terminal/List. Returns 0 on any error or for unknown
// sessions — same fallback as HasSession.
func (c *AgentdClient) AttachmentCount(key terminal.SessionKey) int {
	infos, err := c.listSessions(key)
	if err != nil {
		return 0
	}
	for _, info := range infos {
		if info.GetSession() == key.Name {
			return int(info.GetAttachedCount())
		}
	}
	return 0
}

// listSessions issues a Terminal/List RPC against the agentd that owns key.
// Routing must already be cached; a cache miss returns (nil, nil) so the
// caller can treat it as "no live sessions" without distinguishing it from
// an empty-list response. (HasSession / AttachmentCount must not block on
// a fresh resolve — they're called on the hot path while serving WS frames
// and a control-plane round trip would defeat the point of the cache.)
func (c *AgentdClient) listSessions(key terminal.SessionKey) ([]*agentdpb.SessionInfo, error) {
	entry, ok := c.cache.Get(key.Workspace, key.Name)
	if !ok {
		return nil, nil
	}
	tlsCfg, err := tlsConfigFromPEM(entry.certPEM, entry.keyPEM, c.caRootPEM, key.Workspace, key.Name)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := c.dialAgentd(ctx, entry.vmHost, entry.agentdPort, tlsCfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	resp, err := conn.client.List(ctx, &agentdpb.ListRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetSessions(), nil
}

// SessionCount returns 0 — agentd-backed sessions are tracked per-VM, so
// the loomcli process has no global view to report. Callers that need
// per-workspace caps should use SessionCountFor (also currently 0).
func (c *AgentdClient) SessionCount() int {
	return 0
}

// SessionCountFor returns 0 — see SessionCount. The per-workspace cap
// will be enforced in plan-rbp.5 where the dispatch factory aggregates
// across local + agentd backends.
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
