package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestFleetWorkspaceListAndRemoveAgainstLocalStore(t *testing.T) {
	handle := setupWorkspaceCommandFleetStore(t)
	defer handle.Close()

	resetWorkspaceCommandFlags(t)
	if out := captureWorkspaceStdout(t, func() {
		if err := runFleetWorkspaceList(); err != nil {
			t.Fatalf("runFleetWorkspaceList empty: %v", err)
		}
	}); !strings.Contains(out, "No workspaces configured") {
		t.Fatalf("empty list output = %q", out)
	}

	ctx := context.Background()
	localRoot := t.TempDir()
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "WS",
		Name:          "Workspace",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "api",
		RemoteURL:     "/tmp/api",
		Remote:        "origin",
		DefaultBranch: "main",
		Groups:        []string{"backend"},
		SourceRepoID:  "api",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {
				Path:  localRoot,
				Repos: map[string]string{"api": filepath.Join(localRoot, "api")},
			},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	wsListJSON = false
	if out := captureWorkspaceStdout(t, func() {
		if err := runFleetWorkspaceList(); err != nil {
			t.Fatalf("runFleetWorkspaceList: %v", err)
		}
	}); !strings.Contains(out, "WS") || !strings.Contains(out, localRoot) || !strings.Contains(out, "1 repos") {
		t.Fatalf("list output = %q", out)
	}

	wsListJSON = true
	if out := captureWorkspaceStdout(t, func() {
		if err := runFleetWorkspaceList(); err != nil {
			t.Fatalf("runFleetWorkspaceList json: %v", err)
		}
	}); !strings.Contains(out, `"key": "WS"`) || !strings.Contains(out, `"repos": 1`) {
		t.Fatalf("list json output = %q", out)
	}

	wsRemoveKeepWorktrees = true
	if out := captureWorkspaceStdout(t, func() {
		runWorkspaceRemove(&cobra.Command{}, []string{"Workspace"})
	}); !strings.Contains(out, `Workspace "WS" removed.`) {
		t.Fatalf("remove output = %q", out)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state after remove: %v", err)
	}
	if _, ok := sc.Workspaces["WS"]; ok || sc.LastWorkspace != "" {
		t.Fatalf("workspace local state after remove = %+v", sc)
	}
}

func TestFleetWorkspaceAddUseShowAndStatusAgainstLocalStore(t *testing.T) {
	handle := setupWorkspaceCommandFleetStore(t)
	defer handle.Close()

	resetWorkspaceCommandFlags(t)
	origAddDescription, origAddBranch := wsAddDescription, wsAddBranch
	origShowJSON, origStatusJSON := wsShowJSON, wsStatusJSON
	t.Cleanup(func() {
		wsAddDescription, wsAddBranch = origAddDescription, origAddBranch
		wsShowJSON, wsStatusJSON = origShowJSON, origStatusJSON
	})

	wsAddDescription = "Primary workspace"
	wsAddBranch = "trunk"
	if out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceAdd(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceAdd: %v", err)
		}
	}); !strings.Contains(out, "Created workspace APP") {
		t.Fatalf("add output = %q", out)
	}

	ctx := context.Background()
	if _, err := handle.Store.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "APP", Name: "builder"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "APP",
		Name:          "api",
		RemoteURL:     "/tmp/api",
		Remote:        "origin",
		DefaultBranch: "trunk",
		SourceRepoID:  "api",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := handle.Store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "APP",
		Name:         "worker",
		RoleName:     "builder",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceUse(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceUse: %v", err)
		}
	}); !strings.Contains(out, "Selected workspace: APP") || !strings.Contains(out, "LOOM_WORKSPACE=APP") {
		t.Fatalf("use output = %q", out)
	}

	wsShowJSON = false
	if out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceShow(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceShow: %v", err)
		}
	}); !strings.Contains(out, "Workspace:    APP") || !strings.Contains(out, "Repos:        1") || !strings.Contains(out, "Agents:       1") {
		t.Fatalf("show output = %q", out)
	}

	wsShowJSON = true
	if out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceShow(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceShow json: %v", err)
		}
	}); !strings.Contains(out, `"key": "APP"`) || !strings.Contains(out, `"name": "api"`) {
		t.Fatalf("show json output = %q", out)
	}

	wsStatusJSON = false
	if out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceStatus(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceStatus: %v", err)
		}
	}); !strings.Contains(out, "APP") || !strings.Contains(out, "ready") {
		t.Fatalf("status output = %q", out)
	}

	wsStatusJSON = true
	out := captureWorkspaceStdout(t, func() {
		if err := runWorkspaceStatus(&cobra.Command{}, []string{"APP"}); err != nil {
			t.Fatalf("runWorkspaceStatus json: %v", err)
		}
	})
	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("status json = %q: %v", out, err)
	}
	if status["key"] != "APP" {
		t.Fatalf("status json map = %#v", status)
	}

	if _, err := pickWorkspaceKey(ctx, handle.Store, []string{"EXPLICIT"}); err != nil {
		t.Fatalf("pick explicit workspace: %v", err)
	}
	if err := runWorkspaceUse(&cobra.Command{}, []string{"MISSING"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing workspace use err = %v", err)
	}
}

func setupWorkspaceCommandFleetStore(t *testing.T) *bootstrap.StoreHandle {
	t.Helper()
	requireWorkspaceCommandFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "workspace-command-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	handle, err := bootstrap.OpenStore(context.Background(), configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return handle
}

func requireWorkspaceCommandFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func resetWorkspaceCommandFlags(t *testing.T) {
	t.Helper()
	origListJSON := wsListJSON
	origRemoveForce, origRemoveKeep := wsRemoveForce, wsRemoveKeepWorktrees
	t.Cleanup(func() {
		wsListJSON = origListJSON
		wsRemoveForce, wsRemoveKeepWorktrees = origRemoveForce, origRemoveKeep
	})
}

func TestRunWorkspaceCreateUsesStoreBackedCreateHook(t *testing.T) {
	oldWithStore := workspaceWithStoreFn
	oldBuildCreate := buildStoreBackedCreateWorkspaceFn
	oldRepos, oldPath, oldBranch := wsCreateRepos, wsCreatePath, wsCreateBranch
	t.Cleanup(func() {
		workspaceWithStoreFn = oldWithStore
		buildStoreBackedCreateWorkspaceFn = oldBuildCreate
		wsCreateRepos, wsCreatePath, wsCreateBranch = oldRepos, oldPath, oldBranch
	})

	workspaceWithStoreFn = func(fn func(context.Context, *bootstrap.StoreHandle) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{})
	}

	var gotReq service.WorkspaceCreateRequest
	buildStoreBackedCreateWorkspaceFn = func(store.Store) service.WorkspaceCreateFn {
		return func(_ context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
			gotReq = req
			return service.WorkspaceCreateResult{WorkspaceID: "WS-CREATED", WorkspacePath: "/tmp/ws-created"}, nil
		}
	}

	wsCreateRepos = "/repo/one,/repo/two"
	wsCreatePath = "/workspace/path"
	wsCreateBranch = "feature_branch"

	out := captureWorkspaceStdout(t, func() {
		runWorkspaceCreate(&cobra.Command{}, []string{"New_WS"})
	})
	if !strings.Contains(out, `Workspace "WS-CREATED" created at /tmp/ws-created.`) {
		t.Fatalf("create output = %q", out)
	}
	if gotReq.Name != "New_WS" || gotReq.Type != "empty" || gotReq.Path != "/workspace/path" || gotReq.Branch != "feature_branch" {
		t.Fatalf("create request = %+v", gotReq)
	}
	if len(gotReq.Repos) != 2 || gotReq.Repos[0] != "/repo/one" || gotReq.Repos[1] != "/repo/two" {
		t.Fatalf("create repos = %+v", gotReq.Repos)
	}
}

func captureWorkspaceStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	return b.String()
}
