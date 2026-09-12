package agentdef

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// withAgentStore points the desired-state commands at a memstore and returns
// it, so a refusal can be checked against "no write happened".
func withAgentStore(t *testing.T) store.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	original := agentWithActiveWorkspace
	agentWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, "WS")
	}
	t.Cleanup(func() { agentWithActiveWorkspace = original })
	return st
}

func mustCreateAgent(t *testing.T, st store.Store, name string, auto bool) {
	t.Helper()
	if _, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         name,
		RoleName:     "task",
		Auto:         auto,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

// --auto now defaults to true: omitting it used to register auto=false and
// still get supervision, so anything but a true default would silently disable
// every agent created without the flag.
func TestAgentAddAutoDefaultsToTrue(t *testing.T) {
	flag := agentAddCmd.Flags().Lookup("auto")
	if flag == nil {
		t.Fatal("agentdef add is missing --auto")
	}
	if flag.DefValue != "true" {
		t.Errorf("--auto default = %q, want \"true\"", flag.DefValue)
	}
}

func TestAgentCreateFromFlags_AutoFollowsFlag(t *testing.T) {
	resetHookFlags(t)
	agentAddRole = "task"
	t.Cleanup(func() { agentAddRole = "" })

	agentAddAuto = true
	t.Cleanup(func() { agentAddAuto = true })
	create, err := agentCreateFromFlags("WS", "worker-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !create.Auto {
		t.Error("create.Auto = false, want true when --auto is set")
	}

	agentAddAuto = false
	create, err = agentCreateFromFlags("WS", "worker-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if create.Auto {
		t.Error("create.Auto = true, want false when --auto=false")
	}
}

func TestAgentUpdateIdentityPatch_Auto(t *testing.T) {
	t.Cleanup(func() { agentUpdateAuto = true })

	agentUpdateAuto = false
	patch, touched, err := agentUpdateIdentityPatch(changedSet("auto"))
	if err != nil || !touched {
		t.Fatalf("--auto=false must touch the patch; touched=%v err=%v", touched, err)
	}
	if patch.Auto == nil || *patch.Auto {
		t.Fatalf("patch.Auto = %v, want an explicit false", patch.Auto)
	}
	if patch.Parent != nil || patch.RoleName != nil || patch.Mode != nil {
		t.Errorf("--auto must not touch the other identity fields; patch = %+v", patch)
	}

	agentUpdateAuto = true
	patch, _, err = agentUpdateIdentityPatch(changedSet("auto"))
	if err != nil || patch.Auto == nil || !*patch.Auto {
		t.Fatalf("--auto must set an explicit true; patch = %+v err=%v", patch, err)
	}

	patch, touched, err = agentUpdateIdentityPatch(changedSet())
	if err != nil || touched || patch.Auto != nil {
		t.Fatalf("no --auto must leave Auto nil; patch = %+v touched=%v err=%v", patch, touched, err)
	}
}

// The client-side refusal exists so the operator learns the reason now rather
// than from a queued command that fails asynchronously in the daemon.
func TestAgentStartRefusesDisabledAgent(t *testing.T) {
	st := withAgentStore(t)
	mustCreateAgent(t, st, "ci-verifier", false)

	err := runAgentStart(nil, []string{"ci-verifier"})
	if err == nil {
		t.Fatal("expected start of a disabled agent to be refused")
	}
	if !strings.Contains(err.Error(), "auto: false") ||
		!strings.Contains(err.Error(), "loom agentdef update ci-verifier --auto") {
		t.Errorf("error should explain the refusal and the remedy, got %q", err)
	}

	a, getErr := st.Agents().Get(context.Background(), "WS", "ci-verifier")
	if getErr != nil {
		t.Fatalf("get agent: %v", getErr)
	}
	if a.DesiredState == domain.AgentDesiredRunning {
		t.Error("a refused start must not write desired_state=running")
	}
}

func TestAgentStartAllowsEnabledAgent(t *testing.T) {
	st := withAgentStore(t)
	mustCreateAgent(t, st, "worker", true)

	if err := runAgentStart(nil, []string{"worker"}); err != nil {
		t.Fatalf("start of an enabled agent failed: %v", err)
	}
	a, err := st.Agents().Get(context.Background(), "WS", "worker")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if a.DesiredState != domain.AgentDesiredRunning {
		t.Errorf("desired_state = %q, want running", a.DesiredState)
	}
}

// Stopping a disabled agent must still work: it is idempotent, and parking a
// still-running agent is exactly what you do while disabling it.
func TestAgentStopAllowsDisabledAgent(t *testing.T) {
	st := withAgentStore(t)
	mustCreateAgent(t, st, "ci-verifier", false)

	if err := runAgentStop(nil, []string{"ci-verifier"}); err != nil {
		t.Fatalf("stop of a disabled agent failed: %v", err)
	}
	a, err := st.Agents().Get(context.Background(), "WS", "ci-verifier")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if a.DesiredState != domain.AgentDesiredStopped {
		t.Errorf("desired_state = %q, want stopped", a.DesiredState)
	}
}

func TestAgentUpdateExposesAutoFlag(t *testing.T) {
	flag := agentUpdateCmd.Flags().Lookup("auto")
	if flag == nil {
		t.Fatal("agentdef update is missing --auto, so the switch is only settable by a raw fleet-db PATCH")
	}
	if !strings.Contains(agentUpdateCmd.Long, "--auto") {
		t.Error("agentdef update help does not document --auto")
	}
}
