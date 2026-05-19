package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func workspaceCoverageConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_ISSUE_BACKEND", "")
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_FLEET_DB_URL", "")
	t.Setenv(envLocalRuntimeMode, "")
	return dir
}

func TestWorkspaceCreateValidationAndRepoParsing(t *testing.T) {
	oldRepos, oldBranch := wsCreateRepos, wsCreateBranch
	t.Cleanup(func() {
		wsCreateRepos, wsCreateBranch = oldRepos, oldBranch
	})

	for _, name := range []string{"alpha", "Alpha_1", "release-2026"} {
		if !isValidWorkspaceName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "bad name", "../escape", "feature/x"} {
		if isValidWorkspaceName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}

	wsCreateBranch = ""
	if got := validateCreateInputs("alpha"); got != "alpha" {
		t.Fatalf("default branch = %q, want alpha", got)
	}
	wsCreateBranch = "feature_1"
	if got := validateCreateInputs("alpha"); got != "feature_1" {
		t.Fatalf("explicit branch = %q", got)
	}

	wsCreateRepos = "/repo/a,/repo/b"
	repos := parseRepoPaths()
	if len(repos) != 2 || repos[0] != "/repo/a" || repos[1] != "/repo/b" {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestWorkspaceLocalConfigAndDeleteState(t *testing.T) {
	workspaceCoverageConfigDir(t)
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "app",
		DefaultBranch: "main",
		Remote:        "origin",
		Groups:        []string{"product"},
		SourceRepoID:  "src-app",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS", Name: "api"}); err != nil {
		t.Fatalf("create repo api: %v", err)
	}

	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {
				Path:  "/workspaces/ws",
				Repos: map[string]string{"app": "/custom/app"},
			},
			"OLD": {Path: "/old"},
		},
		LastWorkspace: "WS",
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	localCfg, err := workspaceLocalConfig(ctx, &bootstrap.StoreHandle{Store: st}, "WS")
	if err != nil {
		t.Fatalf("workspaceLocalConfig: %v", err)
	}
	if localCfg.ID != "WS" || localCfg.Path != "/workspaces/ws" {
		t.Fatalf("local config = %+v", localCfg)
	}
	repoByName := map[string]config.RepoConfig{}
	for _, repo := range localCfg.Repos {
		repoByName[repo.Name] = repo
	}
	if repoByName["app"].Path != "/custom/app" {
		t.Fatalf("app path = %q", repoByName["app"].Path)
	}
	if repoByName["api"].Path != filepath.Join("/workspaces/ws", "api") {
		t.Fatalf("api path = %q", repoByName["api"].Path)
	}
	if repoByName["app"].SourceRepoID != "src-app" {
		t.Fatalf("source repo id = %q", repoByName["app"].SourceRepoID)
	}

	if err := deleteWorkspaceLocalState("WS"); err != nil {
		t.Fatalf("delete local state: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if _, ok := sc.Workspaces["WS"]; ok {
		t.Fatalf("WS still present in state cache: %#v", sc.Workspaces)
	}
	if sc.LastWorkspace != "" {
		t.Fatalf("LastWorkspace = %q, want empty", sc.LastWorkspace)
	}
}

func TestRemoveWorktreesSkipsNonGitReposAndRemovesWorkspaceDir(t *testing.T) {
	root := t.TempDir()
	wsPath := filepath.Join(root, "workspace")
	repoPath := filepath.Join(wsPath, "app")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	removeWorktrees(&cli.Deps{}, config.WorkspaceConfig{
		Path: wsPath,
		Repos: []config.RepoConfig{
			{Name: "app", Path: repoPath},
			{Name: "relative", Path: "relative"},
		},
	})
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace dir still exists or stat error: %v", err)
	}
}

func TestCollectWorkspaceOpsReposRolesAndAgents(t *testing.T) {
	root := t.TempDir()
	plannerWT := filepath.Join(root, "worktrees", "app", "planner")
	explicitWT := filepath.Join(root, "custom", "explicit")
	if err := os.MkdirAll(filepath.Join(plannerWT, ".git"), 0755); err != nil {
		t.Fatalf("mkdir agent git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(explicitWT, ".git"), 0755); err != nil {
		t.Fatalf("mkdir explicit agent git: %v", err)
	}
	localState := bootstrap.WorkspaceLocalState{
		Path: root,
		Repos: map[string]string{
			"app": filepath.Join(root, "app"),
		},
		Agents: map[string]bootstrap.AgentLocalState{
			"explicit": {Worktree: explicitWT},
		},
	}
	status := &WorkspaceOpsStatus{}
	repos := []*domain.Repo{
		{WorkspaceKey: "WS", Name: "app", RemoteURL: "git@example/app", Groups: []string{"product"}, SourceRepoID: "src-app"},
		nil,
		{WorkspaceKey: "WS", Name: "api", RemoteURL: "git@example/api"},
	}
	repoByName := collectOpsRepos(status, repos, localState)
	if len(status.Repos) != 2 || len(repoByName) != 2 {
		t.Fatalf("repos=%+v repoByName=%+v", status.Repos, repoByName)
	}
	if status.Repos[0].Groups[0] != "product" || status.Repos[0].SourceRepo != "src-app" {
		t.Fatalf("repo projection = %+v", status.Repos[0])
	}
	if repoLocalPath(localState, "app") != filepath.Join(root, "app") {
		t.Fatalf("repoLocalPath did not use explicit repo path")
	}

	roleNames := collectRoleNames([]*domain.Role{{Name: "plan"}, nil, {Name: "task"}})
	if _, ok := roleNames["plan"]; !ok {
		t.Fatalf("roleNames = %#v", roleNames)
	}

	agents := []*domain.Agent{
		{Name: "planner", RoleName: "plan", State: domain.AgentStateActive, DesiredState: domain.AgentDesiredRunning, Repos: []string{"app"}},
		{Name: "explicit", RoleName: "task", State: domain.AgentStateActive, DesiredState: domain.AgentDesiredRunning},
		nil,
		{Name: "stopped", RoleName: "task", State: domain.AgentStateActive, DesiredState: domain.AgentDesiredStopped},
	}
	collectOpsAgents(status, agents, localState, repoByName, roleNames)
	if len(status.Agents) != 3 {
		t.Fatalf("agents = %+v", status.Agents)
	}
	if !status.Agents[0].WorktreeReady {
		t.Fatalf("planner should have found generated git worktree: %+v", status.Agents[0])
	}
	if status.Agents[1].WorktreePath != explicitWT || !status.Agents[1].WorktreeReady {
		t.Fatalf("explicit agent = %+v", status.Agents[1])
	}
	if status.Agents[2].Runnable || status.Agents[2].Reason != "desired_state_not_running" {
		t.Fatalf("stopped agent = %+v", status.Agents[2])
	}
}

func TestBuildWorkspaceOpsStatusAndRendering(t *testing.T) {
	workspaceCoverageConfigDir(t)
	root := t.TempDir()
	repoPath := filepath.Join(root, "app")
	agentWT := filepath.Join(root, "worktrees", "app", "planner")
	if err := os.MkdirAll(filepath.Join(agentWT, ".git"), 0755); err != nil {
		t.Fatalf("mkdir worktree git: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {
				Path:  root,
				Repos: map[string]string{"app": repoPath},
			},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	status, err := buildWorkspaceOpsStatus(context.Background(),
		&domain.Workspace{Key: "WS", Name: "Workspace"},
		[]*domain.Repo{{WorkspaceKey: "WS", Name: "app"}},
		[]*domain.Agent{{WorkspaceKey: "WS", Name: "planner", RoleName: "plan", State: domain.AgentStateActive, DesiredState: domain.AgentDesiredRunning, Repos: []string{"app"}}},
		[]*domain.Role{{WorkspaceKey: "WS", Name: "plan"}},
	)
	if err != nil {
		t.Fatalf("buildWorkspaceOpsStatus: %v", err)
	}
	if status.Workspace.State != "ready" || status.Workspace.LocalPath != root {
		t.Fatalf("workspace projection = %+v", status.Workspace)
	}
	if len(status.Repos) != 1 || len(status.Agents) != 1 {
		t.Fatalf("status repos/agents = %+v / %+v", status.Repos, status.Agents)
	}
	if status.OK {
		t.Fatalf("status should include daemon_not_running error while planner is runnable: %+v", status.Problems)
	}
	if !hasErrorProblem(status.Problems) || !statusNeedsDaemonWait(status) {
		t.Fatalf("expected error problem and daemon wait need: %+v", status)
	}

	oldJSON := workspaceOpsJSON
	t.Cleanup(func() { workspaceOpsJSON = oldJSON })

	var human bytes.Buffer
	workspaceOpsJSON = false
	cmd := &cobra.Command{}
	cmd.SetOut(&human)
	if err := renderWorkspaceOpsStatus(cmd, status); err != nil {
		t.Fatalf("render human: %v", err)
	}
	if !strings.Contains(human.String(), "Workspace: WS") || !strings.Contains(human.String(), "Problems:") {
		t.Fatalf("human output = %s", human.String())
	}

	var jsonOut bytes.Buffer
	workspaceOpsJSON = true
	cmd = &cobra.Command{}
	cmd.SetOut(&jsonOut)
	if err := renderWorkspaceOpsStatus(cmd, status); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	var decoded WorkspaceOpsStatus
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, jsonOut.String())
	}
	if decoded.Workspace.Key != "WS" {
		t.Fatalf("decoded workspace = %+v", decoded.Workspace)
	}
}

func TestWorkspaceOpsSmallHelpers(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv(envLocalRuntimeMode, "headless")
	applicable, reason, ok := localRuntimeModeOverride()
	if applicable || !ok || !strings.Contains(reason, "headless") {
		t.Fatalf("headless override = applicable=%t reason=%q ok=%t", applicable, reason, ok)
	}
	t.Setenv(envLocalRuntimeMode, "local")
	applicable, reason, ok = localRuntimeModeOverride()
	if !applicable || reason != "" || !ok {
		t.Fatalf("local override = applicable=%t reason=%q ok=%t", applicable, reason, ok)
	}

	if workspaceStateString(domain.WorkspaceStateCloning) != string(domain.WorkspaceStateCloning) {
		t.Fatalf("workspaceStateString cloning mismatch")
	}
	if daemonHuman(DaemonInfo{Running: true, PID: 55}) != "running(pid=55)" {
		t.Fatalf("daemonHuman running with pid mismatch")
	}
	if daemonHuman(DaemonInfo{Running: true}) != "running" {
		t.Fatalf("daemonHuman running mismatch")
	}
	if daemonHuman(DaemonInfo{StalePID: true}) != "stale" {
		t.Fatalf("daemonHuman stale mismatch")
	}
	if daemonHuman(DaemonInfo{}) != "stopped" {
		t.Fatalf("daemonHuman stopped mismatch")
	}

	runtimeBlock := buildApplicableLocalRuntime(&local.RuntimeStatusSnapshot{
		Healthy: true,
		Runtime: &local.RuntimeSnapshot{PID: 1, URL: "http://127.0.0.1"},
	}, nil)
	seed := newWorkspaceOpsStatus(
		&domain.Workspace{Key: "WS", Name: "Workspace", State: domain.WorkspaceStateReady},
		bootstrap.WorkspaceLocalState{Path: t.TempDir()},
		t.TempDir(),
		&local.RuntimeStatusSnapshot{Healthy: runtimeBlock.Healthy, Runtime: runtimeBlock.Runtime},
		nil,
		1,
		1,
	)
	if seed.LocalRuntime == nil || !seed.LocalRuntime.Applicable || !seed.LocalRuntime.Healthy {
		t.Fatalf("seed local runtime = %+v", seed.LocalRuntime)
	}
	if seed.Daemon.DataDir == "" {
		t.Fatalf("seed data dir should be populated")
	}
}

func TestWorkspaceOpsCommandsAgainstFleetStore(t *testing.T) {
	handle := setupWorkspaceCommandFleetStore(t)
	defer func() { _ = handle.Close() }()
	t.Setenv(envLocalRuntimeMode, "headless")

	ctx := context.Background()
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS",
		Name:         "api",
		RemoteURL:    "git@example/api",
		Remote:       "origin",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := handle.Store.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := handle.Store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredStopped,
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	oldJSON, oldTimeout := workspaceOpsJSON, workspaceOpsTimeoutSec
	t.Cleanup(func() {
		workspaceOpsJSON = oldJSON
		workspaceOpsTimeoutSec = oldTimeout
	})

	status, err := workspaceOpsStatusForArgs([]string{"WS"})
	if err != nil {
		t.Fatalf("workspaceOpsStatusForArgs: %v", err)
	}
	if status.Workspace.Key != "WS" || len(status.Repos) != 1 || len(status.Agents) != 1 {
		t.Fatalf("status = %+v", status)
	}
	if status.LocalRuntime == nil || status.LocalRuntime.Applicable {
		t.Fatalf("headless local runtime = %+v", status.LocalRuntime)
	}

	var human bytes.Buffer
	workspaceOpsJSON = false
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&human)
	if err := runWorkspaceOpsStatus(cmd, []string{"WS"}); err != nil {
		t.Fatalf("runWorkspaceOpsStatus: %v", err)
	}
	if !strings.Contains(human.String(), "Runtime:   not applicable") {
		t.Fatalf("status output = %q", human.String())
	}

	var jsonOut bytes.Buffer
	workspaceOpsJSON = true
	cmd = &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&jsonOut)
	workspaceOpsTimeoutSec = 0
	if err := runWorkspaceOpsEnsureRuntime(cmd, []string{"WS"}); err != nil {
		t.Fatalf("runWorkspaceOpsEnsureRuntime: %v", err)
	}
	var decoded WorkspaceOpsStatus
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("decode ensure-runtime JSON %q: %v", jsonOut.String(), err)
	}
	if decoded.Workspace.Key != "WS" || decoded.LocalRuntime == nil || decoded.LocalRuntime.Applicable {
		t.Fatalf("ensure-runtime decoded = %+v", decoded)
	}
}
