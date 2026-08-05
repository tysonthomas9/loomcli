package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type failingPutTerminalService struct {
	service.TerminalService
	err error
}

type signalingPutTerminalService struct {
	service.TerminalService
	once      sync.Once
	putCalled chan struct{}
}

func (s failingPutTerminalService) PutTab(context.Context, string, *tabmeta.TabMetadata) error {
	return s.err
}

func (s *signalingPutTerminalService) PutTab(
	ctx context.Context,
	workspace string,
	meta *tabmeta.TabMetadata,
) error {
	s.once.Do(func() { close(s.putCalled) })
	return s.TerminalService.PutTab(ctx, workspace, meta)
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
	// Tab creation only reserves the ID. The loom lead child creates and starts
	// heartbeating the durable record when the PTY is actually launched.
	if got, err := store.OrchestrationSessionIDFor(ctx, st, "E2E", "lead-ui-e2e"); err != nil {
		t.Fatalf("lookup orchestrator before launch: %v", err)
	} else if got != "" {
		t.Fatalf("lead orchestrator before launch = %q, want no precreated running session", got)
	}
	if _, err := st.AgentSessions().Get(ctx, "E2E", orchestratorID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reserved orchestrator lookup error = %v, want not found before PTY launch", err)
	}
}

func TestEnsureAgentTerminalSessionPutFailureDoesNotCreateRunningSession(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	baseSvc := webuiterminal.NewTerminalService(nil, tabStore, nil, rdb, nil, time.Now())
	svc := failingPutTerminalService{TerminalService: baseSvc, err: errors.New("put failed")}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "E2E", Name: "lead", Backend: "codex"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "E2E", Name: "lead-put-fail", RoleName: "lead"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if _, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "lead-put-fail"); err == nil || !strings.Contains(err.Error(), "put failed") {
		t.Fatalf("ensure error = %v, want PutTab failure", err)
	}
	sessions, err := st.AgentSessions().List(ctx, "E2E", store.AgentSessionFilter{AgentID: "lead-put-fail"})
	if err != nil {
		t.Fatalf("list agent sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("agent sessions after PutTab failure = %d, want 0", len(sessions))
	}
}

func TestBuildAgentLaunchSpecIncludesPromptForInteractiveRole(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
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
	if launch.Env["LOOM_CONFIG_DIR"] != configDir {
		t.Fatalf("launch config dir = %q, want %q", launch.Env["LOOM_CONFIG_DIR"], configDir)
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

func TestBuildAgentLaunchSpecRejectsDaemonSupervisedWorker(t *testing.T) {
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
	var svcErr *service.ServiceError
	if launch != nil || !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
		t.Fatalf("buildAgentLaunchSpec = (%#v, %v), want worker-terminal validation", launch, err)
	}
	if !strings.Contains(svcErr.Message, "daemon-supervised worker") {
		t.Fatalf("validation message = %q, want daemon-supervised worker guidance", svcErr.Message)
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
	if orchestrationSessions != 0 {
		t.Fatalf("orchestration session count before PTY launch = %d, want 0", orchestrationSessions)
	}
}

func TestEnsureAgentTerminalSessionWaitsForStopLifecycleBoundary(t *testing.T) {
	ctx := context.Background()
	st, tabStore, rdb := newAgentSessionTestDeps(t)
	baseSvc := webuiterminal.NewTerminalService(
		nil,
		tabStore,
		nil,
		rdb,
		nil,
		time.Now(),
	)
	putCalled := make(chan struct{})
	svc := &signalingPutTerminalService{
		TerminalService: baseSvc,
		putCalled:       putCalled,
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Kind:         string(domain.RoleKindInteractive),
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "first-terminal-race",
		RoleName:     "lead",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "E2E", "first-terminal-race", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate agent: %v", err)
	}

	// Model Stop after its empty ownership snapshot. First-terminal metadata
	// creation must wait until Stop makes the stopped state durable.
	unlockStop := webuiterminal.LockAgentLifecycle("E2E", "first-terminal-race")
	stopUnlocked := false
	defer func() {
		if !stopUnlocked {
			unlockStop()
		}
	}()
	type ensureResult struct {
		meta *tabmeta.TabMetadata
		err  error
	}
	resultCh := make(chan ensureResult, 1)
	ensureStarted := make(chan struct{})
	go func() {
		close(ensureStarted)
		meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "first-terminal-race")
		resultCh <- ensureResult{meta: meta, err: err}
	}()
	<-ensureStarted
	select {
	case <-putCalled:
		t.Fatal("first-terminal metadata was created inside Stop's snapshot-to-state-update gap")
	case <-time.After(200 * time.Millisecond):
	}

	stopped := domain.AgentStateStopped
	desiredStopped := domain.AgentDesiredStopped
	if _, err := st.Agents().Update(ctx, "E2E", "first-terminal-race", store.AgentUpdate{
		State:        &stopped,
		DesiredState: &desiredStopped,
	}); err != nil {
		t.Fatalf("persist stopped lifecycle state: %v", err)
	}
	unlockStop()
	stopUnlocked = true

	var result ensureResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal metadata creation did not finish after Stop released the lifecycle boundary")
	}
	if result.err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", result.err)
	}
	if result.meta == nil || result.meta.AgentID != "first-terminal-race" {
		t.Fatalf("terminal metadata = %#v, want stopped interactive agent tab", result.meta)
	}
	select {
	case <-putCalled:
	default:
		t.Fatal("terminal metadata was not created after Stop released the lifecycle boundary")
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
	if !strings.Contains(svcErr.Message, "daemon-supervised worker") {
		t.Fatalf("validation message = %q, want daemon-supervised worker guidance", svcErr.Message)
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
	if !strings.Contains(svcErr.Message, "daemon-supervised worker") {
		t.Fatalf("validation message = %q, want daemon-supervised worker guidance", svcErr.Message)
	}

	tabs, err := svc.ListTabs(ctx, "E2E")
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("tab count = %d, want no terminal tab created for daemon-supervised worker", len(tabs))
	}
}

func TestEnsureAgentTerminalSessionRejectsAdvancedServiceWorkersWithoutCreatingTabs(t *testing.T) {
	tests := []struct {
		name     string
		roleName string
		role     *store.RoleCreate
	}{
		{name: "planner", roleName: "plan"},
		{name: "task runner", roleName: "task"},
		{
			name:     "bug triage",
			roleName: "bug-triage",
			role: &store.RoleCreate{
				WorkspaceKey: "E2E",
				Name:         "bug-triage",
				Kind:         string(domain.RoleKindWorker),
				PromptFile:   "prompts/bug-triage.md",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			if tt.role != nil {
				if _, err := st.Roles().Create(ctx, *tt.role); err != nil {
					t.Fatalf("create role: %v", err)
				}
			}
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "E2E",
				Name:         "advanced-worker",
				RoleName:     tt.roleName,
				Mode:         domain.AgentModeService,
				DesiredState: domain.AgentDesiredRunning,
			}); err != nil {
				t.Fatalf("create agent: %v", err)
			}
			active := domain.AgentStateActive
			if _, err := st.Agents().Update(ctx, "E2E", "advanced-worker", store.AgentUpdate{State: &active}); err != nil {
				t.Fatalf("activate agent: %v", err)
			}

			meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "advanced-worker")
			var svcErr *service.ServiceError
			if meta != nil || !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
				t.Fatalf("ensureAgentTerminalSession = (%#v, %v), want validation", meta, err)
			}
			if !strings.Contains(svcErr.Message, "daemon-supervised worker") {
				t.Fatalf("validation message = %q, want daemon-supervised worker guidance", svcErr.Message)
			}
			tabs, err := svc.ListTabs(ctx, "E2E")
			if err != nil {
				t.Fatalf("list tabs: %v", err)
			}
			if len(tabs) != 0 {
				t.Fatalf("tab count = %d, want no worker terminal metadata", len(tabs))
			}
		})
	}
}

func TestEnsureAgentTerminalSessionAllowsInteractiveLeadPRReviewAndCustomAssignments(t *testing.T) {
	tests := []struct {
		name       string
		roleName   string
		role       *store.RoleCreate
		wantPrompt string
	}{
		{name: "lead", roleName: "lead"},
		{
			name:     "PR review",
			roleName: "pr-review",
			role: &store.RoleCreate{
				WorkspaceKey: "E2E",
				Name:         "pr-review",
				Kind:         string(domain.RoleKindInteractive),
				PromptFile:   "builtin:pr-review",
			},
			wantPrompt: "builtin:pr-review",
		},
		{
			name:     "custom",
			roleName: "operator",
			role: &store.RoleCreate{
				WorkspaceKey: "E2E",
				Name:         "operator",
				Kind:         string(domain.RoleKindInteractive),
				Prompt:       "custom interactive prompt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			if tt.role != nil {
				if _, err := st.Roles().Create(ctx, *tt.role); err != nil {
					t.Fatalf("create role: %v", err)
				}
			}
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "E2E",
				Name:         "interactive-agent",
				RoleName:     tt.roleName,
				Mode:         domain.AgentModeService,
				DesiredState: domain.AgentDesiredRunning,
			}); err != nil {
				t.Fatalf("create agent: %v", err)
			}

			meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "interactive-agent")
			if err != nil {
				t.Fatalf("ensureAgentTerminalSession: %v", err)
			}
			if meta == nil || meta.Launch == nil || !meta.Writable {
				t.Fatalf("interactive terminal metadata = %#v, want launchable tab", meta)
			}
			cmd := strings.Join(meta.Launch.Argv, " ")
			if !strings.Contains(cmd, "'lead'") {
				t.Fatalf("interactive launch command %q missing lead runtime", cmd)
			}
			if strings.Contains(cmd, "'--auto'") || strings.Contains(cmd, "'--daemon-mode'") {
				t.Fatalf("interactive launch command %q contains worker runtime flags", cmd)
			}
			if tt.wantPrompt != "" && !strings.Contains(cmd, tt.wantPrompt) {
				t.Fatalf("interactive launch command %q missing prompt %q", cmd, tt.wantPrompt)
			}
		})
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
	if _, err := st.AgentSessions().Get(ctx, "E2E", orchestratorID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reserved orchestration session lookup error = %v, want not found before PTY launch", err)
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

func TestEnsureAgentTerminalSessionRejectsWorkerWithExistingTerminalTab(t *testing.T) {
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
	var svcErr *service.ServiceError
	if meta != nil || !errors.As(err, &svcErr) || svcErr.Kind != service.KindValidation {
		t.Fatalf("ensureAgentTerminalSession = (%#v, %v), want validation", meta, err)
	}
	if !strings.Contains(svcErr.Message, "daemon-supervised worker") {
		t.Fatalf("validation message = %q, want daemon-supervised worker guidance", svcErr.Message)
	}
	stored, err := svc.GetTab(ctx, "E2E", "term_worker_done")
	if err != nil {
		t.Fatalf("get existing worker tab: %v", err)
	}
	if stored.Launch == nil || !stored.Writable {
		t.Fatalf("existing metadata was mutated during rejection: %#v", stored)
	}
}

func TestEnsureAgentTerminalSessionCreatesFreshTabForStaleRunningInteractiveAgentTab(t *testing.T) {
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
		Name:         "lead-live",
		RoleName:     "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	oldTime := time.Now().UTC().Add(-time.Hour)
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "term_worker_old",
		Workspace:   "E2E",
		Label:       "agent-lead-live",
		Notes:       "old scrollback tab",
		SortOrder:   3,
		Pinned:      true,
		Kind:        "agent",
		AgentID:     "lead-live",
		Role:        "lead",
		Backend:     "codex",
		Writable:    true,
		Launch: &tabmeta.LaunchSpec{
			Argv: []string{"sh", "-c", "loom lead"},
		},
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}); err != nil {
		t.Fatalf("seed tab: %v", err)
	}

	type ensureResult struct {
		meta *tabmeta.TabMetadata
		err  error
	}
	resultChannel := make(chan ensureResult, 1)
	go func() {
		meta, err := ensureAgentTerminalSession(ctx, svc, st, "E2E", "lead-live")
		resultChannel <- ensureResult{meta: meta, err: err}
	}()
	var result ensureResult
	select {
	case result = <-resultChannel:
	case <-time.After(2 * time.Second):
		t.Fatal("stale-tab replacement deadlocked on the agent lifecycle boundary")
	}
	meta, err := result.meta, result.err
	if err != nil {
		t.Fatalf("ensureAgentTerminalSession: %v", err)
	}
	if meta.SessionName == "term_worker_old" {
		t.Fatalf("session = %q, want fresh session for stale running agent tab", meta.SessionName)
	}
	if !strings.HasPrefix(meta.SessionName, "term_") {
		t.Fatalf("session name = %q, want UUID term_ prefix", meta.SessionName)
	}
	if meta.SortOrder != 3 || !meta.Pinned || meta.Label != "agent-lead-live" || meta.Notes != "old scrollback tab" {
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
