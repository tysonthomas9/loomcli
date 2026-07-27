package workspace

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func renderForTest(t *testing.T, status *WorkspaceOpsStatus, asJSON bool) string {
	t.Helper()
	prev := workspaceOpsJSON
	workspaceOpsJSON = asJSON
	t.Cleanup(func() { workspaceOpsJSON = prev })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderWorkspaceOpsStatus(cmd, status); err != nil {
		t.Fatalf("renderWorkspaceOpsStatus: %v", err)
	}
	return buf.String()
}

// ensure-runtime returns early whenever the local desktop runtime is not
// applicable, which is always the case in cloud mode. Its output was then
// identical to `workspace ops status`, so ok=true read as "runtime ensured"
// when nothing had been touched (DOGFOOD-46).
func TestEnsureRuntime_SkipIsVisibleInText(t *testing.T) {
	status := &WorkspaceOpsStatus{
		OK: true,
		EnsureRuntime: &WorkspaceOpsEnsureRuntime{
			ActionTaken: false,
			Reason:      "remote issue backend active - local desktop runtime not required",
			Scope:       ensureRuntimeScopeNote,
		},
	}

	out := renderForTest(t, status, false)

	if !strings.Contains(out, "NO ACTION TAKEN") {
		t.Errorf("skip is not stated in the output:\n%s", out)
	}
	if !strings.Contains(out, "remote issue backend active") {
		t.Errorf("skip reason is missing:\n%s", out)
	}
	// The scope matters as much as the skip: readers reached for this command
	// to revive a wedged supervisor, which it never touches.
	if !strings.Contains(out, "does not start or repair the agent supervisor") {
		t.Errorf("scope note is missing:\n%s", out)
	}
}

// A machine reader must be able to tell the two apart too - the original
// complaint came from an agent trusting ok=true.
func TestEnsureRuntime_SkipIsVisibleInJSON(t *testing.T) {
	status := &WorkspaceOpsStatus{
		OK: true,
		EnsureRuntime: &WorkspaceOpsEnsureRuntime{
			ActionTaken: false,
			Reason:      "remote issue backend active",
			Scope:       ensureRuntimeScopeNote,
		},
	}

	var decoded struct {
		OK            bool `json:"ok"`
		EnsureRuntime *struct {
			ActionTaken bool   `json:"action_taken"`
			Reason      string `json:"reason"`
		} `json:"ensure_runtime"`
	}
	if err := json.Unmarshal([]byte(renderForTest(t, status, true)), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.EnsureRuntime == nil {
		t.Fatal("ensure_runtime absent from JSON; a caller cannot distinguish a skip from a start")
	}
	if decoded.EnsureRuntime.ActionTaken {
		t.Error("action_taken should be false for a skip")
	}
	if decoded.EnsureRuntime.Reason == "" {
		t.Error("reason should be populated for a skip")
	}
}

func TestEnsureRuntime_ActionTakenIsReported(t *testing.T) {
	status := &WorkspaceOpsStatus{
		OK:            true,
		EnsureRuntime: &WorkspaceOpsEnsureRuntime{ActionTaken: true, Scope: ensureRuntimeScopeNote},
	}

	out := renderForTest(t, status, false)

	if !strings.Contains(out, "runtime started") {
		t.Errorf("a real start is not reported:\n%s", out)
	}
	if strings.Contains(out, "NO ACTION TAKEN") {
		t.Errorf("a real start must not read as a skip:\n%s", out)
	}
}

// `workspace ops status` has no EnsureRuntime field, and must not grow one -
// otherwise every status call starts claiming something about ensure-runtime.
func TestStatusOutputHasNoEnsureRuntimeSection(t *testing.T) {
	out := renderForTest(t, &WorkspaceOpsStatus{OK: true}, false)

	if strings.Contains(out, "Ensure:") {
		t.Errorf("plain status should not mention ensure-runtime:\n%s", out)
	}
}
