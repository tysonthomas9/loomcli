package daemonwire

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestBuildStoreBackedDaemonConfigFnUsesFleetDBStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv(bootstrap.EnvWorkspace, "WS1")

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxPriority := 2
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS1",
		Name:         "task",
		TaskFilter:   "ready",
		Backend:      "codex",
		MaxPriority:  &maxPriority,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	maxAgents := 7
	profile, err := st.Daemon().Get(ctx, "WS1")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.PIDFile = ".loom/custom.pid"
	profile.MaxAgents = &maxAgents
	if _, err := st.Daemon().Upsert(ctx, profile); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:     "WS1",
		Name:             "nova",
		RoleName:         "task",
		Backend:          "codex",
		FallbackBackends: []string{"claude"},
		Repos:            []string{"api"},
		RepoGroups:       []string{"backend"},
		CrossRepo:        true,
		Parent:           "epic-1",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	fn := BuildStoreBackedDaemonConfigFn(st)
	if fn == nil {
		t.Fatal("BuildStoreBackedDaemonConfigFn returned nil")
	}
	raw, err := fn()
	if err != nil {
		t.Fatalf("daemon config fn: %v", err)
	}
	var got config.DaemonConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if got.Backend != "fleetdb" || got.Daemon.IssueBackend != "fleetdb" {
		t.Fatalf("backend = %q daemon.issue_backend = %q, want fleetdb", got.Backend, got.Daemon.IssueBackend)
	}
	if got.Daemon.PIDFile != ".loom/custom.pid" {
		t.Fatalf("pid_file = %q", got.Daemon.PIDFile)
	}
	if got.Daemon.MaxAgents == nil || *got.Daemon.MaxAgents != 7 {
		t.Fatalf("max_agents = %v, want 7", got.Daemon.MaxAgents)
	}
	if role, ok := got.Roles["task"]; !ok || role.TaskFilter != "ready" || role.Backend != "codex" {
		t.Fatalf("role task = %+v, ok=%v", role, ok)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(got.Agents))
	}
	agent := got.Agents[0]
	if agent.Worktree != "nova" || agent.Role != "task" || !agent.CrossRepo || agent.Parent != "epic-1" {
		t.Fatalf("agent = %+v", agent)
	}
	if len(agent.Repos) != 1 || agent.Repos[0] != "api" {
		t.Fatalf("agent repos = %v, want [api]", agent.Repos)
	}
	if len(agent.RepoGroups) != 1 || agent.RepoGroups[0] != "backend" {
		t.Fatalf("agent repo_groups = %v, want [backend]", agent.RepoGroups)
	}
}

func TestDaemonwireDomainConversionsClonePointersAndSlices(t *testing.T) {
	maxRetries, maxAgents, startupTimeout := 3, 5, 12
	trace, metrics, rateLimitNoCount := true, false, true
	budget := 1.25
	profile := &domain.DaemonProfile{
		PIDFile:        ".loom/pid",
		LogDir:         ".loom/logs",
		EventsDir:      ".loom/events",
		IssueBackend:   "fleetdb",
		MaxAgents:      &maxAgents,
		StartupTimeout: &startupTimeout,
		RestartPolicy: domain.RestartPolicy{
			MaxRetries:       &maxRetries,
			RateLimitNoCount: &rateLimitNoCount,
		},
		OTel: &domain.OTelSettings{
			Enabled: true, Endpoint: "http://otel", Protocol: "grpc",
			ServiceName: "loom", SampleRate: 0.5, FlushIntervalMs: 100,
			Traces: &trace, Metrics: &metrics,
		},
	}
	settings := daemonSettingsFromProfile(profile)
	if settings.PIDFile != ".loom/pid" || settings.MaxAgents == nil || *settings.MaxAgents != 5 ||
		settings.StartupTimeout == nil || *settings.StartupTimeout != 12 ||
		settings.RestartPolicy.MaxRetries == nil || *settings.RestartPolicy.MaxRetries != 3 ||
		settings.RestartPolicy.RateLimitNoCount == nil || !*settings.RestartPolicy.RateLimitNoCount ||
		settings.OTel == nil || settings.OTel.Traces == nil || !*settings.OTel.Traces || settings.OTel.Metrics == nil || *settings.OTel.Metrics {
		t.Fatalf("daemon settings = %#v", settings)
	}
	*profile.MaxAgents = 99
	if *settings.MaxAgents != 5 {
		t.Fatal("daemonSettingsFromProfile did not clone pointer values")
	}
	if got := daemonSettingsFromProfile(nil); got.IssueBackend != "fleetdb" {
		t.Fatalf("nil daemon settings = %#v", got)
	}

	maxPriority, maxConcurrency := 2, 4
	role := roleConfigFromDomain(&domain.Role{
		Name: "task", Description: "desc", PromptFile: "prompt.md", Model: "gpt",
		TaskFilter: "ready", Backend: "codex", PathPatterns: []string{"*.go"},
		Skills: []string{"go"}, MaxPriority: &maxPriority, MaxConcurrency: &maxConcurrency,
		ReadOnly: true, AllowedTools: []string{"read"}, DeniedTools: []string{"write"},
		MaxBudgetUSD: &budget,
	})
	if role.Description != "desc" || role.Backend != "codex" || role.MaxPriority == nil || *role.MaxPriority != 2 ||
		len(role.PathPatterns) != 1 || role.PathPatterns[0] != "*.go" || role.MaxBudgetUSD == nil || *role.MaxBudgetUSD != 1.25 {
		t.Fatalf("role config = %#v", role)
	}
	if empty := roleConfigFromDomain(nil); empty.Backend != "" {
		t.Fatalf("nil role config = %#v", empty)
	}

	agent := agentEntryFromDomain(&domain.Agent{
		Name: "nova", RoleName: "task", Auto: true, Backend: "codex",
		FallbackBackends: []string{"claude"}, Repos: []string{"api"},
		RepoGroups: []string{"backend"}, CrossRepo: true, Parent: "epic-1",
		DesiredState: domain.AgentDesiredRunning,
	})
	if agent.Worktree != "nova" || agent.Role != "task" || !agent.Auto || agent.FallbackBackends[0] != "claude" ||
		!agent.CrossRepo || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent entry = %#v", agent)
	}
	if empty := agentEntryFromDomain(nil); empty.Worktree != "" {
		t.Fatalf("nil agent entry = %#v", empty)
	}
}

func TestScoreAndSortQueue(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "LOW", Title: "low", Status: "open", Priority: 4, IssueType: "task", Labels: []string{"go"}, Parent: "P"},
		{ID: "HIGH", Title: "high", Status: "open", Priority: 1, IssueType: "task", Labels: []string{"go"}, Parent: "P"},
		{ID: "SKIP", Title: "skip", Status: "closed", Priority: 1, IssueType: "task", Labels: []string{"go"}, Parent: "P"},
	}
	entries := scoreAndSortQueue(issues, cli.RoleConstraints{TaskFilter: "any", Skills: []string{"go"}})
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].IssueID != "HIGH" || entries[1].IssueID != "LOW" {
		t.Fatalf("sorted entries = %#v", entries)
	}
	if entries[0].Reason == "" || entries[0].Parent != "P" || entries[0].Labels[0] != "go" {
		t.Fatalf("entry details = %#v", entries[0])
	}
}

func TestBuildStoreBackedDaemonConfigFnNilAndMissingWorkspace(t *testing.T) {
	if BuildStoreBackedDaemonConfigFn(nil) != nil {
		t.Fatal("nil store returned non-nil config fn")
	}
	t.Setenv(bootstrap.EnvWorkspace, "")
	fn := BuildStoreBackedDaemonConfigFn(memstore.New())
	if fn == nil {
		t.Fatal("memstore config fn is nil")
	}
	if _, err := fn(); err == nil {
		t.Fatal("missing workspace config fn error = nil")
	}
}
