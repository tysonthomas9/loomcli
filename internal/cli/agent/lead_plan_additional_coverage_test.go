package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLeadAssignmentDeliveredNoopBranches(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if err := markLeadAssignmentDelivered(ctx, nil, "WS", nil); err != nil {
		t.Fatalf("nil inputs returned error: %v", err)
	}
	if err := markLeadAssignmentDelivered(ctx, st, "WS", nil); err != nil {
		t.Fatalf("nil assignment returned error: %v", err)
	}
	if err := markLeadAssignmentDelivered(ctx, st, "WS", &epicrunner.LeadAssignmentContext{}); err != nil {
		t.Fatalf("blank assignment returned error: %v", err)
	}
	if err := markLeadAssignmentDelivered(ctx, st, "WS", &epicrunner.LeadAssignmentContext{
		OrchestratorSessionID: "missing",
		AssignmentVersion:     "v1",
	}); err != nil {
		t.Fatalf("missing session should be ignored, got %v", err)
	}
}

func TestLeadSessionActorAndRegistrationNoopBranches(t *testing.T) {
	t.Setenv("USER", "")
	if got := leadSessionActor(); got != "unknown" {
		t.Fatalf("leadSessionActor with empty USER = %q, want unknown", got)
	}

	called := false
	leadSessionRegistration{finalize: func() { called = true }}.Finalize()
	if !called {
		t.Fatal("Finalize did not invoke callback")
	}
	leadSessionRegistration{}.Finalize()
}

func TestAdoptOrCreateSessionInheritedUpdatesPrompt(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{AgentName: "nova", Backend: "codex", Prompt: "old"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Setenv("LOOM_SESSION_ID", sess.SessionID())

	got := adoptOrCreateSession("nova", "EPIC-1", "new prompt", "planning")
	if got != nil {
		t.Fatalf("adoptOrCreateSession inherited = %+v, want nil", got)
	}
	data, err := os.ReadFile(filepath.Join(runtimeDir, "sessions", sess.SessionID(), "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if string(data) != "new prompt" {
		t.Fatalf("prompt after adopt = %q", data)
	}
	finalizeAgentSession(nil, runtimeDir, "", nil)
}

func TestCreateLeadSessionStoresFallbackActor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Setenv(envAgentTerminalID, " ")
	t.Setenv("USER", "")
	realHandle := &bootstrap.StoreHandle{Store: st}
	if err := createLeadSession(ctx, realHandle, "WS", "lead-session-2", "lead-agent", "/tmp/work"); err != nil {
		t.Fatalf("createLeadSession: %v", err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session-2")
	if err != nil {
		t.Fatalf("get lead session: %v", err)
	}
	if session.Kind != domain.AgentSessionKindOrchestration || session.Metadata["actor"] != "unknown" {
		t.Fatalf("session = %+v", session)
	}
}

func TestMarkLeadAssignmentDeliveredSuccess(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "orch-1",
		AgentID:      "lead",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"existing": "kept"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	err := markLeadAssignmentDelivered(ctx, st, "WS", &epicrunner.LeadAssignmentContext{
		OrchestratorSessionID: "orch-1",
		AssignmentVersion:     "v3",
		EpicID:                "EPIC-7",
	})
	if err != nil {
		t.Fatalf("markLeadAssignmentDelivered: %v", err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "orch-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata["existing"] != "kept" ||
		session.Metadata["lead_assignment_delivered_version"] != "v3" ||
		session.Metadata["lead_assignment_delivered_epic"] != "EPIC-7" {
		t.Fatalf("metadata = %#v", session.Metadata)
	}
}

func TestLeadSessionEnvResolversAndFinalizer(t *testing.T) {
	t.Setenv(envOrchestratorSessionID, " existing-session ")
	if got := resolveLeadOrchestratorSessionID(); got != "existing-session" {
		t.Fatalf("resolveLeadOrchestratorSessionID env = %q", got)
	}
	t.Setenv(envAgentName, " nova ")
	if got := resolveLeadAgentID(); got != "nova" {
		t.Fatalf("resolveLeadAgentID env = %q", got)
	}

	activateLeadSessionEnv("sid-1")
	if got := os.Getenv(envOrchestratorSessionID); got != "sid-1" {
		t.Fatalf("orchestrator env = %q", got)
	}

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sid-1",
		AgentID:      "lead",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	handle := &bootstrap.StoreHandle{Store: st}
	if (leadSessionRegistration{}).Store() != nil {
		t.Fatal("empty registration returned a store")
	}
	if got := (leadSessionRegistration{handle: handle}).Store(); got != st {
		t.Fatalf("registration store = %#v, want memstore", got)
	}

	stopHB := make(chan struct{})
	var wg sync.WaitGroup
	finalize := leadSessionFinalizer(handle, "WS", "sid-1", stopHB, &wg)
	finalize()
	session, err := st.AgentSessions().Get(ctx, "WS", "sid-1")
	if err != nil {
		t.Fatalf("get finalized session: %v", err)
	}
	if session.Status != domain.AgentSessionCompleted || session.FinishedAt == nil {
		t.Fatalf("finalized session = %+v", session)
	}
}

func TestLeadHeartbeatStopsBeforeTick(t *testing.T) {
	var wg sync.WaitGroup
	stopHB := make(chan struct{})
	close(stopHB)
	wg.Add(1)
	heartbeatLeadSession(&bootstrap.StoreHandle{Store: memstore.New()}, "WS", "sid", stopHB, &wg)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeatLeadSession did not stop promptly")
	}
}
