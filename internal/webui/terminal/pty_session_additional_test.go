package terminal

import (
	"testing"

	"github.com/creack/pty"
)

func TestLocalAttachmentResizeAndExitReasonBranches(t *testing.T) {
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptm.Close()
	defer pts.Close()

	att := &localAttachment{pty: ptm}
	if err := att.Resize("conn-1", 90, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := att.ExitReason(); got != "" {
		t.Fatalf("nil session ExitReason = %q, want empty", got)
	}

	session := newPtySession(SessionKey{Workspace: "WS", Name: "term"}, ptm, nil)
	session.closeReason.Store(ExitReasonKilled)
	att.session = session
	if got := att.ExitReason(); got != ExitReasonKilled {
		t.Fatalf("ExitReason = %q, want %q", got, ExitReasonKilled)
	}
}
