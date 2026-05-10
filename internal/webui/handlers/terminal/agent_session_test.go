package terminal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
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
