package agentd

import (
	"crypto/tls"
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

// newTestClient builds an insecure AgentdClient pointed at a placeholder
// address. It never issues an RPC unless the test explicitly drives one
// through AttachSession with a fake server (see controlplane_test.go), so
// the bogus endpoint is fine for the local-only tests in this file.
func newTestClient(t *testing.T) *AgentdClient {
	t.Helper()
	c, err := NewInsecure("test:0")
	if err != nil {
		t.Fatalf("NewInsecure: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

func TestAgentdClient_ImplementsPTYSource(t *testing.T) {
	var _ terminal.PTYSource = (*AgentdClient)(nil)
	c := newTestClient(t)
	var _ terminal.PTYSource = c
}

func TestAgentdClient_New_RequiresEndpoint(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatalf("New with empty endpoint = nil, want error")
	}
}

func TestAgentdClient_New_RejectsNegativeTTL(t *testing.T) {
	if _, err := New(Options{ControlPlaneEndpoint: "x:1", CertTTL: -1}); err == nil {
		t.Fatalf("New with negative CertTTL = nil, want error")
	}
}

func TestAgentdClient_KillReturnsUnimplemented(t *testing.T) {
	c := newTestClient(t)
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
	c := newTestClient(t)
	// Should not panic; nothing to assert beyond that.
	c.Detach(terminal.SessionKey{Workspace: "w", Name: "s"}, "conn-1")
}

func TestAgentdClient_QueriesReturnZeroValues(t *testing.T) {
	c := newTestClient(t)
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

func TestAgentdClient_AttachSession_RejectsEmptyKey(t *testing.T) {
	c := newTestClient(t)
	_, _, err := c.AttachSession(terminal.SessionKey{Workspace: "", Name: "s"}, 80, 24, nil)
	if got := codeOf(err); got != codes.InvalidArgument {
		t.Errorf("AttachSession empty workspace code = %v, want InvalidArgument", got)
	}
	_, _, err = c.AttachSession(terminal.SessionKey{Workspace: "w", Name: ""}, 80, 24, nil)
	if got := codeOf(err); got != codes.InvalidArgument {
		t.Errorf("AttachSession empty name code = %v, want InvalidArgument", got)
	}
}

func TestPhase2Attachment_Methods(t *testing.T) {
	att := newPhase2Attachment(&tls.Config{MinVersion: tls.VersionTLS13}, "vm:1234", 9100)
	if att.ConnID() == "" {
		t.Errorf("ConnID = \"\", want non-empty")
	}
	out := att.Output()
	if out == nil {
		t.Fatalf("Output() returned nil channel")
	}
	// Output is a closed channel: a receive must not block and must report
	// closed-on-second-read with !ok.
	select {
	case _, ok := <-out:
		if ok {
			t.Errorf("Output() yielded a value before being closed")
		}
	default:
		t.Errorf("Output() blocked; want closed channel")
	}
	if got := att.Scrollback(); got != nil {
		t.Errorf("Scrollback = %v, want nil", got)
	}
	if got := att.ExitReason(); got != "" {
		t.Errorf("ExitReason = %q, want empty", got)
	}
	if _, err := att.WriteInput([]byte("x")); codeOf(err) != codes.Unimplemented {
		t.Errorf("WriteInput code = %v, want Unimplemented", codeOf(err))
	}
	if err := att.Resize("any", 80, 24); codeOf(err) != codes.Unimplemented {
		t.Errorf("Resize code = %v, want Unimplemented", codeOf(err))
	}
}

func TestPhase2Attachment_ConnIDsAreUnique(t *testing.T) {
	a := newPhase2Attachment(nil, "vm", 1)
	b := newPhase2Attachment(nil, "vm", 1)
	if a.ConnID() == b.ConnID() {
		t.Errorf("two phase2Attachments shared ConnID %q", a.ConnID())
	}
}
