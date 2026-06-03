package daemonwire

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
		WorkspaceKey:       "WS1",
		Name:               "task",
		TaskFilter:         "ready",
		Backend:            "codex",
		MaxPriority:        &maxPriority,
		RuntimeProvider:    domain.RuntimeProviderDaytona,
		RuntimeProfileName: "daytona-agent",
		RuntimeCWD:         "/workspace/project",
		RuntimeDaytona: map[string]any{
			"repo_url":           "https://github.com/acme/project.git",
			"api_key_env":        "DAYTONA_API_KEY",
			"openai_api_key_env": "OPENAI_API_KEY",
			"env_vars": map[string]any{
				"OPENAI_API_KEY": "sk-inline-secret",
				"PLAIN_FLAG":     "1",
			},
			"nested": map[string]any{
				"token": "nested-token-secret",
			},
		},
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
	role, ok := got.Roles["task"]
	if !ok || role.TaskFilter != "ready" || role.Backend != "codex" {
		t.Fatalf("role task = %+v, ok=%v", role, ok)
	}
	if role.RuntimeProvider != domain.RuntimeProviderDaytona ||
		role.RuntimeProfileName != "daytona-agent" ||
		role.RuntimeCWD != "/workspace/project" {
		t.Fatalf("role Daytona runtime = provider:%q profile:%q cwd:%q", role.RuntimeProvider, role.RuntimeProfileName, role.RuntimeCWD)
	}
	if role.RuntimeDaytona["repo_url"] != "https://github.com/acme/project.git" ||
		role.RuntimeDaytona["api_key_env"] != "DAYTONA_API_KEY" ||
		role.RuntimeDaytona["openai_api_key_env"] != "OPENAI_API_KEY" {
		t.Fatalf("role Daytona runtime config = %+v, want non-secret selectors preserved", role.RuntimeDaytona)
	}
	envVars, ok := role.RuntimeDaytona["env_vars"].(map[string]any)
	if !ok {
		t.Fatalf("role Daytona env_vars = %#v, want map", role.RuntimeDaytona["env_vars"])
	}
	if envVars["OPENAI_API_KEY"] != "[redacted]" || envVars["PLAIN_FLAG"] != "1" {
		t.Fatalf("role Daytona env_vars = %+v, want only secret env value redacted", envVars)
	}
	nested, ok := role.RuntimeDaytona["nested"].(map[string]any)
	if !ok || nested["token"] != "[redacted]" {
		t.Fatalf("role Daytona nested config = %+v, want secret-looking key redacted", role.RuntimeDaytona["nested"])
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

func TestRedactDaemonConfigSecretsRedactsLocalRuntimeDaytonaValues(t *testing.T) {
	cfg := &config.DaemonConfig{
		Roles: map[string]config.RoleConfig{
			"task": {
				RuntimeProvider:    domain.RuntimeProviderDaytona,
				RuntimeProfileName: "daytona-local",
				RuntimeDaytona: map[string]any{
					"repo_url":           "https://github.com/acme/project.git",
					"api_key_env":        "DAYTONA_API_KEY",
					"openai_api_key_env": "OPENAI_API_KEY",
					"env_vars": map[string]string{
						"OPENAI_API_KEY": "sk-local-secret",
						"SAFE_FLAG":      "true",
					},
					"api_key": "direct-daytona-secret",
				},
			},
		},
	}

	redactDaemonConfigSecrets(cfg)

	role := cfg.Roles["task"]
	if role.RuntimeDaytona["repo_url"] != "https://github.com/acme/project.git" ||
		role.RuntimeDaytona["api_key_env"] != "DAYTONA_API_KEY" ||
		role.RuntimeDaytona["openai_api_key_env"] != "OPENAI_API_KEY" {
		t.Fatalf("runtime_daytona = %+v, want non-secret selectors preserved", role.RuntimeDaytona)
	}
	envVars, ok := role.RuntimeDaytona["env_vars"].(map[string]any)
	if !ok {
		t.Fatalf("env_vars = %#v, want redacted map", role.RuntimeDaytona["env_vars"])
	}
	if envVars["OPENAI_API_KEY"] != "[redacted]" || envVars["SAFE_FLAG"] != "true" {
		t.Fatalf("env_vars = %+v, want only secret env value redacted", envVars)
	}
	if role.RuntimeDaytona["api_key"] != "[redacted]" {
		t.Fatalf("runtime_daytona api_key = %q, want redacted", role.RuntimeDaytona["api_key"])
	}
}
