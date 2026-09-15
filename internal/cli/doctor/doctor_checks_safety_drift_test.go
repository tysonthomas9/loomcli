package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/agent/lead"
)

// stubLeadSuppression swaps the suppression probe for the duration of a test.
// The real probe reads the workspace's lead role out of fleet-db; the check's
// verdicts are what this file is about, not that lookup.
func stubLeadSuppression(t *testing.T, reason lead.SuppressionReason, suppressed bool) {
	t.Helper()
	orig := leadPersonaSuppression
	leadPersonaSuppression = func(context.Context) (lead.SuppressionReason, bool) { return reason, suppressed }
	t.Cleanup(func() { leadPersonaSuppression = orig })
}

// dedicatedLeadWorkdir points LOOM_LEAD_WORKDIR at a fresh temp directory, so
// resolveLeadWorkdir reports it as dedicated without needing a workspace.
func dedicatedLeadWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_LEAD_WORKDIR", dir)
	return dir
}

func writeLeadAgentsFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
}

// TestCheckLeadSafetyDriftSkipsUnsuppressedLead is acceptance criterion 5's
// second half. A lead whose persona is on argv renders the safety block every
// run, so there is nothing that can go stale — the check must SKIP (zero
// CheckResult, which runDoctor drops) rather than report a pass it cannot back.
func TestCheckLeadSafetyDriftSkipsUnsuppressedLead(t *testing.T) {
	stubLeadSuppression(t, "", false)

	got := checkLeadSafetyDrift()
	if got != (CheckResult{}) {
		t.Fatalf("a non-suppressed lead produced a result instead of skipping: %+v", got)
	}
}

// TestCheckLeadSafetyDriftPasses is criterion 5 on a healthy ambient file.
func TestCheckLeadSafetyDriftPasses(t *testing.T) {
	if backend := cli.GetBackendName(); backend != "codex" {
		t.Skipf("test assumes the codex ambient file; active backend is %q", backend)
	}
	stubLeadSuppression(t, lead.SuppressedByBuiltinNone, true)
	dir := dedicatedLeadWorkdir(t)
	writeLeadAgentsFile(t, dir, "# Lead\n\n"+agent.LeadSafetyPrompt()+"\n")

	got := checkLeadSafetyDrift()
	if got.Name != "lead_safety_drift" || got.Status != StatusPass {
		t.Fatalf("current AGENTS.md did not pass: %+v", got)
	}
}

// TestCheckLeadSafetyDriftFails is criterion 5 on a stale one: doctor must
// reach the same verdict `loom lead` would, and carry the refusal text.
func TestCheckLeadSafetyDriftFails(t *testing.T) {
	if backend := cli.GetBackendName(); backend != "codex" {
		t.Skipf("test assumes the codex ambient file; active backend is %q", backend)
	}
	stubLeadSuppression(t, lead.SuppressedByProfileSource, true)
	dir := dedicatedLeadWorkdir(t)
	writeLeadAgentsFile(t, dir, "# Lead\n\nNo guardrails here.\n")

	got := checkLeadSafetyDrift()
	if got.Name != "lead_safety_drift" || got.Status != StatusFail {
		t.Fatalf("stale AGENTS.md did not fail: %+v", got)
	}
	for _, want := range []string{"multi-agent safety block", "loom lead --print-prompt"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("failure detail does not mention %q:\n%s", want, got.Detail)
		}
	}
}

// TestCheckLeadSafetyDriftIsRegistered pins the check into the doctor run: an
// unregistered check reports nothing no matter how right it is.
func TestCheckLeadSafetyDriftIsRegistered(t *testing.T) {
	stubLeadSuppression(t, lead.SuppressedByBuiltinNone, true)
	dir := dedicatedLeadWorkdir(t)
	writeLeadAgentsFile(t, dir, "# Lead\n\n"+agent.LeadSafetyPrompt()+"\n")

	for _, check := range collectDoctorChecks(nil) {
		if check().Name == "lead_safety_drift" {
			return
		}
	}
	t.Fatal("lead_safety_drift is not in collectDoctorChecks")
}
