package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLoadDaemonConfigFromStoreProjectsFleetDBEntities(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxAgents := 5
	startupTimeout := 12
	traces := true
	metrics := false
	maxRetries := 7
	rateLimitNoCount := true
	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{
		WorkspaceKey: "WS",
		PIDFile:      "/tmp/loom.pid",
		LogDir:       "/tmp/logs",
		EventsDir:    "/tmp/events",
		MaxAgents:    &maxAgents,
		IssueBackend: "api",
		AgentBackend: "codex",
		RestartPolicy: domain.RestartPolicy{
			MaxRetries:       &maxRetries,
			RateLimitNoCount: &rateLimitNoCount,
		},
		StartupTimeout: &startupTimeout,
		OTel: &domain.OTelSettings{
			Enabled: true, Endpoint: "http://otel", Protocol: "grpc",
			ServiceName: "loom", SampleRate: 0.5, FlushIntervalMs: 1000,
			Traces: &traces, Metrics: &metrics,
		},
	}); err != nil {
		t.Fatalf("upsert daemon: %v", err)
	}
	rolePriority := 3
	roleConcurrency := 2
	roleBudget := 1.25
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS", Name: "task", Description: "Task role",
		PromptFile: "task.md", Model: "gpt", TaskFilter: "kind:task", Backend: "codex",
		PathPatterns: []string{"*.go"}, Skills: []string{"go"},
		MaxPriority: &rolePriority, MaxConcurrency: &roleConcurrency,
		ReadOnly: true, AllowedTools: []string{"git"}, DeniedTools: []string{"rm"},
		MaxBudgetUSD: &roleBudget,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "worker", RoleName: "task", Auto: true,
		Backend: "codex", FallbackBackends: []string{"claude"}, Repos: []string{"api"},
		RepoGroups: []string{"backend"}, CrossRepo: true, Parent: "EPIC-1",
		Mode: domain.AgentModeService, TaskFilter: "kind:task", MaxConcurrency: 2,
		BudgetPolicy: "strict", DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	cfg, err := loadDaemonConfigFromStore(ctx, st, "WS", nil, t.TempDir())
	if err != nil {
		t.Fatalf("loadDaemonConfigFromStore: %v", err)
	}
	if cfg.Backend != "codex" || cfg.Daemon.IssueBackend != "api" {
		t.Fatalf("backend projection = backend %q issue %q", cfg.Backend, cfg.Daemon.IssueBackend)
	}
	if cfg.Daemon.OTel == nil || !cfg.Daemon.OTel.Enabled || cfg.Daemon.OTel.Traces == nil || cfg.Daemon.OTel.Metrics == nil {
		t.Fatalf("otel projection = %+v", cfg.Daemon.OTel)
	}
	if got := cfg.Daemon.GetStartupTimeout(30 * time.Second); got != 12*time.Second {
		t.Fatalf("startup timeout = %v, want 12s", got)
	}
	role := cfg.Roles["task"]
	if role.MaxPriority == nil || *role.MaxPriority != 3 || !role.ReadOnly || len(role.Skills) != 1 {
		t.Fatalf("role projection = %+v", role)
	}
	if len(cfg.Agents) != 1 || !cfg.Agents[0].CrossRepo || cfg.Agents[0].DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agents projection = %+v", cfg.Agents)
	}
}

func TestProjectConfigHelpersAndValidation(t *testing.T) {
	t.Run("agent equality compares persisted fields", func(t *testing.T) {
		a := AgentEntry{Worktree: "w", Role: "task", Repo: "api", Auto: true, Backend: "codex", FallbackBackends: []string{"claude"}, PathPatterns: []string{"*.go"}, Repos: []string{"api"}, RepoGroups: []string{"backend"}, CrossRepo: true, Parent: "EPIC", Mode: domain.AgentModeService, DesiredState: domain.AgentDesiredRunning}
		b := a
		b.SourceRepos = []string{"ignored"}
		if !a.Equal(b) {
			t.Fatalf("Equal returned false for source-repo-only difference")
		}
		b.Repos = []string{"web"}
		if a.Equal(b) {
			t.Fatalf("Equal returned true for persisted repo difference")
		}
	})

	t.Run("validate agent limits", func(t *testing.T) {
		if err := validateAgents([]AgentEntry{{Role: "task"}}, nil); err == nil || !strings.Contains(err.Error(), "worktree") {
			t.Fatalf("missing worktree err = %v", err)
		}
		if err := validateAgents([]AgentEntry{{Worktree: "w"}}, nil); err == nil || !strings.Contains(err.Error(), "role") {
			t.Fatalf("missing role err = %v", err)
		}
		if err := validateAgents([]AgentEntry{{Worktree: "w", Role: "task", FallbackBackends: []string{""}}}, nil); err == nil || !strings.Contains(err.Error(), "fallback_backends") {
			t.Fatalf("empty fallback err = %v", err)
		}
		neg := -1
		if err := validateAgents(nil, &neg); err == nil || !strings.Contains(err.Error(), "non-negative") {
			t.Fatalf("negative max err = %v", err)
		}
		one := 1
		err := validateAgents([]AgentEntry{
			{Worktree: "a", Role: "task"},
			{Worktree: "b", Role: "task"},
			{Worktree: "lead", Role: "lead"},
		}, &one)
		if err == nil || !strings.Contains(err.Error(), "too many runnable") {
			t.Fatalf("limit err = %v", err)
		}
	})

	t.Run("overlay daemon settings", func(t *testing.T) {
		dst := DaemonSettings{}
		enabled := false
		OverlayDaemonSettings(&dst, &DaemonSettings{
			PIDFile: "/pid", LogDir: "/logs", EventsDir: "/events",
			RestartPolicy: RestartPolicy{
				MaxRetries: IntPtr(1), BackoffInitial: IntPtr(2), BackoffMax: IntPtr(3),
				OutputTimeout: IntPtr(4), RateLimitBackoff: IntPtr(5), RateLimitMaxWait: IntPtr(6),
				RateLimitNoCount: &enabled, TimeoutBackoff: IntPtr(7), NoWorkBackoff: IntPtr(8),
				IdlePollInterval: IntPtr(9), YieldTimeout: IntPtr(10), SigtermTimeout: IntPtr(11),
			},
			MaxAgents: IntPtr(12), RedisURL: "redis://", IssueBackend: "fleetdb",
			StartupTimeout: IntPtr(13),
			OTel: &OTelDaemonConfig{
				Enabled: true, Endpoint: "endpoint", Protocol: "http",
				ServiceName: "svc", SampleRate: 1, FlushIntervalMs: 99,
				Traces: &enabled, Metrics: &enabled,
			},
		})
		if dst.PIDFile != "/pid" || dst.LogDir != "/logs" || dst.RestartPolicy.SigtermTimeout == nil || dst.OTel == nil || dst.OTel.Endpoint != "endpoint" {
			t.Fatalf("overlay result = %+v", dst)
		}
	})

	if got := (&DaemonSettings{}).GetStartupTimeout(42 * time.Second); got != 42*time.Second {
		t.Fatalf("fallback startup timeout = %v", got)
	}
	if got := (*DaemonSettings)(nil).GetStartupTimeout(42 * time.Second); got != 42*time.Second {
		t.Fatalf("nil startup timeout = %v", got)
	}
	if got := resolvePath("/base", "rel"); got != filepath.Join("/base", "rel") {
		t.Fatalf("relative resolve = %q", got)
	}
	if got := resolvePath("/base", "/abs"); got != "/abs" {
		t.Fatalf("absolute resolve = %q", got)
	}
}

func TestProjectConfigSmallHelpersAndDaemonStatePath(t *testing.T) {
	if p := BoolPtr(true); p == nil || !*p {
		t.Fatalf("BoolPtr(true) = %#v", p)
	}
	cfg := &DaemonConfig{Roles: map[string]RoleConfig{
		"review": {Backend: "codex", TaskFilter: "kind:task"},
	}}
	role, ok := cfg.ResolveRole("review")
	if !ok || role.Backend != "codex" {
		t.Fatalf("ResolveRole(review) role=%+v ok=%t", role, ok)
	}
	if _, ok := cfg.ResolveRole("missing"); ok {
		t.Fatal("ResolveRole(missing) ok = true")
	}

	dataDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	t.Setenv("LOOM_WORKSPACE", "")
	got := ResolveDaemonStatePath(projectDir)
	want := filepath.Join(projectDir, ".loom", "daemon-agents.json")
	if got != want {
		t.Fatalf("ResolveDaemonStatePath = %q, want %q", got, want)
	}
}

func TestValidateAgentReposNoWorkspaceAndResolveRepoPathErrors(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	if err := ValidateAgentRepos([]AgentEntry{{Worktree: "plain", Role: "task"}}); err != nil {
		t.Fatalf("ValidateAgentRepos without repo = %v", err)
	}
	if err := ValidateAgentRepos([]AgentEntry{{Worktree: "repo-worker", Role: "task", Repo: "api"}}); err != nil {
		t.Fatalf("ValidateAgentRepos without active workspace should warn only, got %v", err)
	}
	if _, err := resolveRepoPath("api"); err == nil || !strings.Contains(err.Error(), "no active workspace") {
		t.Fatalf("resolveRepoPath without active workspace err = %v", err)
	}
}
