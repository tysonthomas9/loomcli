package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentStoreCRUDCloneAndPatch(t *testing.T) {
	st := New()
	ctx := t.Context()
	agents := st.Agents()

	if _, err := agents.Create(ctx, store.AgentCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	created, err := agents.Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "nova", RoleName: "task", Auto: true,
		Backend: "codex", FallbackBackends: []string{"claude"}, Repos: []string{"repo-a"},
		RepoGroups: []string{"frontend"}, CrossRepo: true, Parent: "epic-1",
		Mode: domain.AgentModeService, TaskFilter: "has_design", MaxConcurrency: 2,
		BudgetPolicy: "standard", DesiredState: domain.AgentDesiredRunning,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if created.State != domain.AgentStateIdle || created.CreatedAt.IsZero() || created.FallbackBackends[0] != "claude" {
		t.Fatalf("created agent = %+v", created)
	}
	if _, err := agents.Create(ctx, store.AgentCreate{WorkspaceKey: "WS", Name: "nova", RoleName: "task"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate create: want ErrAlreadyExists, got %v", err)
	}

	got, err := agents.Get(ctx, "WS", "nova")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	got.Repos[0] = "mutated"
	again, _ := agents.Get(ctx, "WS", "nova")
	if again.Repos[0] != "repo-a" {
		t.Fatalf("get returned mutable repo slice: %+v", again.Repos)
	}

	list, err := agents.List(ctx, "WS")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "nova" {
		t.Fatalf("list agents = %+v", list)
	}

	role := "plan"
	auto := false
	backend := "claude"
	fallbacks := []string{"codex", "opencode"}
	repos := []string{"repo-b"}
	repoGroups := []string{"backend"}
	crossRepo := false
	parent := "epic-2"
	state := domain.AgentStateActive
	mode := domain.AgentModeEphemeral
	taskFilter := "needs_design"
	maxConcurrency := 3
	budgetPolicy := "tight"
	desired := domain.AgentDesiredIdle
	updated, err := agents.Update(ctx, "WS", "nova", store.AgentUpdate{
		RoleName: &role, Auto: &auto, Backend: &backend, FallbackBackends: &fallbacks,
		Repos: &repos, RepoGroups: &repoGroups, CrossRepo: &crossRepo, Parent: &parent,
		State: &state, Mode: &mode, TaskFilter: &taskFilter, MaxConcurrency: &maxConcurrency,
		BudgetPolicy: &budgetPolicy, DesiredState: &desired,
	})
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}
	fallbacks[0] = "mutated"
	if updated.RoleName != "plan" || updated.Auto || updated.Backend != "claude" ||
		updated.FallbackBackends[0] != "codex" || updated.Repos[0] != "repo-b" ||
		updated.RepoGroups[0] != "backend" || updated.CrossRepo ||
		updated.Parent != "epic-2" || updated.State != domain.AgentStateActive ||
		updated.Mode != domain.AgentModeEphemeral || updated.TaskFilter != "needs_design" ||
		updated.MaxConcurrency != 3 || updated.BudgetPolicy != "tight" ||
		updated.DesiredState != domain.AgentDesiredIdle {
		t.Fatalf("updated agent = %+v", updated)
	}
	if _, err := agents.Update(ctx, "WS", "missing", store.AgentUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
	if err := agents.Delete(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	if err := agents.Delete(ctx, "WS", "nova"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
}

func TestRoleStoreCRUDCloneAndPatch(t *testing.T) {
	st := New()
	ctx := t.Context()
	roles := st.Roles()

	if _, err := roles.Create(ctx, store.RoleCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	maxPriority := 1
	maxConcurrency := 2
	maxBudget := 3.5
	created, err := roles.Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS", Name: "task", Description: "desc", PromptFile: "task.md",
		Model: "gpt-5", TaskFilter: "has_design", Backend: "codex",
		PathPatterns: []string{"internal/**"}, Skills: []string{"go"}, MaxPriority: &maxPriority,
		MaxConcurrency: &maxConcurrency, ReadOnly: true, AllowedTools: []string{"go"},
		DeniedTools: []string{"rm"}, MaxBudgetUSD: &maxBudget,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if created.CreatedAt.IsZero() || created.MaxPriority == nil || *created.MaxPriority != 1 {
		t.Fatalf("created role = %+v", created)
	}
	if _, err := roles.Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate create: want ErrAlreadyExists, got %v", err)
	}

	got, err := roles.Get(ctx, "WS", "task")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	got.PathPatterns[0] = "mutated"
	again, _ := roles.Get(ctx, "WS", "task")
	if again.PathPatterns[0] != "internal/**" {
		t.Fatalf("get returned mutable path slice: %+v", again.PathPatterns)
	}
	list, err := roles.List(ctx, "WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(list) != 1 || list[0].Name != "task" {
		t.Fatalf("list roles = %+v", list)
	}

	description := "updated"
	promptFile := "updated.md"
	model := "gpt-5.1"
	taskFilter := "needs_design"
	backend := "claude"
	paths := []string{"cmd/**"}
	skills := []string{"planning"}
	newPriority := 4
	newPriorityPtr := &newPriority
	newConcurrency := 5
	newConcurrencyPtr := &newConcurrency
	readOnly := false
	allowed := []string{"git"}
	denied := []string{"curl"}
	newBudget := 9.25
	newBudgetPtr := &newBudget
	updated, err := roles.Update(ctx, "WS", "task", store.RoleUpdate{
		Description: &description, PromptFile: &promptFile, Model: &model,
		TaskFilter: &taskFilter, Backend: &backend, PathPatterns: &paths, Skills: &skills,
		MaxPriority: &newPriorityPtr, MaxConcurrency: &newConcurrencyPtr, ReadOnly: &readOnly,
		AllowedTools: &allowed, DeniedTools: &denied, MaxBudgetUSD: &newBudgetPtr,
	})
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	paths[0] = "mutated"
	if updated.Description != "updated" || updated.PromptFile != "updated.md" ||
		updated.Model != "gpt-5.1" || updated.TaskFilter != "needs_design" ||
		updated.Backend != "claude" || updated.PathPatterns[0] != "cmd/**" ||
		updated.Skills[0] != "planning" || updated.MaxPriority == nil ||
		*updated.MaxPriority != 4 || updated.MaxConcurrency == nil ||
		*updated.MaxConcurrency != 5 || updated.ReadOnly ||
		updated.AllowedTools[0] != "git" || updated.DeniedTools[0] != "curl" ||
		updated.MaxBudgetUSD == nil || *updated.MaxBudgetUSD != 9.25 {
		t.Fatalf("updated role = %+v", updated)
	}
	if _, err := roles.Update(ctx, "WS", "missing", store.RoleUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
	if err := roles.Delete(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	if err := roles.Delete(ctx, "WS", "task"); err != nil {
		t.Fatalf("delete role: %v", err)
	}
}

func TestRepoStoreFullPatchAndDelete(t *testing.T) {
	st := New()
	ctx := t.Context()
	repos := st.Repos()

	if _, err := repos.Create(ctx, store.RepoCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	created, err := repos.Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS", Name: "app", RemoteURL: "git@example.com:app.git",
		Remote: "origin", DefaultBranch: "main", Groups: []string{"frontend"},
		SourceRepoID: "src-app",
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if created.SourceRepoID != "src-app" || created.CreatedAt.IsZero() {
		t.Fatalf("created repo = %+v", created)
	}
	if _, err := repos.Get(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
	got, _ := repos.Get(ctx, "WS", "app")
	got.Groups[0] = "mutated"
	again, _ := repos.Get(ctx, "WS", "app")
	if again.Groups[0] != "frontend" {
		t.Fatalf("get returned mutable groups slice: %+v", again.Groups)
	}

	remoteURL := "git@example.com:new.git"
	remote := "upstream"
	defaultBranch := "develop"
	groups := []string{"backend"}
	sourceRepoID := "src-new"
	updated, err := repos.Update(ctx, "WS", "app", store.RepoUpdate{
		RemoteURL: &remoteURL, Remote: &remote, DefaultBranch: &defaultBranch,
		Groups: &groups, SourceRepoID: &sourceRepoID,
	})
	if err != nil {
		t.Fatalf("update repo: %v", err)
	}
	groups[0] = "mutated"
	if updated.RemoteURL != remoteURL || updated.Remote != remote ||
		updated.DefaultBranch != defaultBranch || updated.Groups[0] != "backend" ||
		updated.SourceRepoID != sourceRepoID {
		t.Fatalf("updated repo = %+v", updated)
	}
	if _, err := repos.Update(ctx, "WS", "missing", store.RepoUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
	if err := repos.Delete(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	if err := repos.Delete(ctx, "WS", "app"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
}

func TestDaemonProfileUpsertClonesNestedPointers(t *testing.T) {
	st := New()
	ctx := t.Context()
	daemons := st.Daemon()

	if _, err := daemons.Get(ctx, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("get empty workspace: want ErrInvalid, got %v", err)
	}
	if _, err := daemons.Upsert(ctx, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("upsert nil: want ErrInvalid, got %v", err)
	}
	if _, err := daemons.Upsert(ctx, &domain.DaemonProfile{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("upsert empty workspace: want ErrInvalid, got %v", err)
	}

	maxRetries := 2
	maxAgents := 4
	startupTimeout := 30
	traces := true
	profile, err := daemons.Upsert(ctx, &domain.DaemonProfile{
		WorkspaceKey: "WS", PIDFile: "/tmp/loom.pid", LogDir: "/tmp/logs",
		EventsDir: "/tmp/events", IssueBackend: "fleetdb", AgentBackend: "codex",
		MaxAgents: &maxAgents, StartupTimeout: &startupTimeout,
		RestartPolicy: domain.RestartPolicy{MaxRetries: &maxRetries},
		OTel:          &domain.OTelSettings{Enabled: true, Endpoint: "localhost:4317", Traces: &traces},
	})
	if err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if profile.UpdatedAt.IsZero() || profile.MaxAgents == nil || *profile.MaxAgents != 4 ||
		profile.RestartPolicy.MaxRetries == nil || *profile.RestartPolicy.MaxRetries != 2 ||
		profile.OTel == nil || profile.OTel.Traces == nil || !*profile.OTel.Traces {
		t.Fatalf("upserted profile = %+v", profile)
	}

	*profile.MaxAgents = 99
	*profile.RestartPolicy.MaxRetries = 99
	*profile.OTel.Traces = false
	got, err := daemons.Get(ctx, "WS")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if *got.MaxAgents != 4 || *got.RestartPolicy.MaxRetries != 2 || !*got.OTel.Traces {
		t.Fatalf("get returned mutable profile aliases: %+v", got)
	}
}

func TestNodeStoreFullLifecycle(t *testing.T) {
	st := New()
	ctx := t.Context()
	nodes := st.Nodes()

	if _, err := nodes.Create(ctx, store.NodeCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	node, err := nodes.Create(ctx, store.NodeCreate{
		WorkspaceKey: "WS", NodeID: "node-1", OwnerActor: "owner",
		RuntimeProvider: domain.RuntimeProviderLocal, Labels: []string{"a"},
		Capabilities: []string{"shell"}, ToolInventory: []string{"git"},
		Version: "v1", Capacity: 2, DrainState: domain.NodeDrainActive,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.ExpiresAt.IsZero() || node.Labels[0] != "a" {
		t.Fatalf("created node = %+v", node)
	}
	node.Labels[0] = "mutated"
	got, _ := nodes.Get(ctx, "WS", "node-1")
	if got.Labels[0] != "a" {
		t.Fatalf("get returned mutable labels: %+v", got.Labels)
	}

	owner := "new-owner"
	provider := domain.RuntimeProviderCI
	labels := []string{"b"}
	capabilities := []string{"pty"}
	tools := []string{"go"}
	version := "v2"
	capacity := 5
	drain := domain.NodeDrainDraining
	expires := time.Now().UTC().Add(time.Hour)
	updated, err := nodes.Update(ctx, "WS", "node-1", store.NodeUpdate{
		OwnerActor: &owner, RuntimeProvider: &provider, Labels: &labels,
		Capabilities: &capabilities, ToolInventory: &tools, Version: &version,
		Capacity: &capacity, DrainState: &drain, ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatalf("update node: %v", err)
	}
	labels[0] = "mutated"
	if updated.OwnerActor != "new-owner" || updated.RuntimeProvider != domain.RuntimeProviderCI ||
		updated.Labels[0] != "b" || updated.Capabilities[0] != "pty" ||
		updated.ToolInventory[0] != "go" || updated.Version != "v2" ||
		updated.Capacity != 5 || updated.DrainState != domain.NodeDrainDraining ||
		!updated.ExpiresAt.Equal(expires) {
		t.Fatalf("updated node = %+v", updated)
	}
	beat, err := nodes.Heartbeat(ctx, "WS", "node-1", time.Minute)
	if err != nil {
		t.Fatalf("heartbeat node: %v", err)
	}
	if !beat.LastHeartbeat.After(updated.LastHeartbeat) {
		t.Fatalf("heartbeat did not move last heartbeat: before=%s after=%s", updated.LastHeartbeat, beat.LastHeartbeat)
	}
	list, err := nodes.List(ctx, "WS")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(list) != 1 || list[0].NodeID != "node-1" {
		t.Fatalf("list nodes = %+v", list)
	}
	if _, err := nodes.Heartbeat(ctx, "WS", "missing", time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("heartbeat missing: want ErrNotFound, got %v", err)
	}
	if _, err := nodes.Update(ctx, "WS", "missing", store.NodeUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestAgentSessionStoreFullLifecycle(t *testing.T) {
	st := New()
	ctx := t.Context()
	sessions := st.AgentSessions()

	if _, err := sessions.Create(ctx, store.AgentSessionCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	session, err := sessions.Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", Kind: domain.AgentSessionKindTask, TaskID: "task-1",
		TerminalID: "term-1", ParentSessionID: "parent-1",
		Status: domain.AgentSessionQueued, Phase: "queued", Attempt: 1,
		Metadata: map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.CreatedAt.IsZero() || session.Metadata["source"] != "test" {
		t.Fatalf("created session = %+v", session)
	}
	if _, err := sessions.Create(ctx, store.AgentSessionCreate{WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate create: want ErrAlreadyExists, got %v", err)
	}
	session.Metadata["source"] = "mutated"
	got, _ := sessions.Get(ctx, "WS", "session-1")
	if got.Metadata["source"] != "test" {
		t.Fatalf("get returned mutable metadata: %+v", got.Metadata)
	}

	filtered, err := sessions.List(ctx, "WS", store.AgentSessionFilter{
		AgentID: "agent-1", NodeID: "node-1", TaskID: "task-1",
		Status: domain.AgentSessionQueued, Kind: domain.AgentSessionKindTask,
		ParentSessionID: "parent-1", Limit: 1,
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "session-1" {
		t.Fatalf("filtered list = %+v", filtered)
	}

	nodeID := "node-2"
	taskID := "task-2"
	status := domain.AgentSessionCompleted
	phase := "done"
	lastHeartbeat := time.Now().UTC()
	finished := lastHeartbeat.Add(time.Minute)
	finishedPtr := &finished
	summary := "completed"
	errorClass := "none"
	exitCode := 0
	exitCodePtr := &exitCode
	meta := map[string]string{"source": "updated"}
	updated, err := sessions.Update(ctx, "WS", "session-1", store.AgentSessionUpdate{
		NodeID: &nodeID, TaskID: &taskID, Status: &status, Phase: &phase,
		LastHeartbeat: &lastHeartbeat, FinishedAt: &finishedPtr, Summary: &summary,
		ErrorClass: &errorClass, ExitCode: &exitCodePtr, Metadata: &meta,
	})
	if err != nil {
		t.Fatalf("update session: %v", err)
	}
	meta["source"] = "mutated"
	if updated.NodeID != "node-2" || updated.TaskID != "task-2" ||
		updated.Status != domain.AgentSessionCompleted || updated.Phase != "done" ||
		updated.FinishedAt == nil || updated.Summary != "completed" ||
		updated.ErrorClass != "none" || updated.ExitCode == nil ||
		*updated.ExitCode != 0 || updated.Metadata["source"] != "updated" {
		t.Fatalf("updated session = %+v", updated)
	}
	beat, err := sessions.Heartbeat(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("heartbeat session: %v", err)
	}
	if !beat.LastHeartbeat.After(lastHeartbeat) {
		t.Fatalf("heartbeat did not update last heartbeat: before=%s after=%s", lastHeartbeat, beat.LastHeartbeat)
	}
	if _, err := sessions.Get(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
	if _, err := sessions.Update(ctx, "WS", "missing", store.AgentSessionUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
}
