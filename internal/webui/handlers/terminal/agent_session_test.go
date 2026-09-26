package terminal

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
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

type slowListTerminalService struct {
	service.TerminalService
	delay time.Duration
}

type liveTabTerminalService struct {
	service.TerminalService
	liveSession string
}

func (s liveTabTerminalService) ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error) {
	tabs, err := s.TerminalService.ListTabs(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for i := range tabs {
		if tabs[i].SessionName == s.liveSession {
			tabs[i].Attachable = true
		}
	}
	return tabs, nil
}

type terminalSkillStore struct {
	store.SkillStore
	err error
}

func (s terminalSkillStore) List(context.Context, string, store.SkillFilter) ([]*domain.Skill, error) {
	return nil, s.err
}

type terminalMaterializeStore struct {
	store.Store
	skills store.SkillStore
}

func (s terminalMaterializeStore) Skills() store.SkillStore { return s.skills }
func (s terminalMaterializeStore) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return nil
}

func (s slowListTerminalService) ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error) {
	tabs, err := s.TerminalService.ListTabs(ctx, wsID)
	if err != nil {
		return nil, err
	}
	time.Sleep(s.delay)
	return tabs, nil
}

func TestEnsureAgentTerminalSessionCreatesLeadLaunchSpec(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now().Add(-time.Second),
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

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "lead-ui-e2e")
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
	for _, want := range []string{"'--workspace' 'E2E'", "'--backend' 'codex'", "'lead'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
	for _, forbidden := range []string{"'--server'", "'epic' 'run'", "'--parent' 'E2E-8'", "'--lead' 'lead-ui-e2e'"} {
		if strings.Contains(cmd, forbidden) {
			t.Fatalf("launch command %q contains %q, want interactive lead launch", cmd, forbidden)
		}
	}
	if got := meta.Launch.Env["LOOM_AGENT_TERMINAL_ID"]; got != meta.SessionName {
		t.Fatalf("LOOM_AGENT_TERMINAL_ID = %q, want %q", got, meta.SessionName)
	}
	orchestratorID := meta.Launch.Env["LOOM_ORCHESTRATOR_SESSION_ID"]
	if !strings.HasPrefix(orchestratorID, "lead-") {
		t.Fatalf("LOOM_ORCHESTRATOR_SESSION_ID = %q, want lead- prefix", orchestratorID)
	}
	// Resolve orchestrator attribution via the join helper.
	if got, err := store.OrchestrationSessionIDFor(ctx, st, "E2E", "lead-ui-e2e"); err != nil {
		t.Fatalf("lookup orchestrator: %v", err)
	} else if got != orchestratorID {
		t.Fatalf("lead orchestrator = %q, want %q", got, orchestratorID)
	}
	session, err := st.AgentSessions().Get(ctx, "E2E", orchestratorID)
	if err != nil {
		t.Fatalf("load orchestrator session: %v", err)
	}
	if session.Kind != domain.AgentSessionKindOrchestration || session.TerminalID != meta.SessionName {
		t.Fatalf("agent session = kind:%q terminal:%q", session.Kind, session.TerminalID)
	}
}

func TestBuildAgentLaunchSpecIncludesPromptForInteractiveRole(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "prompts/lead.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_nova", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	for _, want := range []string{"'lead'", "'--prompt' 'prompts/lead.md'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
}

func TestBuildAgentLaunchSpecIncludesBuiltinPromptForInteractiveRole(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "pr-review",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "builtin:pr-review",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "review-nova", RoleName: "pr-review"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_review", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	for _, want := range []string{"'lead'", "'--prompt' 'builtin:pr-review'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
}

func TestBuildAgentLaunchSpecIncludesCheckoutPromptForPRReviewerRole(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "pr-reviewer",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "builtin:pr-review-checkout",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "review-nova-pr-7", RoleName: "pr-reviewer"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_review", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	for _, want := range []string{"'lead'", "'--prompt' 'builtin:pr-review-checkout'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
}

func TestBuildAgentLaunchSpecInlinePromptOmitsRolePromptFile(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		Prompt:       "inline wins",
		PromptFile:   "prompts/ignored.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "operator-a", RoleName: "operator"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_operator", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	if !strings.Contains(cmd, "'lead'") {
		t.Fatalf("launch command %q missing lead runtime", cmd)
	}
	if strings.Contains(cmd, "'--prompt'") || strings.Contains(cmd, "prompts/ignored.md") {
		t.Fatalf("launch command %q includes role prompt_file despite inline prompt", cmd)
	}
}

func TestBuildAgentLaunchSpecCustomInteractiveRoleUsesLeadRuntime(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "prompts/operator.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "operator-a", RoleName: "operator"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_operator", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	for _, want := range []string{"'lead'", "'--prompt' 'prompts/operator.md'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
	for _, forbidden := range []string{"'agent' 'operator-a'", "'--auto'", "'--daemon-mode'"} {
		if strings.Contains(cmd, forbidden) {
			t.Fatalf("launch command %q contains worker arg %q", cmd, forbidden)
		}
	}
}

func TestBuildAgentLaunchSpecCustomWorkerRoleUnchanged(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "reviewer",
		Kind:         string(domain.RoleKindWorker),
		PromptFile:   "prompts/reviewer.md",
		TaskFilter:   "review",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "review-a", RoleName: "reviewer", Parent: "EPIC-1"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_review", agent, "")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	cmd := strings.Join(launch.Argv, " ")
	for _, want := range []string{"'agent' 'review-a'", "'--prompt' 'prompts/reviewer.md'", "'--auto'", "'--daemon-mode'", "'--task-filter' 'review'", "'--parent' 'EPIC-1'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command %q missing %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "'lead'") {
		t.Fatalf("launch command %q contains lead runtime, want worker runtime", cmd)
	}
}

func TestEnsureAgentTerminalSessionLaunchesLeadInConfiguredWorktree(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now().Add(-time.Second),
	)
	worktree := filepath.Join(t.TempDir(), "worktrees", "slack-src", "nova")
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0755); err != nil {
		t.Fatalf("create worktree marker: %v", err)
	}
	workspacePath := t.TempDir()
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["E2E"] = bootstrap.WorkspaceLocalState{
			Path: workspacePath,
			Agents: map[string]bootstrap.AgentLocalState{
				"nova": {Worktree: worktree},
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Skills().Create(ctx, store.SkillCreate{
		WorkspaceKey: "E2E",
		Ref:          domain.RoleSkillRef("lead", "terminal-skill"),
		Description:  "terminal materialization",
		Content:      "Lead skill body\n",
	}); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "nova",
		RoleName:     "lead",
		Parent:       "E2E-8",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "nova")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil {
		t.Fatal("launch spec is nil")
	}
	if meta.Launch.Cwd != worktree {
		t.Fatalf("Launch.Cwd = %q, want configured lead worktree %q", meta.Launch.Cwd, worktree)
	}
	materialized, err := os.ReadFile(filepath.Join(worktree, ".agents", "skills", "terminal-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read terminal skill: %v", err)
	}
	if !strings.Contains(string(materialized), "name: terminal-skill\n") || !strings.HasSuffix(string(materialized), "Lead skill body\n") {
		t.Fatalf("terminal skill = %q", materialized)
	}
	hookConfig, err := os.ReadFile(filepath.Join(worktree, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read terminal hook config: %v", err)
	}
	if !strings.Contains(string(hookConfig), "loom skill materialize") {
		t.Fatalf("terminal hook config = %q", hookConfig)
	}
}

func TestEnsureAgentTerminalSessionFailsBeforeTabOnSkillCollision(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(nil, tabStore, nil, rdb, nil, time.Now().Add(-time.Second))
	worktree := filepath.Join(t.TempDir(), "worktrees", "repo", "nova")
	skillDir := filepath.Join(worktree, ".agents", "skills", "alpha")
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("create worktree marker: %v", err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	existing := filepath.Join(skillDir, "README.md")
	if err := os.WriteFile(existing, []byte("user file\n"), 0o644); err != nil {
		t.Fatalf("write collision: %v", err)
	}
	if err := localworkspace.RememberAgentWorktree("E2E", "nova", worktree); err != nil {
		t.Fatalf("remember worktree: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "E2E", Name: "lead", Backend: "codex"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Skills().Create(ctx, store.SkillCreate{
		WorkspaceKey: "E2E",
		Ref:          domain.WorkspaceSkillRef("alpha"),
		Description:  "alpha",
		Content:      "body",
		Files:        []domain.SkillFile{{Path: "readme.md", Content: "managed"}},
	}); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	_, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "nova")
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindInternal {
		t.Fatalf("ensureAgentTerminalSession error = %v, want internal materialization failure", err)
	}
	for _, want := range []string{"README.md", "readme.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ensure error = %q, want path %q", err, want)
		}
	}
	tabs, listErr := svc.ListTabs(ctx, "E2E")
	if listErr != nil {
		t.Fatalf("list tabs: %v", listErr)
	}
	if len(tabs) != 0 {
		t.Fatalf("terminal tab persisted after materialization failure: %#v", tabs)
	}
	got, readErr := os.ReadFile(existing)
	if readErr != nil || string(got) != "user file\n" {
		t.Fatalf("collision file changed: content=%q err=%v", got, readErr)
	}
}

func TestEnsureAgentTerminalSessionContinuesOnSkillStoreOutage(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	svc := webuiterminal.NewTerminalService(nil, tabStore, nil, rdb, nil, time.Now().Add(-time.Second))
	worktree := filepath.Join(t.TempDir(), "worktrees", "repo", "nova")
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("create worktree marker: %v", err)
	}
	if err := localworkspace.RememberAgentWorktree("E2E", "nova", worktree); err != nil {
		t.Fatalf("remember worktree: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "E2E", Name: "lead", Backend: "codex"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	outageStore := terminalMaterializeStore{
		Store:  st,
		skills: terminalSkillStore{err: &url.Error{Op: "GET", URL: "http://fleet-db/skills", Err: syscall.ECONNREFUSED}},
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, outageStore, "E2E", "nova")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil || meta.Launch.Cwd != worktree {
		t.Fatalf("launch after outage = %#v, want configured worktree", meta.Launch)
	}
	if _, statErr := os.Lstat(filepath.Join(worktree, ".agents")); !os.IsNotExist(statErr) {
		t.Fatalf("store outage touched skill projection: %v", statErr)
	}
}

func TestMaterializeInteractiveSkillsPropagatesCancellation(t *testing.T) {
	target := t.TempDir()
	st := terminalMaterializeStore{skills: terminalSkillStore{err: context.Canceled}}
	err := materializeInteractiveSkills(context.Background(), st, "E2E", "lead", "codex", target)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("materializeInteractiveSkills error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(filepath.Join(target, ".agents")); !os.IsNotExist(statErr) {
		t.Fatalf("canceled materialization touched target: %v", statErr)
	}
}

func TestEnsureAgentTerminalSessionSkipsSkillRewriteWhenStaleTabPTYIsLive(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	baseSvc := webuiterminal.NewTerminalService(nil, tabStore, nil, rdb, nil, time.Now().Add(time.Hour))
	worktree := filepath.Join(t.TempDir(), "worktrees", "repo", "nova")
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("create worktree marker: %v", err)
	}
	if err := localworkspace.RememberAgentWorktree("E2E", "nova", worktree); err != nil {
		t.Fatalf("remember worktree: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E", Name: "lead", Kind: string(domain.RoleKindInteractive), Backend: "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	ref := domain.WorkspaceSkillRef("terminal-skill")
	if _, err := st.Skills().Create(ctx, store.SkillCreate{
		WorkspaceKey: "E2E", Ref: ref, Description: "terminal materialization", Content: "Old body\n",
	}); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E", Name: "nova", RoleName: "lead", Backend: "codex",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	first, err := ensureAgentTerminalSession(ctx, baseSvc, st, "E2E", "nova")
	if err != nil {
		t.Fatalf("first ensureAgentTerminalSession: %v", err)
	}
	skillPath := filepath.Join(worktree, ".agents", "skills", "terminal-skill", "SKILL.md")
	markerPath := filepath.Join(worktree, ".agents", "skills", ".loom-skills-marker.json")
	beforeSkill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read initial skill: %v", err)
	}
	beforeMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read initial marker: %v", err)
	}
	newBody := "New body that must wait for the next process\n"
	if _, err := st.Skills().Update(ctx, "E2E", ref, store.SkillUpdate{Content: &newBody}); err != nil {
		t.Fatalf("update skill: %v", err)
	}
	newBackend := "claude"
	if _, err := st.Agents().Update(ctx, "E2E", "nova", store.AgentUpdate{Backend: &newBackend}); err != nil {
		t.Fatalf("update agent backend: %v", err)
	}

	liveSvc := liveTabTerminalService{TerminalService: baseSvc, liveSession: first.SessionName}
	second, err := ensureAgentTerminalSession(ctx, liveSvc, st, "E2E", "nova")
	if err != nil {
		t.Fatalf("second ensureAgentTerminalSession: %v", err)
	}
	if second.SessionName == first.SessionName {
		t.Fatalf("session = %q, want stale launch spec rebuild to remain intact", second.SessionName)
	}
	afterSkill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill after live-PTY rebuild: %v", err)
	}
	afterMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker after live-PTY rebuild: %v", err)
	}
	if string(afterSkill) != string(beforeSkill) {
		t.Fatalf("live PTY worktree skill was rewritten:\nbefore=%q\nafter=%q", beforeSkill, afterSkill)
	}
	if string(afterMarker) != string(beforeMarker) {
		t.Fatalf("live PTY worktree marker was rewritten:\nbefore=%q\nafter=%q", beforeMarker, afterMarker)
	}
	tabs, err := liveSvc.ListTabs(ctx, "E2E")
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	var oldStillPresent bool
	for _, tab := range tabs {
		if tab.SessionName == first.SessionName {
			oldStillPresent = true
		}
	}
	if !oldStillPresent {
		t.Fatal("live stale tab was pruned during rebuild")
	}
}

func TestBuildAgentLaunchSpecFallsBackWhenConfiguredWorktreeMissing(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st := memstore.New()
	missing := filepath.Join(t.TempDir(), "missing", "nova")
	if err := localworkspace.RememberAgentWorktree("E2E", "nova", missing); err != nil {
		t.Fatalf("remember worktree: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}

	launch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_nova", agent, "lead-1")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	if launch.Cwd != "" {
		t.Fatalf("Launch.Cwd = %q, want empty fallback for missing worktree", launch.Cwd)
	}
}

// TestAgentTerminalLaunchSpecStale_DetectsBackendChange exercises the
// cache-validity helper. An existing tab whose launch argv lacks
// --backend is stale once the workspace default is set, because the next
// build would include the flag. ensure() relies on this check to know
// when to emit a fresh tab instead of returning the cached one.
func TestAgentTerminalLaunchSpecStale_DetectsBackendChange(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "E2E", Name: "E2E"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "E2E", Name: "lead"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}

	// Build the "previous" cached spec via the same builder so the only
	// thing that changes between the two states is the workspace's daemon
	// profile (which contributes the --backend fallback).
	cachedLaunch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_old", agent, "")
	if err != nil {
		t.Fatalf("build cached launch: %v", err)
	}
	existing := &tabmeta.TabMetadata{SessionName: "term_old", Launch: cachedLaunch}

	if agentTerminalLaunchSpecStale(ctx, st, "E2E", existing, agent) {
		t.Fatal("with no workspace default backend, the cached spec matches what would be built — spec is fresh")
	}

	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{
		WorkspaceKey: "E2E",
		AgentBackend: "codex",
	}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}

	if !agentTerminalLaunchSpecStale(ctx, st, "E2E", existing, agent) {
		t.Fatal("expected staleness after workspace default backend was set; ensure() would return the cached spec")
	}
}

func TestAgentTerminalLaunchSpecStaleDetectsInteractivePromptFileChange(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "prompts/old.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "operator-a", RoleName: "operator"}
	cachedLaunch, _, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_old", agent, "lead-1")
	if err != nil {
		t.Fatalf("build cached launch: %v", err)
	}
	existing := &tabmeta.TabMetadata{SessionName: "term_old", Launch: cachedLaunch}
	if agentTerminalLaunchSpecStale(ctx, st, "E2E", existing, agent) {
		t.Fatal("unchanged terminal prompt_file should not be stale")
	}

	nextPrompt := "prompts/new.md"
	if _, err := st.Roles().Update(ctx, "E2E", "operator", store.RoleUpdate{PromptFile: &nextPrompt}); err != nil {
		t.Fatalf("update role prompt: %v", err)
	}
	if !agentTerminalLaunchSpecStale(ctx, st, "E2E", existing, agent) {
		t.Fatal("interactive role prompt_file change should invalidate cached launch spec")
	}
}

// TestAgentTerminalLaunchSpecStale_NilLaunchTreatedStale guards against the
// case where an existing tab has no Launch (Bug from earlier passes —
// "terminal metadata missing launch spec"). Treating it as stale lets
// ensure() rebuild instead of failing to attach forever.
func TestAgentTerminalLaunchSpecStale_NilLaunchTreatedStale(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	agent := &domain.Agent{WorkspaceKey: "E2E", Name: "nova", RoleName: "lead"}
	existing := &tabmeta.TabMetadata{SessionName: "term_old", Launch: nil}
	if !agentTerminalLaunchSpecStale(ctx, st, "E2E", existing, agent) {
		t.Fatal("existing tab with nil Launch should be treated as stale")
	}
}

func TestEnsureAgentTerminalSessionSerializesConcurrentCreates(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	baseSvc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now().Add(-time.Second),
	)
	svc := slowListTerminalService{TerminalService: baseSvc, delay: 25 * time.Millisecond}

	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "nova",
		RoleName:     "lead",
		Parent:       "E2E-8",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	const workers = 24
	start := make(chan struct{})
	metas := make(chan *tabmeta.TabMetadata, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "nova")
			if err != nil {
				errs <- err
				return
			}
			metas <- meta
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(metas)

	for err := range errs {
		t.Errorf("ensureAgentTerminalSession: %v", err)
	}

	seenSessions := map[string]bool{}
	for meta := range metas {
		seenSessions[meta.SessionName] = true
	}
	if len(seenSessions) != 1 {
		t.Fatalf("concurrent ensure returned %d sessions: %#v", len(seenSessions), seenSessions)
	}

	tabs, err := baseSvc.ListTabs(ctx, "E2E")
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	var agentTabs int
	for _, tab := range tabs {
		if tab.Kind == "agent" && tab.AgentID == "nova" {
			agentTabs++
		}
	}
	if agentTabs != 1 {
		t.Fatalf("agent tab count = %d, want 1", agentTabs)
	}

	sessions, err := st.AgentSessions().List(ctx, "E2E", store.AgentSessionFilter{AgentID: "nova"})
	if err != nil {
		t.Fatalf("list agent sessions: %v", err)
	}
	var orchestrationSessions int
	for _, session := range sessions {
		if session.Kind == domain.AgentSessionKindOrchestration {
			orchestrationSessions++
		}
	}
	if orchestrationSessions != 1 {
		t.Fatalf("orchestration session count = %d, want 1", orchestrationSessions)
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

	_, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-done")
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
		t.Fatalf("ensureAgentTerminalSession error = %v, want validation", err)
	}
}

func TestEnsureAgentTerminalSessionRejectsActiveEphemeralWorkerWithoutRelaunch(t *testing.T) {
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
		WorkspaceKey:   "E2E",
		Name:           "worker-live",
		RoleName:       "task",
		Mode:           domain.AgentModeEphemeral,
		DesiredState:   domain.AgentDesiredRunning,
		MaxConcurrency: 1,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "E2E", "worker-live", store.AgentUpdate{
		State: &active,
	}); err != nil {
		t.Fatalf("activate agent: %v", err)
	}

	_, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-live")
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
		t.Fatalf("ensureAgentTerminalSession error = %v, want validation", err)
	}
	if !strings.Contains(svcErr.Message, "daemon-owned ephemeral worker") {
		t.Fatalf("validation message = %q, want daemon-owned ephemeral worker guidance", svcErr.Message)
	}

	tabs, err := svc.ListTabs(ctx, "E2E")
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("tab count = %d, want no terminal tab created for daemon-owned worker", len(tabs))
	}
}

func TestEnsureAgentTerminalSessionAllowsStoppedCustomInteractiveRole(t *testing.T) {
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
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "prompts/operator.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "operator-a",
		RoleName:     "operator",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "operator-a", store.AgentUpdate{State: &stopped}); err != nil {
		t.Fatalf("stop agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "operator-a")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil || !meta.Writable {
		t.Fatalf("launch/writable = %#v/%v, want stopped interactive-kind agent launchable", meta.Launch, meta.Writable)
	}
}

func TestEnsureAgentTerminalSessionAllowsEphemeralCustomInteractiveRole(t *testing.T) {
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
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "operator-live",
		RoleName:     "operator",
		Mode:         domain.AgentModeEphemeral,
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "E2E", "operator-live", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "operator-live")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil {
		t.Fatal("launch spec = nil, want ephemeral interactive-kind agent to be launchable")
	}
}

func TestEnsureAgentTerminalSessionCreatesOrchestrationForCustomInteractiveRole(t *testing.T) {
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
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "operator-a",
		RoleName:     "operator",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "operator-a")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	orchestratorID := meta.Launch.Env["LOOM_ORCHESTRATOR_SESSION_ID"]
	if !strings.HasPrefix(orchestratorID, "lead-") {
		t.Fatalf("LOOM_ORCHESTRATOR_SESSION_ID = %q, want lead- prefix", orchestratorID)
	}
	session, err := st.AgentSessions().Get(ctx, "E2E", orchestratorID)
	if err != nil {
		t.Fatalf("load orchestration session: %v", err)
	}
	if session.AgentID != "operator-a" || session.Kind != domain.AgentSessionKindOrchestration {
		t.Fatalf("session = agent:%q kind:%q, want operator orchestration", session.AgentID, session.Kind)
	}
}

func TestEnsureAgentTerminalSessionCreatesLaunchForStoppedAssignedLead(t *testing.T) {
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
		Name:         "nova",
		RoleName:     "lead",
		Parent:       "E2E-8",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "nova", store.AgentUpdate{
		State: &stopped,
	}); err != nil {
		t.Fatalf("stop agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "nova")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil {
		t.Fatal("launch spec = nil, want assigned lead to be resumable")
	}
	if !meta.Writable {
		t.Fatal("writable = false, want assigned lead terminal to be writable")
	}
	cmd := strings.Join(meta.Launch.Argv, " ")
	for _, want := range []string{"--backend", "codex", "lead"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch argv %q missing %q", cmd, want)
		}
	}
}

func TestEnsureAgentTerminalSessionCreatesLaunchForStoppedUnassignedLead(t *testing.T) {
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
		Name:         "atlas",
		RoleName:     "lead",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "atlas", store.AgentUpdate{
		State: &stopped,
	}); err != nil {
		t.Fatalf("stop agent: %v", err)
	}

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "atlas")
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.Launch == nil {
		t.Fatal("launch spec = nil, want unassigned lead to be resumable")
	}
	if !meta.Writable {
		t.Fatal("writable = false, want unassigned lead terminal to be writable")
	}
	cmd := strings.Join(meta.Launch.Argv, " ")
	for _, want := range []string{"--backend", "codex", "lead"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch argv %q missing %q", cmd, want)
		}
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

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-done")
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
	if meta.Attachable {
		t.Fatal("Attachable = true, want false for stopped agent tab")
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

	meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "worker-live")
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

// TestBuildAgentLaunchSpecFallsBackToWorkspaceBackend asserts that when the
// agent and role both have no backend, the launch spec picks up the
// workspace's daemon-profile backend. Without this fallback, `loom agentdef
// add nova --role lead` (no --backend) produced a launch command of
// `loom lead` with no --backend flag, so the terminal never started codex.
func TestBuildAgentLaunchSpecFallsBackToWorkspaceBackend(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "E2E", Name: "E2E"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{
		WorkspaceKey: "E2E",
		AgentBackend: "codex",
	}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}
	agent := &domain.Agent{
		WorkspaceKey: "E2E",
		Name:         "nova",
		RoleName:     "lead",
		// No Backend set on the agent itself
	}

	launch, backend, err := buildAgentLaunchSpec(ctx, st, "E2E", "term_1", agent, "")
	if err != nil {
		t.Fatalf("buildAgentLaunchSpec: %v", err)
	}
	if backend != "codex" {
		t.Fatalf("backend = %q, want %q (workspace daemon profile default)", backend, "codex")
	}
	if launch == nil {
		t.Fatal("launch spec is nil")
	}
	joined := strings.Join(launch.Argv, " ")
	if !strings.Contains(joined, "--backend") || !strings.Contains(joined, "codex") {
		t.Fatalf("launch argv missing --backend codex: %v", launch.Argv)
	}
}
