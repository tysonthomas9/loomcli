package prreview

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// fakeTerminalService implements just the tab-listing/deletion surface the
// backend migration touches; every other TerminalService method panics via
// the embedded nil interface, which is the desired "not expected here" signal.
type fakeTerminalService struct {
	service.TerminalService

	tabs        []tabmeta.TabMetadata
	listErr     error
	deleted     []string
	deleteErr   error
	listCalled  bool
	deleteCalls int
}

func (f *fakeTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	f.listCalled = true
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]tabmeta.TabMetadata(nil), f.tabs...), nil
}

func (f *fakeTerminalService) DeleteTab(_ context.Context, _, session string) error {
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, session)
	return nil
}

type backendTestEnv struct {
	store  store.Store
	module *Module
	term   *fakeTerminalService
}

func newBackendTestEnv(t *testing.T, workspaceBackend string) *backendTestEnv {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: prReviewTestWorkspace, Name: "Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if workspaceBackend != "" {
		if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{
			WorkspaceKey: prReviewTestWorkspace,
			AgentBackend: workspaceBackend,
		}); err != nil {
			t.Fatalf("upsert daemon profile: %v", err)
		}
	}
	term := &fakeTerminalService{}
	return &backendTestEnv{
		store:  st,
		module: &Module{store: st, terminalSvc: term},
		term:   term,
	}
}

func (e *backendTestEnv) seedReviewer(t *testing.T, agentName, backend string) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.store.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: prReviewTestWorkspace, Name: "lead"}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create role: %v", err)
	}
	if _, err := e.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         agentName,
		RoleName:     "lead",
		Backend:      backend,
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create reviewer agent: %v", err)
	}
}

func (e *backendTestEnv) agentBackend(t *testing.T, agentName string) string {
	t.Helper()
	agent, err := e.store.Agents().Get(context.Background(), prReviewTestWorkspace, agentName)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	return agent.Backend
}

func TestEnsureReviewerAgentUsesWorkspaceBackend(t *testing.T) {
	env := newBackendTestEnv(t, "claude")
	if err := env.module.ensureReviewerAgent(context.Background(), prReviewTestWorkspace, "review-hello-pr-7"); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}
	if got := env.agentBackend(t, "review-hello-pr-7"); got != "claude" {
		t.Fatalf("backend = %q, want claude", got)
	}
}

func TestEnsureReviewerAgentDefaultsToCodex(t *testing.T) {
	for _, workspaceBackend := range []string{"", "not-a-real-backend"} {
		env := newBackendTestEnv(t, workspaceBackend)
		if err := env.module.ensureReviewerAgent(context.Background(), prReviewTestWorkspace, "review-hello-pr-7"); err != nil {
			t.Fatalf("ensureReviewerAgent(%q): %v", workspaceBackend, err)
		}
		if got := env.agentBackend(t, "review-hello-pr-7"); got != "codex" {
			t.Fatalf("workspace backend %q: agent backend = %q, want codex", workspaceBackend, got)
		}
	}
}

func TestEnsureReviewerAgentMigratesExistingBackend(t *testing.T) {
	const agentName = "review-hello-pr-7"
	env := newBackendTestEnv(t, "claude")
	env.seedReviewer(t, agentName, "codex")
	ctx := context.Background()

	// A live orchestration session carrying runtime identity from the codex
	// run plus keys that must survive the migration.
	if _, err := env.store.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: prReviewTestWorkspace,
		SessionID:    "orch-1",
		AgentID:      agentName,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"lead_runtime_provider":     "codex",
			"lead_runtime_status":       "idle",
			"codex_app_server_endpoint": "unix:///tmp/x.sock",
			"codex_provider_thread_id":  "thread-1",
			"lead_harness_session_id":   "stale-uuid",
			"source":                    "web-terminal",
			"lead_workdir":              "/tmp/wt",
		},
	}); err != nil {
		t.Fatalf("create orchestration session: %v", err)
	}
	env.term.tabs = []tabmeta.TabMetadata{
		{SessionName: "agent-review-hello-pr-7", Kind: terminalKindAgent, AgentID: agentName, PTYAlive: true},
		{SessionName: "agent-other", Kind: terminalKindAgent, AgentID: "other-agent", PTYAlive: true},
		{SessionName: "shell-1", Kind: "shell"},
	}

	if err := env.module.ensureReviewerAgent(ctx, prReviewTestWorkspace, agentName); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}

	if got := env.agentBackend(t, agentName); got != "claude" {
		t.Fatalf("backend = %q, want claude", got)
	}
	if len(env.term.deleted) != 1 || env.term.deleted[0] != "agent-review-hello-pr-7" {
		t.Fatalf("deleted tabs = %v, want only the reviewer's tab", env.term.deleted)
	}
	sess, err := env.store.AgentSessions().Get(ctx, prReviewTestWorkspace, "orch-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	for _, gone := range []string{"lead_runtime_provider", "lead_runtime_status", "codex_app_server_endpoint", "codex_provider_thread_id", "lead_harness_session_id"} {
		if _, ok := sess.Metadata[gone]; ok {
			t.Fatalf("metadata key %q survived migration: %v", gone, sess.Metadata)
		}
	}
	for _, kept := range []string{"source", "lead_workdir"} {
		if _, ok := sess.Metadata[kept]; !ok {
			t.Fatalf("metadata key %q was wrongly cleared: %v", kept, sess.Metadata)
		}
	}
}

func TestEnsureReviewerAgentSameBackendIsNoop(t *testing.T) {
	const agentName = "review-hello-pr-7"
	env := newBackendTestEnv(t, "claude")
	env.seedReviewer(t, agentName, "claude")

	if err := env.module.ensureReviewerAgent(context.Background(), prReviewTestWorkspace, agentName); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}
	if env.term.listCalled || env.term.deleteCalls != 0 {
		t.Fatalf("terminal service touched on same-backend ensure (list=%v deletes=%d)", env.term.listCalled, env.term.deleteCalls)
	}
}

func TestMigrateReviewerBackendRefusesWhenTabsUnknown(t *testing.T) {
	const agentName = "review-hello-pr-7"
	env := newBackendTestEnv(t, "claude")
	env.seedReviewer(t, agentName, "codex")
	env.term.listErr = errors.New("redis down")

	err := env.module.ensureReviewerAgent(context.Background(), prReviewTestWorkspace, agentName)
	if err == nil {
		t.Fatal("expected migration to refuse when live terminals cannot be enumerated")
	}
	if got := env.agentBackend(t, agentName); got != "codex" {
		t.Fatalf("backend changed to %q despite refused migration", got)
	}
}
