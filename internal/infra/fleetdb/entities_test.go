package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestFleetDBAgentStoreLifecycle(t *testing.T) {
	now := time.Now().UTC()
	requests := make([]string, 0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agents":
			body := decodeBody(t, r)
			if body["name"] != "agent-1" ||
				body["role_name"] != "task" ||
				body["auto"] != true ||
				body["backend"] != "codex" ||
				body["cross_repo"] != true ||
				body["mode"] != "service" ||
				body["desired_state"] != "running" ||
				body["max_concurrency"] != float64(3) {
				t.Fatalf("agent create body = %#v", body)
			}
			writeJSON(t, w, agentWire{
				WorkspaceKey: "WS", Name: "agent-1", RoleName: "task",
				Auto: true, Backend: "codex", FallbackBackends: []string{"claude"},
				Repos: []string{"repo"}, RepoGroups: []string{"group"}, CrossRepo: true,
				Parent: "lead", State: "active", Mode: "service", TaskFilter: "priority:high",
				MaxConcurrency: 3, BudgetPolicy: "strict", DesiredState: "running",
				CreatedAt: now, UpdatedAt: now,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agents/agent-1":
			writeJSON(t, w, agentWire{WorkspaceKey: "WS", Name: "agent-1", RoleName: "task", State: "idle"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agents":
			writeJSON(t, w, map[string]any{"agents": []agentWire{{WorkspaceKey: "WS", Name: "agent-1", RoleName: "task"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/agents/agent-1":
			body := decodeBody(t, r)
			if body["role_name"] != "lead" ||
				body["auto"] != false ||
				body["backend"] != "claude" ||
				body["cross_repo"] != false ||
				body["state"] != "stopped" ||
				body["mode"] != "ephemeral" ||
				body["desired_state"] != "draining" ||
				body["max_concurrency"] != float64(1) {
				t.Fatalf("agent patch body = %#v", body)
			}
			writeJSON(t, w, agentWire{WorkspaceKey: "WS", Name: "agent-1", RoleName: "lead", State: "stopped"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/agents/agent-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client := newFleetTestClient(t, ts.URL)
	created, err := client.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS", Name: "agent-1", RoleName: "task", Auto: true,
		Backend: "codex", FallbackBackends: []string{"claude"}, Repos: []string{"repo"},
		RepoGroups: []string{"group"}, CrossRepo: true, Parent: "lead",
		Mode: domain.AgentModeService, TaskFilter: "priority:high", MaxConcurrency: 3,
		BudgetPolicy: "strict", DesiredState: domain.AgentDesiredRunning,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if created.Mode != domain.AgentModeService || created.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("created agent = %+v", created)
	}
	if got, err := client.Agents().Get(t.Context(), "WS", "agent-1"); err != nil || got.Name != "agent-1" {
		t.Fatalf("get agent = %+v err=%v", got, err)
	}
	if got, err := client.Agents().List(t.Context(), "WS"); err != nil || len(got) != 1 {
		t.Fatalf("list agents = %+v err=%v", got, err)
	}

	roleName := "lead"
	auto := false
	backend := "claude"
	fallbacks := []string{"codex"}
	repos := []string{"repo-2"}
	groups := []string{"group-2"}
	crossRepo := false
	parent := "parent-2"
	state := domain.AgentStateStopped
	mode := domain.AgentModeEphemeral
	taskFilter := "type:bug"
	maxConcurrency := 1
	budgetPolicy := "open"
	desiredState := domain.AgentDesiredDraining
	if _, err := client.Agents().Update(t.Context(), "WS", "agent-1", store.AgentUpdate{
		RoleName: &roleName, Auto: &auto, Backend: &backend, FallbackBackends: &fallbacks,
		Repos: &repos, RepoGroups: &groups, CrossRepo: &crossRepo, Parent: &parent,
		State: &state, Mode: &mode, TaskFilter: &taskFilter, MaxConcurrency: &maxConcurrency,
		BudgetPolicy: &budgetPolicy, DesiredState: &desiredState,
	}); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if _, err := client.Agents().Update(t.Context(), "WS", "agent-1", store.AgentUpdate{}); err != nil {
		t.Fatalf("empty update should fall back to get: %v", err)
	}
	if err := client.Agents().Delete(t.Context(), "WS", "agent-1"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if got := requests[len(requests)-2]; got != "GET /api/v1/WS/agents/agent-1" {
		t.Fatalf("empty update did not short-circuit to get, requests=%v", requests)
	}
}

func TestFleetDBRoleStoreLifecycle(t *testing.T) {
	maxPriority := 5
	maxConcurrency := 2
	maxBudget := 12.5
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/roles":
			body := decodeBody(t, r)
			if body["name"] != "lead" ||
				body["description"] != "Lead role" ||
				body["max_priority"] != float64(5) ||
				body["max_concurrency"] != float64(2) ||
				body["max_budget_usd"] != 12.5 ||
				body["read_only"] != true {
				t.Fatalf("role create body = %#v", body)
			}
			writeJSON(t, w, roleWire{
				WorkspaceKey: "WS", Name: "lead", Description: "Lead role",
				PromptFile: "lead.md", Model: "gpt", TaskFilter: "kind:plan",
				Backend: "codex", PathPatterns: []string{"*.go"}, Skills: []string{"test"},
				MaxPriority: &maxPriority, MaxConcurrency: &maxConcurrency, ReadOnly: true,
				AllowedTools: []string{"go"}, DeniedTools: []string{"rm"}, MaxBudgetUSD: &maxBudget,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/roles/lead":
			writeJSON(t, w, roleWire{WorkspaceKey: "WS", Name: "lead"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/roles":
			writeJSON(t, w, map[string]any{"roles": []roleWire{{WorkspaceKey: "WS", Name: "lead"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/roles/lead":
			body := decodeBody(t, r)
			if body["description"] != "updated" ||
				body["prompt_file"] != "task.md" ||
				body["model"] != "small" ||
				body["task_filter"] != "kind:task" ||
				body["backend"] != "local" ||
				body["max_priority"] != float64(8) ||
				body["clear_concurrency"] != true ||
				body["clear_max_budget_usd"] != true ||
				body["read_only"] != false {
				t.Fatalf("role patch body = %#v", body)
			}
			writeJSON(t, w, roleWire{WorkspaceKey: "WS", Name: "lead", Description: "updated"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/roles/lead":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client := newFleetTestClient(t, ts.URL)
	created, err := client.Roles().Create(t.Context(), store.RoleCreate{
		WorkspaceKey: "WS", Name: "lead", Description: "Lead role",
		PromptFile: "lead.md", Model: "gpt", TaskFilter: "kind:plan", Backend: "codex",
		PathPatterns: []string{"*.go"}, Skills: []string{"test"}, MaxPriority: &maxPriority,
		MaxConcurrency: &maxConcurrency, ReadOnly: true, AllowedTools: []string{"go"},
		DeniedTools: []string{"rm"}, MaxBudgetUSD: &maxBudget,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if created.MaxPriority == nil || *created.MaxPriority != 5 || !created.ReadOnly {
		t.Fatalf("created role = %+v", created)
	}
	if _, err := client.Roles().Get(t.Context(), "WS", "lead"); err != nil {
		t.Fatalf("get role: %v", err)
	}
	if got, err := client.Roles().List(t.Context(), "WS"); err != nil || len(got) != 1 {
		t.Fatalf("list roles = %+v err=%v", got, err)
	}

	description := "updated"
	promptFile := "task.md"
	model := "small"
	taskFilter := "kind:task"
	backend := "local"
	pathPatterns := []string{"internal/**"}
	skills := []string{"review"}
	newPriority := 8
	priorityPatch := &newPriority
	var clearConcurrency *int
	readOnly := false
	allowedTools := []string{"git"}
	deniedTools := []string{"curl"}
	var clearBudget *float64
	if _, err := client.Roles().Update(t.Context(), "WS", "lead", store.RoleUpdate{
		Description: &description, PromptFile: &promptFile, Model: &model,
		TaskFilter: &taskFilter, Backend: &backend, PathPatterns: &pathPatterns,
		Skills: &skills, MaxPriority: &priorityPatch, MaxConcurrency: &clearConcurrency,
		ReadOnly: &readOnly, AllowedTools: &allowedTools, DeniedTools: &deniedTools,
		MaxBudgetUSD: &clearBudget,
	}); err != nil {
		t.Fatalf("update role: %v", err)
	}
	if err := client.Roles().Delete(t.Context(), "WS", "lead"); err != nil {
		t.Fatalf("delete role: %v", err)
	}
}

func TestFleetDBRepoStoreLifecycleAndRollback(t *testing.T) {
	var createRequests int
	var rollbackRequests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/repos":
			createRequests++
			body := decodeBody(t, r)
			if body["name"] != "repo" || body["remote_url"] != "git@example.com:repo.git" ||
				body["remote"] != "origin" || body["default_branch"] != "main" ||
				body["source_repo_id"] != "source" {
				t.Fatalf("repo create body = %#v", body)
			}
			writeJSON(t, w, repoWire{WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:repo.git", Remote: "origin", DefaultBranch: "main", Groups: []string{"core"}, SourceRepoID: "source"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/WS" && createRequests == 1:
			body := decodeBody(t, r)
			repos, ok := body["add_repos"].([]any)
			if !ok {
				repos, ok = body["del_repos"].([]any)
			}
			if !ok || len(repos) != 1 || repos[0] != "repo" {
				t.Fatalf("workspace repo membership body = %#v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/repos/repo":
			writeJSON(t, w, repoWire{WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:repo.git"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/repos":
			writeJSON(t, w, map[string]any{"repos": []repoWire{{WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:repo.git"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/repos/repo":
			body := decodeBody(t, r)
			if body["remote_url"] != "git@example.com:new.git" ||
				body["remote"] != "upstream" ||
				body["default_branch"] != "trunk" ||
				body["source_repo_id"] != "source-2" {
				t.Fatalf("repo patch body = %#v", body)
			}
			writeJSON(t, w, repoWire{WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:new.git", Remote: "upstream", DefaultBranch: "trunk", Groups: []string{"next"}, SourceRepoID: "source-2"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/repos/repo" && createRequests == 1:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/WS" && createRequests == 2:
			http.Error(w, "admin update failed", http.StatusInternalServerError)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/repos/repo" && createRequests == 2:
			rollbackRequests++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s createRequests=%d", r.Method, r.URL.String(), createRequests)
		}
	}))
	defer ts.Close()

	client := newFleetTestClient(t, ts.URL)
	if _, err := client.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:repo.git",
		Remote: "origin", DefaultBranch: "main", Groups: []string{"core"}, SourceRepoID: "source",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := client.Repos().Get(t.Context(), "WS", "repo"); err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if got, err := client.Repos().List(t.Context(), "WS"); err != nil || len(got) != 1 {
		t.Fatalf("list repos = %+v err=%v", got, err)
	}
	remoteURL := "git@example.com:new.git"
	remote := "upstream"
	defaultBranch := "trunk"
	groups := []string{"next"}
	sourceRepoID := "source-2"
	if _, err := client.Repos().Update(t.Context(), "WS", "repo", store.RepoUpdate{
		RemoteURL: &remoteURL, Remote: &remote, DefaultBranch: &defaultBranch,
		Groups: &groups, SourceRepoID: &sourceRepoID,
	}); err != nil {
		t.Fatalf("update repo: %v", err)
	}
	if err := client.Repos().Delete(t.Context(), "WS", "repo"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if _, err := client.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "WS", Name: "repo", RemoteURL: "git@example.com:repo.git",
		Remote: "origin", DefaultBranch: "main", Groups: []string{"core"}, SourceRepoID: "source",
	}); err == nil {
		t.Fatal("create repo with failed workspace admin update returned nil error")
	}
	if rollbackRequests != 1 {
		t.Fatalf("rollbackRequests = %d, want 1", rollbackRequests)
	}
}

func TestFleetDBWorkspaceStoreLifecycleAndFallbacks(t *testing.T) {
	requests := make([]string, 0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/workspaces":
			body := decodeBody(t, r)
			if body["key"] != "WS" || body["name"] != "Workspace" || body["description"] != "desc" {
				t.Fatalf("workspace create body = %#v", body)
			}
			writeJSON(t, w, workspaceWire{Key: "WS", Name: "Workspace", Description: "desc", State: "creating"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/WS":
			writeJSON(t, w, workspaceWire{Key: "WS", Name: "Workspace 2", Description: "desc", State: "ready", ErrorMessage: "none", DefaultBranch: "trunk"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces":
			writeJSON(t, w, map[string]any{"workspaces": []workspaceWire{
				{Key: "OTHER", Name: "Other"},
				{Key: "WS", Name: "Workspace", State: "ready", DefaultBranch: "main"},
			}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/WS":
			body := decodeBody(t, r)
			if body["name"] != "Workspace 2" {
				t.Fatalf("workspace patch body = %#v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/admin/workspaces/WS" && r.URL.RawQuery == "force=true":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client := newFleetTestClient(t, ts.URL)
	created, err := client.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key: "WS", Name: "Workspace", Description: "desc", DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if created.DefaultBranch != "main" {
		t.Fatalf("create default branch fallback = %q", created.DefaultBranch)
	}
	if got, err := client.Workspaces().Get(t.Context(), "WS"); err != nil || got.State != domain.WorkspaceStateReady {
		t.Fatalf("get workspace = %+v err=%v", got, err)
	}
	if got, err := client.Workspaces().List(t.Context()); err != nil || len(got) != 2 {
		t.Fatalf("list workspaces = %+v err=%v", got, err)
	}
	if got, err := client.Workspaces().GetByName(t.Context(), "Workspace"); err != nil || got.Key != "WS" {
		t.Fatalf("get by name = %+v err=%v", got, err)
	}
	if _, err := client.Workspaces().GetByName(t.Context(), "Missing"); err == nil {
		t.Fatal("missing workspace name returned nil error")
	}

	description := "ignored"
	defaultBranch := "ignored"
	state := domain.WorkspaceStateError
	errorMessage := "ignored"
	if _, err := client.Workspaces().Update(t.Context(), "WS", store.WorkspaceUpdate{
		Description: &description, DefaultBranch: &defaultBranch, State: &state, ErrorMessage: &errorMessage,
	}); err != nil {
		t.Fatalf("non-name update should fall back to get: %v", err)
	}
	name := "Workspace 2"
	if _, err := client.Workspaces().Update(t.Context(), "WS", store.WorkspaceUpdate{Name: &name}); err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if err := client.Workspaces().Delete(t.Context(), "WS"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if requests[len(requests)-4] != "GET /api/v1/admin/workspaces/WS" {
		t.Fatalf("non-name update did not fall back to get, requests=%v", requests)
	}
}

func TestFleetDBDaemonStoreAndWireMapping(t *testing.T) {
	maxRetries := 4
	backoffInitial := 3
	backoffMax := 30
	maxAgents := 9
	startupTimeout := 45
	enabled := true
	sampleRate := 0.75
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/daemon":
			writeJSON(t, w, daemonProfileWire{
				WorkspaceKey: "WS", PIDFile: "/tmp/pid", LogDir: "/tmp/logs", EventsDir: "/tmp/events",
				RestartPolicy: &fleetRestartPolicyWire{
					MaxRetries: &maxRetries, BackoffInitial: &backoffInitial, BackoffMax: &backoffMax,
				},
				MaxAgents: &maxAgents, IssueBackend: "github", AgentBackend: "codex",
				StartupTimeout: &startupTimeout,
				OTel: &fleetOTelWire{
					Enabled: &enabled, Endpoint: "http://otel", ServiceName: "loom",
					SampleRate: &sampleRate,
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/WS/daemon":
			body := decodeBody(t, r)
			rp, ok := body["restart_policy"].(map[string]any)
			if !ok || rp["max_retries"] != float64(4) || rp["backoff_initial"] != float64(3) || rp["backoff_max"] != float64(30) {
				t.Fatalf("daemon restart policy body = %#v", body)
			}
			otel, ok := body["otel"].(map[string]any)
			if !ok || otel["enabled"] != true || otel["endpoint"] != "http://otel" || otel["sample_rate"] != 0.75 {
				t.Fatalf("daemon otel body = %#v", body)
			}
			writeJSON(t, w, daemonProfileWire{WorkspaceKey: "WS", MaxAgents: &maxAgents, OTel: &fleetOTelWire{Enabled: &enabled}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client := newFleetTestClient(t, ts.URL)
	got, err := client.Daemon().Get(t.Context(), "WS")
	if err != nil {
		t.Fatalf("get daemon: %v", err)
	}
	if got.RestartPolicy.MaxRetries == nil || *got.RestartPolicy.MaxRetries != 4 ||
		got.OTel == nil || !got.OTel.Enabled || got.OTel.ServiceName != "loom" {
		t.Fatalf("daemon profile = %+v", got)
	}
	unmappedTimeout := 99
	out, err := client.Daemon().Upsert(t.Context(), &domain.DaemonProfile{
		WorkspaceKey: "WS", PIDFile: "/tmp/pid", LogDir: "/tmp/logs", EventsDir: "/tmp/events",
		MaxAgents: &maxAgents, IssueBackend: "github", AgentBackend: "codex",
		StartupTimeout: &startupTimeout,
		RestartPolicy: domain.RestartPolicy{
			MaxRetries: &maxRetries, BackoffInitial: &backoffInitial, BackoffMax: &backoffMax,
			OutputTimeout: &unmappedTimeout,
		},
		OTel: &domain.OTelSettings{
			Enabled: true, Endpoint: "http://otel", ServiceName: "loom", SampleRate: 0.75,
		},
	})
	if err != nil {
		t.Fatalf("upsert daemon: %v", err)
	}
	if out.MaxAgents == nil || *out.MaxAgents != 9 {
		t.Fatalf("upserted daemon = %+v", out)
	}
	if hasRestartPolicy(domain.RestartPolicy{OutputTimeout: &unmappedTimeout}) {
		t.Fatal("hasRestartPolicy returned true for unmapped-only policy")
	}
}

func newFleetTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: baseURL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
