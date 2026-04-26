package agentd

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// codeOf extracts the gRPC code from err, or codes.OK if nil. status.Code
// already returns codes.Unknown for non-status errors, but distinguishing
// "method returned nil" from "method returned a non-status error" makes
// failure messages clearer.
func codeOf(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return codes.Unknown
}

func TestAgentdClient_ImplementsPTYSource(t *testing.T) {
	var _ terminal.PTYSource = (*AgentdClient)(nil)
	var _ terminal.PTYSource = New()
}

func TestAgentdClient_AttachSessionReturnsUnimplemented(t *testing.T) {
	c := New()
	att, reattached, err := c.AttachSession(terminal.SessionKey{Workspace: "w", Name: "s"}, 80, 24, nil)
	if att != nil {
		t.Errorf("AttachSession att = %v, want nil", att)
	}
	if reattached {
		t.Errorf("AttachSession reattached = true, want false")
	}
	if got := codeOf(err); got != codes.Unimplemented {
		t.Errorf("AttachSession code = %v, want Unimplemented", got)
	}
}

func TestAgentdClient_KillReturnsUnimplemented(t *testing.T) {
	c := New()
	err := c.Kill(terminal.SessionKey{Workspace: "w", Name: "s"})
	if got := codeOf(err); got != codes.Unimplemented {
		t.Errorf("Kill code = %v, want Unimplemented", got)
	}
	// Sanity: errors.Is against a sentinel of the same code should not match
	// — this guards against accidentally returning a sentinel that callers
	// might unwrap and mishandle.
	if errors.Is(err, status.Error(codes.Unimplemented, "x")) {
		t.Errorf("Kill returned a wrapped sentinel; want a fresh status.Error")
	}
}

func TestAgentdClient_DetachIsNoOp(t *testing.T) {
	c := New()
	// Should not panic; nothing to assert beyond that.
	c.Detach(terminal.SessionKey{Workspace: "w", Name: "s"}, "conn-1")
}

func TestAgentdClient_QueriesReturnZeroValues(t *testing.T) {
	c := New()
	key := terminal.SessionKey{Workspace: "w", Name: "s"}

	if got := c.HasSession(key); got {
		t.Errorf("HasSession = true, want false on skeleton")
	}
	if got := c.AttachmentCount(key); got != 0 {
		t.Errorf("AttachmentCount = %d, want 0", got)
	}
	if got := c.SessionCount(); got != 0 {
		t.Errorf("SessionCount = %d, want 0", got)
	}
	if got := c.SessionCountFor("w"); got != 0 {
		t.Errorf("SessionCountFor = %d, want 0", got)
	}
	if got := c.MaxSessions(); got != 0 {
		t.Errorf("MaxSessions = %d, want 0", got)
	}
}
