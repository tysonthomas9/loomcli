// Package agentd provides a PTYSource implementation that proxies terminal
// sessions to a loom-agentd inside a persistent-agent Firecracker microVM via
// gRPC. The webui handler treats it identically to the in-process PTYManager.
//
// This is the Phase 1 skeleton (plan-rbp.1): every terminal.PTYSource method
// returns codes.Unimplemented (or the zero value for non-error returns) so the
// rest of the wiring — DI, factory dispatch, tests — can be exercised before
// any wire calls land. Phase 2 wires control-plane (ResolveAgent + EnsureAlive)
// and Phase 3 plumbs the agentd Terminal stream.
package agentd

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// AgentdClient is the persistent-agent backend for the web terminal. It is
// constructed once per loomcli process and shared across all WebSocket
// connections — call sites are responsible for routing only persistent-agent
// sessions to it (see plan-rbp.5 for the dispatch factory).
type AgentdClient struct{}

// New returns a zero-config AgentdClient. Subsequent phases extend the
// constructor with the control-plane endpoint and connection options; the
// signature returns the concrete *AgentdClient so callers can adopt new
// configuration without an interface dance.
func New() *AgentdClient {
	return &AgentdClient{}
}

// AttachSession will open or rejoin a session against the persistent agent.
func (c *AgentdClient) AttachSession(_ terminal.SessionKey, _, _ uint16, _ []string) (terminal.Attachment, bool, error) {
	return nil, false, status.Error(codes.Unimplemented, "agentd: AttachSession not implemented")
}

// Detach will release the named attachment for the session.
func (c *AgentdClient) Detach(_ terminal.SessionKey, _ string) {
	// Phase 1: silent no-op. Once the bidi stream lands (plan-rbp.3) this
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

// MaxSessions returns the configured concurrent-session cap. Until the
// control-plane integration lands (plan-rbp.2) the cap is reported as 0,
// signalling "unbounded / unknown" to callers — they should not gate on this
// value yet.
func (c *AgentdClient) MaxSessions() int {
	return 0
}

var _ terminal.PTYSource = (*AgentdClient)(nil)
