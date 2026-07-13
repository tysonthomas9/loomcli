package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	agentdpb "github.com/tysonthomas9/loom-agentd/proto/agentdpb"
)

// agentdConn owns a single mTLS gRPC connection to a loom-agentd inside a
// persistent-agent VM, plus the generated Terminal stub bound to that
// connection. One agentdConn is created per AttachSession in Phase 3 — the
// connection is owned by the returned attachment, which closes it on
// teardown. Phase 3 deliberately does not pool agentd connections; we'll
// revisit pooling once we have data on real connection-churn cost.
type agentdConn struct {
	conn   *grpc.ClientConn
	client agentdpb.TerminalClient
}

// agentdKeepalive is the gRPC client keepalive policy applied to every
// agentd dial. The 30 s ping cadence matches the heartbeat path; the 10 s
// timeout is well under the 15 s liveness budget so a half-open NAT entry
// is recycled before the control plane considers the agent dead.
var agentdKeepalive = keepalive.ClientParameters{
	Time:                30 * time.Second,
	Timeout:             10 * time.Second,
	PermitWithoutStream: false,
}

// dialAgentd opens an mTLS gRPC connection to vmHost:port. The supplied ctx
// is reserved for future grpc.DialContext-style use; grpc.NewClient is lazy
// today so the function does not block on TCP/TLS — the first RPC will
// surface any connectivity issue.
//
// tlsCfg must be non-nil and configured for mutual auth (see tlsConfigFromPEM).
// The returned agentdConn owns conn; the caller must invoke Close() exactly
// once.
func dialAgentd(ctx context.Context, vmHost string, port int32, tlsCfg *tls.Config) (*agentdConn, error) {
	if vmHost == "" {
		return nil, errors.New("agentd: dialAgentd: vmHost is empty")
	}
	if port <= 0 {
		return nil, fmt.Errorf("agentd: dialAgentd: port = %d, want > 0", port)
	}
	if tlsCfg == nil {
		return nil, errors.New("agentd: dialAgentd: tlsCfg is nil")
	}
	_ = ctx // reserved for future grpc.DialContext use; no-op for grpc.NewClient.

	target := net.JoinHostPort(vmHost, strconv.Itoa(int(port)))
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(agentdKeepalive),
	)
	if err != nil {
		return nil, fmt.Errorf("agentd: dial %q: %w", target, err)
	}

	return &agentdConn{
		conn:   conn,
		client: agentdpb.NewTerminalClient(conn),
	}, nil
}

// dialAgentdFromConn wraps an already-connected *grpc.ClientConn. Used
// exclusively by tests that drive the AgentdClient through a bufconn-served
// fake agentd; production code goes through dialAgentd with a real endpoint.
func dialAgentdFromConn(conn *grpc.ClientConn) *agentdConn {
	return &agentdConn{conn: conn, client: agentdpb.NewTerminalClient(conn)}
}

// Close tears down the underlying gRPC connection. Idempotent: a nil receiver
// or a nil conn returns nil.
func (a *agentdConn) Close() error {
	if a == nil || a.conn == nil {
		return nil
	}
	return a.conn.Close()
}
