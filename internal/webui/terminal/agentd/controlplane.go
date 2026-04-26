package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cpb "github.com/tysonthomas9/loom-control-plane/proto/cpb"
)

// controlPlaneClient owns the long-lived *grpc.ClientConn to the loom-control-
// plane plus the generated stub. Constructed once at AgentdClient setup time;
// callers issue one Resolve / EnsureAlive per session attach.
//
// The wrapper deliberately exposes a narrow surface (Resolve, EnsureAlive,
// Close) instead of leaking the *grpc.ClientConn — Phase 3 will need
// ReleaseAttach too, but until that ticket lands keeping the surface small
// makes it harder for a test to reach behind and tweak grpc internals.
type controlPlaneClient struct {
	conn   *grpc.ClientConn
	client cpb.ControlPlaneClient
}

// newControlPlaneClient dials endpoint with the supplied tls.Config (mTLS
// to the control-plane). A nil tls.Config picks the insecure transport — the
// constructor doc-comment in client.go calls this out explicitly so tests
// can pass nil without hunting for a way to disable TLS.
//
// The dial is non-blocking; the first RPC will surface any connectivity
// issue. The supplied ctx scopes only the dial itself (today this is a no-op
// since grpc.NewClient is lazy, but keeping ctx in the signature lets a
// future change adopt grpc.DialContext without churning callers).
func newControlPlaneClient(ctx context.Context, endpoint string, tlsCfg *tls.Config) (*controlPlaneClient, error) {
	if endpoint == "" {
		return nil, errors.New("agentd: control-plane endpoint must not be empty")
	}
	_ = ctx // reserved for future grpc.DialContext usage; no-op for grpc.NewClient.

	var creds credentials.TransportCredentials
	if tlsCfg == nil {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(tlsCfg)
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("agentd: dial control-plane %q: %w", endpoint, err)
	}
	return &controlPlaneClient{
		conn:   conn,
		client: cpb.NewControlPlaneClient(conn),
	}, nil
}

// newControlPlaneClientFromConn wraps an already-connected *grpc.ClientConn.
// Used by tests that dial via bufconn — the production New / NewInsecure
// path always goes through newControlPlaneClient with a real endpoint.
func newControlPlaneClientFromConn(conn *grpc.ClientConn) *controlPlaneClient {
	return &controlPlaneClient{conn: conn, client: cpb.NewControlPlaneClient(conn)}
}

// Close tears down the underlying gRPC connection. Idempotent.
func (c *controlPlaneClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Resolve issues a ResolveAgent RPC and returns the routing response on
// success. Errors are passed through verbatim so AttachSession can branch
// on codes.NotFound and call EnsureAlive.
func (c *controlPlaneClient) Resolve(ctx context.Context, ws, agent string) (*cpb.ResolveResponse, error) {
	resp, err := c.client.ResolveAgent(ctx, &cpb.ResolveRequest{Workspace: ws, Agent: agent})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// EnsureAlive issues an EnsureAlive RPC and waits for a READY response.
// Per the control-plane contract the server itself blocks until READY-or-
// fail; the client only needs to handle three response shapes:
//
//   - READY → return the embedded routing.
//   - FAILED → translate to a status error.
//   - PROVISIONING / WARMING / STATUS_UNKNOWN → treat as a transient race
//     (the server should not return these unless something raced an
//     in-flight provisioner) and retry exactly once with a 1-second backoff
//     before giving up. We do not loop unbounded here — Phase 2 keeps the
//     deadline shape simple and pushes any longer wait policy out to the
//     caller's context.
func (c *controlPlaneClient) EnsureAlive(ctx context.Context, ws, agent string) (*cpb.ResolveResponse, error) {
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.client.EnsureAlive(ctx, &cpb.EnsureRequest{Workspace: ws, Agent: agent})
		if err != nil {
			return nil, err
		}
		switch resp.GetStatus() {
		case cpb.AgentStatus_READY:
			routing := resp.GetRouting()
			if routing == nil {
				return nil, status.Errorf(codes.Internal,
					"agentd: EnsureAlive returned READY without routing for %s/%s", ws, agent)
			}
			return routing, nil
		case cpb.AgentStatus_FAILED:
			return nil, status.Errorf(codes.FailedPrecondition,
				"agentd: EnsureAlive reported FAILED for %s/%s", ws, agent)
		case cpb.AgentStatus_PROVISIONING, cpb.AgentStatus_WARMING, cpb.AgentStatus_STATUS_UNKNOWN:
			// Transient: server didn't block as long as it should have.
			// One retry is plenty — if a second non-terminal status comes
			// back the issue is structural, not a transient race, and we
			// surface that to AttachSession instead of looping forever.
			if attempt == 1 {
				return nil, status.Errorf(codes.Unavailable,
					"agentd: EnsureAlive %s/%s returned non-terminal status %s twice",
					ws, agent, resp.GetStatus())
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
		default:
			return nil, status.Errorf(codes.Internal,
				"agentd: EnsureAlive returned unknown status %d for %s/%s",
				resp.GetStatus(), ws, agent)
		}
	}
	// Unreachable: the loop above always returns. Keep a sentinel so a
	// future refactor that breaks out of the loop fails loudly instead of
	// returning (nil, nil).
	return nil, status.Errorf(codes.Internal, "agentd: EnsureAlive %s/%s exited retry loop", ws, agent)
}
