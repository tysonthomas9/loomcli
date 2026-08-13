package terminal

import (
	"fmt"
	"testing"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestClassifyAttachErrDaytonaSandboxGone(t *testing.T) {
	code, reason := classifyAttachErr(
		fmt.Errorf("attach Daytona terminal: %w", webuterminal.ErrDaytonaSandboxGone),
		"lead",
		"workspace-1",
	)

	if code != websocket.StatusCode(realtime.WSCloseSandboxGone) { //nolint:staticcheck // SA1019
		t.Fatalf("code = %d, want %d", code, realtime.WSCloseSandboxGone)
	}
	if reason != "sandbox no longer exists" {
		t.Fatalf("reason = %q, want %q", reason, "sandbox no longer exists")
	}
	if got := wsCloseReason(code); got != wsDisconnectReasonSandboxGone {
		t.Fatalf("disconnect reason = %q, want %q", got, wsDisconnectReasonSandboxGone)
	}
}
