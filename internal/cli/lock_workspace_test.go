//go:build ignore

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupLockWorkspaceConfig creates a temporary config dir with a workspace config
// and sets LOOM_CONFIG_DIR so LoadConfig uses it.
// Returns a cleanup function that restores the original env var.
func setupLockWorkspaceConfig(t *testing.T, cfg *LoomConfig) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	if cfg != nil {
		if err := SaveConfig(cfg); err != nil {
			t.Fatalf("failed to save config: %v", err)
		}
	}
}

// ============================================================================
// ResolveLockDir Tests
// ============================================================================

func TestResolveLockDir_Legacy(t *testing.T) {
	// No config → returns path unchanged
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir()) // empty config dir, no config.yaml

	path := "/some/worktree/path"
	result := ResolveLockDir(path)
	if result != path {
		t.Errorf("expected %q, got %q", path, result)
	}
}

func TestResolveLockDir_WorkspaceRepoPath(t *testing.T) {
	wsDir := t.TempDir()
	repo1 := filepath.Join(wsDir, "repo1")
	repo2 := filepath.Join(wsDir, "repo2")
	os.MkdirAll(repo1, 0755)
	os.MkdirAll(repo2, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1},
					{Name: "repo2", Path: repo2},
				},
			},
		},
	})

	// Each repo gets its own lock dir (not workspace root)
	result := ResolveLockDir(repo1)
	if result != repo1 {
		t.Errorf("expected %q, got %q", repo1, result)
	}

	result = ResolveLockDir(repo2)
	if result != repo2 {
		t.Errorf("expected %q, got %q", repo2, result)
	}
}

func TestResolveLockDir_WorkspaceRootPath(t *testing.T) {
	wsDir := t.TempDir()

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: filepath.Join(wsDir, "repo1")},
				},
			},
		},
	})

	// Path is workspace root → returns workspace root
	result := ResolveLockDir(wsDir)
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
	}
}

func TestResolveLockDir_UnrelatedPath(t *testing.T) {
	wsDir := t.TempDir()
	unrelatedDir := t.TempDir()

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: filepath.Join(wsDir, "repo1")},
				},
			},
		},
	})

	// Unrelated path → returns path unchanged
	result := ResolveLockDir(unrelatedDir)
	if result != unrelatedDir {
		t.Errorf("expected %q, got %q", unrelatedDir, result)
	}
}

func TestResolveLockDir_RelativeRepoPaths(t *testing.T) {
	wsDir := t.TempDir()

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: "repo1"},     // relative
					{Name: "repo2", Path: "sub/repo2"}, // relative with subdir
				},
			},
		},
	})

	// Each path returns unchanged — per-worktree locks
	repo1Path := filepath.Join(wsDir, "repo1")
	result := ResolveLockDir(repo1Path)
	if result != repo1Path {
		t.Errorf("expected %q, got %q", repo1Path, result)
	}

	repo2Path := filepath.Join(wsDir, "sub", "repo2")
	result = ResolveLockDir(repo2Path)
	if result != repo2Path {
		t.Errorf("expected %q, got %q", repo2Path, result)
	}
}

func TestResolveLockDir_EmptyWorkspacePath(t *testing.T) {
	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"empty": {
				Path: "", // empty path
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/some/repo"},
				},
			},
		},
	})

	path := "/some/repo"
	result := ResolveLockDir(path)
	// Should return path unchanged since workspace path is empty
	if result != path {
		t.Errorf("expected %q, got %q", path, result)
	}
}

func TestResolveLockDir_EmptyRepoPath(t *testing.T) {
	wsDir := t.TempDir()

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: ""}, // empty repo path
				},
			},
		},
	})

	// Returns path unchanged
	inputPath := filepath.Join(wsDir, "anything")
	result := ResolveLockDir(inputPath)
	if result != inputPath {
		t.Errorf("expected %q, got %q", inputPath, result)
	}
}

func TestResolveLockDir_PathNormalization(t *testing.T) {
	// Path with .. is returned as-is (caller is responsible for cleaning)
	messyPath := filepath.Join(t.TempDir(), "repo1", "subdir", "..")
	result := ResolveLockDir(messyPath)
	if result != messyPath {
		t.Errorf("expected %q, got %q", messyPath, result)
	}
}

// ============================================================================
// resolveWorkspaceName Tests
// ============================================================================

func TestResolveWorkspaceName(t *testing.T) {
	wsDir := t.TempDir()

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: filepath.Join(wsDir, "repo1")},
				},
			},
		},
	})

	name := resolveWorkspaceName(filepath.Join(wsDir, "repo1"))
	if name != "myws" {
		t.Errorf("expected 'myws', got %q", name)
	}

	name = resolveWorkspaceName("/unrelated/path")
	if name != "" {
		t.Errorf("expected empty string for unrelated path, got %q", name)
	}
}

func TestResolveWorkspaceName_NoConfig(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	name := resolveWorkspaceName("/any/path")
	if name != "" {
		t.Errorf("expected empty string when no config, got %q", name)
	}
}

// ============================================================================
// Workspace-Aware Lock Function Tests
// ============================================================================

func TestAcquireLock_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	// Acquire lock from repo path - lock should be at repo dir (per-worktree)
	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	repoLockPath := filepath.Join(repoDir, LockFileName)
	if _, err := os.Stat(repoLockPath); os.IsNotExist(err) {
		t.Error("lock file should exist at repo dir")
	}

	// Verify Workspace field is populated
	info, err := ReadLockFile(repoDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if info.Workspace != "testws" {
		t.Errorf("expected Workspace 'testws', got %q", info.Workspace)
	}
}

func TestAcquireLock_WorkspaceIndependentLocks(t *testing.T) {
	wsDir := t.TempDir()
	repo1 := filepath.Join(wsDir, "repo1")
	repo2 := filepath.Join(wsDir, "repo2")
	os.MkdirAll(repo1, 0755)
	os.MkdirAll(repo2, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1},
					{Name: "repo2", Path: repo2},
				},
			},
		},
	})

	// Acquire lock from repo1
	err := AcquireLock(repo1, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock for repo1 failed: %v", err)
	}
	defer ReleaseLock(repo1)

	// Acquire lock from repo2 should SUCCEED (independent per-worktree locks)
	err = AcquireLock(repo2, "task", "nova")
	if err != nil {
		t.Errorf("expected independent locks, but got error: %v", err)
	}
	defer ReleaseLock(repo2)
}

func TestCheckLock_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	// Acquire lock from repo path
	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	// CheckLock from repo path should find the lock at repo dir
	info, running, err := CheckLock(repoDir)
	if err != nil {
		t.Fatalf("CheckLock failed: %v", err)
	}
	if !running {
		t.Error("expected running=true")
	}
	if info.AgentName != "falcon" {
		t.Errorf("expected agent 'falcon', got %q", info.AgentName)
	}

	// CheckLock from workspace root should NOT find the repo lock (independent paths)
	info2, _, _ := CheckLock(wsDir)
	if info2 != nil {
		t.Error("expected no lock at workspace root when lock was acquired at repo dir")
	}
}

func TestReleaseLock_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// Release from repo path should remove the lock at repo dir
	err = ReleaseLock(repoDir)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	repoLockPath := filepath.Join(repoDir, LockFileName)
	if _, err := os.Stat(repoLockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after release")
	}
}

func TestLockInfo_WorkspaceField(t *testing.T) {
	info := LockInfo{
		PID:       12345,
		Command:   "task",
		AgentName: "falcon",
		Workspace: "myworkspace",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed LockInfo
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed.Workspace != "myworkspace" {
		t.Errorf("expected Workspace 'myworkspace', got %q", parsed.Workspace)
	}
}

func TestLockInfo_WorkspaceFieldOmitEmpty(t *testing.T) {
	info := LockInfo{
		PID:       12345,
		Command:   "task",
		AgentName: "falcon",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Workspace should be omitted when empty
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, ok := raw["workspace"]; ok {
		t.Error("workspace field should be omitted when empty")
	}
}

func TestUpdateLockTask_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	// UpdateLockTask from repo path should update the workspace lock
	err = UpdateLockTask(repoDir, "bd-456", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	info, err := ReadLockFile(repoDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if info.TaskID != "bd-456" {
		t.Errorf("expected TaskID 'bd-456', got %q", info.TaskID)
	}
}

func TestClearLockTaskID_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	UpdateLockTask(repoDir, "bd-789", "Test")
	err = ClearLockTaskID(repoDir)
	if err != nil {
		t.Fatalf("ClearLockTaskID failed: %v", err)
	}

	info, err := ReadLockFile(repoDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if info.TaskID != "" {
		t.Errorf("expected empty TaskID, got %q", info.TaskID)
	}
}

func TestUpdateLockState_Workspace(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	os.MkdirAll(repoDir, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	err = UpdateLockState(repoDir, StateIdle)
	if err != nil {
		t.Fatalf("UpdateLockState failed: %v", err)
	}

	info, err := ReadLockFile(repoDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed: %v", err)
	}
	if info.State != StateIdle {
		t.Errorf("expected state %q, got %q", StateIdle, info.State)
	}
}

func TestResolveLockDir_PrefixFalsePositive(t *testing.T) {
	// Identity function — all paths returned unchanged
	wsDir := t.TempDir()
	similarDir := wsDir + "2"
	os.MkdirAll(similarDir, 0755)

	result := ResolveLockDir(similarDir)
	if result != similarDir {
		t.Errorf("expected %q, got %q", similarDir, result)
	}
}

func TestResolveLockDir_MultipleWorkspaces(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	repoA := filepath.Join(wsA, "repoA")
	repoB := filepath.Join(wsB, "repoB")
	os.MkdirAll(repoA, 0755)
	os.MkdirAll(repoB, 0755)

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"workspace-a": {
				Path: wsA,
				Repos: []RepoConfig{
					{Name: "repoA", Path: repoA},
				},
			},
			"workspace-b": {
				Path: wsB,
				Repos: []RepoConfig{
					{Name: "repoB", Path: repoB},
				},
			},
		},
	})

	// Each repo path returns unchanged (per-worktree locks)
	result := ResolveLockDir(repoA)
	if result != repoA {
		t.Errorf("repoA: expected %q, got %q", repoA, result)
	}

	result = ResolveLockDir(repoB)
	if result != repoB {
		t.Errorf("repoB: expected %q, got %q", repoB, result)
	}

	// resolveWorkspaceName still works correctly
	nameA := resolveWorkspaceName(repoA)
	if nameA != "workspace-a" {
		t.Errorf("expected 'workspace-a', got %q", nameA)
	}

	nameB := resolveWorkspaceName(repoB)
	if nameB != "workspace-b" {
		t.Errorf("expected 'workspace-b', got %q", nameB)
	}
}
