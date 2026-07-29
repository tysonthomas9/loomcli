package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/local"
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
// when nothing had been touched.
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

// stubEnsureRuntime replaces the three local-runtime calls ensure-runtime makes
// so the command path runs without a desktop runtime.
func stubEnsureRuntime(t *testing.T, action local.RuntimeEnsureAction) {
	t.Helper()
	prevEnsure, prevRead, prevWait := ensureRuntimeStartedFn, readRuntimeStatusFn, waitForWorkspaceReadyFn
	t.Cleanup(func() {
		ensureRuntimeStartedFn, readRuntimeStatusFn, waitForWorkspaceReadyFn = prevEnsure, prevRead, prevWait
	})
	ensureRuntimeStartedFn = func(context.Context, string, int) (*local.RuntimeStatusSnapshot, local.RuntimeEnsureAction, error) {
		return &local.RuntimeStatusSnapshot{Healthy: true}, action, nil
	}
	// No URL recorded, so the workspace-ready wait is skipped; it is not what
	// these cases are about.
	readRuntimeStatusFn = func(context.Context, string) (*local.RuntimeStatusSnapshot, error) {
		return &local.RuntimeStatusSnapshot{Healthy: true}, nil
	}
	waitForWorkspaceReadyFn = func(context.Context, string, string) error { return nil }
}

func runEnsureRuntimeForTest(t *testing.T, initial *WorkspaceOpsStatus) *WorkspaceOpsEnsureRuntime {
	t.Helper()
	status, err := ensureRuntimeAndReport(context.Background(), "ws", initial,
		func(context.Context, string) (*WorkspaceOpsStatus, error) { return initial, nil })
	if err != nil {
		t.Fatalf("ensureRuntimeAndReport: %v", err)
	}
	if status == nil || status.EnsureRuntime == nil {
		t.Fatal("ensure-runtime produced no report")
	}
	return status.EnsureRuntime
}

// The failure this command exists to prevent, reproduced on its own action
// path: EnsureRuntimeStarted returns immediately when the runtime is already
// healthy, and the command used to answer "runtime started" anyway. That is the
// same authoritative-but-wrong report, on the most common local path, including
// the wedged case where every health check reads green.
func TestEnsureRuntime_AlreadyHealthyReportsNoAction(t *testing.T) {
	stubEnsureRuntime(t, local.RuntimeEnsureNoAction)

	er := runEnsureRuntimeForTest(t, &WorkspaceOpsStatus{OK: true})

	if er.ActionTaken {
		t.Error("action_taken must be false when the runtime was already healthy")
	}
	if er.Reason == "" {
		t.Error("a no-op must say why nothing was done")
	}
	if er.Action != string(local.RuntimeEnsureNoAction) {
		t.Errorf("action = %q, want %q", er.Action, local.RuntimeEnsureNoAction)
	}
	if out := renderForTest(t, &WorkspaceOpsStatus{EnsureRuntime: er}, false); !strings.Contains(out, "NO ACTION TAKEN") {
		t.Errorf("a no-op must not read as a start:\n%s", out)
	}
}

func TestEnsureRuntime_RealStartReportsActionThroughTheCommandPath(t *testing.T) {
	stubEnsureRuntime(t, local.RuntimeEnsureStarted)

	er := runEnsureRuntimeForTest(t, &WorkspaceOpsStatus{OK: true})

	if !er.ActionTaken {
		t.Error("action_taken must be true when a runtime was actually started")
	}
	if er.Action != string(local.RuntimeEnsureStarted) {
		t.Errorf("action = %q, want %q", er.Action, local.RuntimeEnsureStarted)
	}
	if out := renderForTest(t, &WorkspaceOpsStatus{EnsureRuntime: er}, false); !strings.Contains(out, "runtime started") {
		t.Errorf("a real start is not reported:\n%s", out)
	}
}

// A restart of a recorded-but-unhealthy runtime is an action, and a different
// one from a cold start — collapsing them loses the fact that something was
// broken enough to need restarting.
func TestEnsureRuntime_RestartIsReportedAsARestart(t *testing.T) {
	stubEnsureRuntime(t, local.RuntimeEnsureRestarted)

	er := runEnsureRuntimeForTest(t, &WorkspaceOpsStatus{OK: true})

	if !er.ActionTaken {
		t.Error("action_taken must be true for a restart")
	}
	if out := renderForTest(t, &WorkspaceOpsStatus{EnsureRuntime: er}, false); !strings.Contains(out, "runtime restarted") {
		t.Errorf("a restart is reported as a plain start:\n%s", out)
	}
}

// The skip path through the command itself, not a hand-built struct: a
// non-applicable local runtime must never reach EnsureRuntimeStarted.
func TestEnsureRuntime_SkipPathTakesNoActionAndDoesNotCallTheRuntime(t *testing.T) {
	stubEnsureRuntime(t, local.RuntimeEnsureStarted)
	called := false
	prev := ensureRuntimeStartedFn
	t.Cleanup(func() { ensureRuntimeStartedFn = prev })
	ensureRuntimeStartedFn = func(context.Context, string, int) (*local.RuntimeStatusSnapshot, local.RuntimeEnsureAction, error) {
		called = true
		return nil, local.RuntimeEnsureStarted, nil
	}

	er := runEnsureRuntimeForTest(t, &WorkspaceOpsStatus{
		OK: true,
		LocalRuntime: &WorkspaceOpsLocalRuntime{
			Applicable: false,
			Reason:     "remote issue backend active - local desktop runtime not required",
		},
	})

	if called {
		t.Error("the local runtime was touched on a path that reports no action")
	}
	if er.ActionTaken {
		t.Error("action_taken must be false on the skip path")
	}
	if !strings.Contains(er.Reason, "remote issue backend active") {
		t.Errorf("skip reason = %q, want the live status reason", er.Reason)
	}
}

// action_taken keeps its meaning and reason is still populated, so a caller
// pinned to the shipped field set is unaffected by the new action field.
func TestEnsureRuntime_JSONStaysBackwardCompatible(t *testing.T) {
	stubEnsureRuntime(t, local.RuntimeEnsureNoAction)
	er := runEnsureRuntimeForTest(t, &WorkspaceOpsStatus{OK: true})

	var decoded struct {
		EnsureRuntime *struct {
			ActionTaken bool   `json:"action_taken"`
			Action      string `json:"action"`
			Reason      string `json:"reason"`
		} `json:"ensure_runtime"`
	}
	out := renderForTest(t, &WorkspaceOpsStatus{OK: true, EnsureRuntime: er}, true)
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.EnsureRuntime == nil {
		t.Fatal("ensure_runtime absent from JSON")
	}
	if decoded.EnsureRuntime.ActionTaken {
		t.Error("action_taken must serialize false for a no-op")
	}
	if decoded.EnsureRuntime.Reason == "" || decoded.EnsureRuntime.Action == "" {
		t.Errorf("reason/action missing from JSON: %s", out)
	}
	// action_taken has no omitempty, so a no-op stays explicit rather than
	// vanishing from the payload.
	if !strings.Contains(out, `"action_taken": false`) {
		t.Errorf("action_taken must be present even when false:\n%s", out)
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
