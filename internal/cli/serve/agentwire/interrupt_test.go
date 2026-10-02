package agentwire

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestServeArchiveCancelledInterrupts: the service serve wires stops a
// running turn on Archive(cancelled) through the session's own Interrupt.
func TestServeArchiveCancelledInterrupts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wt, err := agentworktree.New(filepath.Join(dir, "worktrees"), agentworktree.TargetLocal, gitrunner.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	fh := fake.New()
	ref, err := fh.Open(ctx, loomharness.OpenSpec{Key: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	fh.Script("a1", fake.Turn{Steps: []fake.Step{{Ask: "k1"}}})
	sess := fh.Session(ref)
	if err := sess.Prompt(ctx, loomharness.Input{Key: "in1", Text: "work"}); err != nil {
		t.Fatal(err)
	}
	turn := "in1"
	if err := st.InsertAgent(ctx, loomstore.Agent{AgentID: "a1", WorkspaceID: "ws", Name: "a1", ProfileKey: "a1",
		Preset: "lead", PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive",
		SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u",
		CreateRequestID: "r1", Repo: "/repo", Harness: "opencode", State: loomagent.StateActive,
		HarnessSessionID: &ref.NativeID, HarnessSessionRoot: &ref.Root, RunningTurnID: &turn}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "a1", Harness: "opencode",
		NativeRoot: ref.Root, NativeID: ref.NativeID}); err != nil {
		t.Fatal(err)
	}
	svc := loomagent.New(serviceConfig(st, "ws", wt, nil, map[string]loomharness.Harness{"opencode": fh}))

	if err := svc.Archive(ctx, loomagent.ArchiveRequest{Envelope: loomagent.Envelope{RequestID: "ar1"},
		AgentID: "a1", Reason: loomagent.ArchiveCancelled}); err != nil {
		t.Fatal(err)
	}
	s, err := sess.Status(ctx)
	if err != nil || s.Running || !s.LastTurnInterrupt {
		t.Fatalf("status after Archive(cancelled) = %+v, %v; want the turn interrupted", s, err)
	}
}
