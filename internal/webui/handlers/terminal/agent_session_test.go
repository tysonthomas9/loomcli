package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuiterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func newAgentSessionTestDeps(t *testing.T) (*memstore.Store, *tabmeta.Store, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return memstore.New(), tabmeta.NewStore(rdb, nil), rdb
}

func TestEnsureAgentTerminalSessionCreatesLeadEpicLaunchSpec(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now(),
	)

	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "lead-ui-e2e",
		RoleName:     "lead",
		Parent:       "E2E-8",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "lead-ui-e2e", "http://loom.test")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if !strings.HasPrefix(meta.SessionName, "term_") {
		t.Fatalf("session name = %q, want UUID term_ prefix", meta.SessionName)
	}
	if meta.Kind != "agent" || meta.AgentID != "lead-ui-e2e" || meta.Role != "lead" {
		t.Fatalf("agent metadata = kind:%q agent:%q role:%q", meta.Kind, meta.AgentID, meta.Role)
	}
	if meta.Launch == nil || len(meta.Launch.Argv) != 2 {
		t.Fatalf("launch spec = %#v, want shell argv", meta.Launch)
	}
	cmd := meta.Launch.Argv[1]
	for _, want := range []string{"'--server' 'http://loom.test'", "'--workspace' 'E2E'", "'--backend' 'codex'", "'epic' 'run'", "'--parent' 'E2E-8'", "'--lead' 'lead-ui-e2e'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
	if got := meta.Launch.Env["LOOM_AGENT_TERMINAL_ID"]; got != meta.SessionName {
		t.Fatalf("LOOM_AGENT_TERMINAL_ID = %q, want %q", got, meta.SessionName)
	}
	orchestratorID := meta.Launch.Env["LOOM_ORCHESTRATOR_SESSION_ID"]
	if !strings.HasPrefix(orchestratorID, "lead-") {
		t.Fatalf("LOOM_ORCHESTRATOR_SESSION_ID = %q, want lead- prefix", orchestratorID)
	}
	lead, err := st.Agents().Get(ctx, "E2E", "lead-ui-e2e")
	if err != nil {
		t.Fatalf("reload lead: %v", err)
	}
	if lead.OrchestratorSessionID != orchestratorID {
		t.Fatalf("lead orchestrator = %q, want %q", lead.OrchestratorSessionID, orchestratorID)
	}
	session, err := st.AgentSessions().Get(ctx, "E2E", orchestratorID)
	if err != nil {
		t.Fatalf("load orchestrator session: %v", err)
	}
	if session.Kind != domain.AgentSessionKindOrchestration || session.TerminalID != meta.SessionName {
		t.Fatalf("agent session = kind:%q terminal:%q", session.Kind, session.TerminalID)
	}
}

func TestEnsureAgentTerminalSessionRejectsStoppedAgentWithoutSession(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now(),
	)

	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "worker-done",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "worker-done", store.AgentUpdate{
		State: &stopped,
	}); err != nil {
		t.Fatalf("stop agent: %v", err)
	}

	_, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-done", "http://loom.test")
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
		t.Fatalf("ensureAgentTerminalSession error = %v, want validation", err)
	}
}

func TestEnsureAgentTerminalSessionDoesNotRelaunchStoppedExistingAgentTab(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now(),
	)

	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "worker-done",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "worker-done", store.AgentUpdate{
		State: &stopped,
	}); err != nil {
		t.Fatalf("stop agent: %v", err)
	}

	now := time.Now().UTC()
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "term_worker_done",
		Workspace:   "E2E",
		Label:       "agent-worker-done",
		Kind:        "agent",
		AgentID:     "worker-done",
		Role:        "task",
		Backend:     "codex",
		Writable:    true,
		Launch: &tabmeta.LaunchSpec{
			Argv: []string{"sh", "-c", "loom task worker-done --auto"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed tab: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-done", "http://loom.test")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.SessionName != "term_worker_done" {
		t.Fatalf("session = %q, want existing stopped tab", meta.SessionName)
	}
	if meta.Launch != nil {
		t.Fatalf("launch spec = %#v, want nil for stopped agent", meta.Launch)
	}
	if meta.Writable {
		t.Fatal("writable = true, want false for stopped agent tab")
	}
	if meta.PTYAlive {
		t.Fatal("PTYAlive = true, want false for stopped agent tab")
	}
}

func TestEnsureAgentTerminalSessionCreatesFreshTabForStaleRunningAgentTab(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now(),
	)

	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "worker-live",
		RoleName:     "task",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	oldTime := time.Now().UTC().Add(-time.Hour)
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "term_worker_old",
		Workspace:   "E2E",
		Label:       "agent-worker-live",
		Notes:       "old scrollback tab",
		SortOrder:   3,
		Pinned:      true,
		Kind:        "agent",
		AgentID:     "worker-live",
		Role:        "task",
		Backend:     "codex",
		Writable:    true,
		Launch: &tabmeta.LaunchSpec{
			Argv: []string{"sh", "-c", "loom task worker-live --auto"},
		},
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}); err != nil {
		t.Fatalf("seed tab: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-live", "http://loom.test")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.SessionName == "term_worker_old" {
		t.Fatalf("session = %q, want fresh session for stale running agent tab", meta.SessionName)
	}
	if !strings.HasPrefix(meta.SessionName, "term_") {
		t.Fatalf("session name = %q, want UUID term_ prefix", meta.SessionName)
	}
	if meta.SortOrder != 3 || !meta.Pinned || meta.Label != "agent-worker-live" || meta.Notes != "old scrollback tab" {
		t.Fatalf("metadata not preserved: sort=%d pinned=%v label=%q notes=%q", meta.SortOrder, meta.Pinned, meta.Label, meta.Notes)
	}
	if meta.Launch == nil {
		t.Fatal("launch spec = nil, want relaunchable terminal")
	}
	if got := meta.Launch.Env["LOOM_AGENT_TERMINAL_ID"]; got != meta.SessionName {
		t.Fatalf("LOOM_AGENT_TERMINAL_ID = %q, want %q", got, meta.SessionName)
	}
	tabs, err := svc.ListTabs(ctx, "E2E")
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	for _, tab := range tabs {
		if tab.SessionName == "term_worker_old" {
			t.Fatalf("stale tab %q was not pruned", tab.SessionName)
		}
	}
}

func TestBuildAgentLaunchSpecRejectsUnknownRoleWithoutPrompt(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	agent := &domain.Agent{
		WorkspaceKey: "E2E",
		Name:         "reviewer",
		RoleName:     "reviewer",
	}

	if _, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_1", agent, ""); err == nil {
		t.Fatal("buildAgentLaunchSpec error = nil, want missing launch spec error")
	}
}
