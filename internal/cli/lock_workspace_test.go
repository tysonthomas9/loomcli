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

	// Path matching a repo should resolve to workspace root
	result := ResolveLockDir(repo1)
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
	}

	result = ResolveLockDir(repo2)
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
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
					{Name: "repo1", Path: "repo1"},       // relative
					{Name: "repo2", Path: "sub/repo2"},   // relative with subdir
				},
			},
		},
	})

	// Relative repo paths should be resolved against workspace root
	result := ResolveLockDir(filepath.Join(wsDir, "repo1"))
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
	}

	result = ResolveLockDir(filepath.Join(wsDir, "sub", "repo2"))
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
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

	// Should still match via workspace path prefix
	result := ResolveLockDir(filepath.Join(wsDir, "anything"))
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
	}
}

func TestResolveLockDir_PathNormalization(t *testing.T) {
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

	// Path with trailing slash and .. should be cleaned
	messyPath := filepath.Join(wsDir, "repo1", "subdir", "..")
	result := ResolveLockDir(messyPath)
	if result != wsDir {
		t.Errorf("expected workspace root %q, got %q", wsDir, result)
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

	// Acquire lock from repo path - should create lock at workspace root
	err := AcquireLock(repoDir, "task", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(repoDir)

	// Lock file should be at workspace root, not repo dir
	wsLockPath := filepath.Join(wsDir, LockFileName)
	repoLockPath := filepath.Join(repoDir, LockFileName)

	if _, err := os.Stat(wsLockPath); os.IsNotExist(err) {
		t.Error("lock file should exist at workspace root")
	}
	if _, err := os.Stat(repoLockPath); !os.IsNotExist(err) {
		t.Error("lock file should NOT exist at repo dir")
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

func TestAcquireLock_WorkspaceSharedLock(t *testing.T) {
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

	// Acquire lock from repo2 should FAIL (shared lock)
	err = AcquireLock(repo2, "task", "nova")
	if err == nil {
		t.Error("expected error when acquiring shared workspace lock from second repo")
	}
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

	// CheckLock from repo path should find the workspace lock
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

	// CheckLock from workspace root should also work
	info2, running2, err2 := CheckLock(wsDir)
	if err2 != nil {
		t.Fatalf("CheckLock from wsDir failed: %v", err2)
	}
	if !running2 {
		t.Error("expected running=true from workspace root")
	}
	if info2.AgentName != "falcon" {
		t.Errorf("expected agent 'falcon' from workspace root, got %q", info2.AgentName)
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

	// Release from repo path should remove the lock at workspace root
	err = ReleaseLock(repoDir)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	wsLockPath := filepath.Join(wsDir, LockFileName)
	if _, err := os.Stat(wsLockPath); !os.IsNotExist(err) {
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
	// Ensure /tmp/ws does NOT match /tmp/ws2
	wsDir := t.TempDir()
	similarDir := wsDir + "2" // e.g., /tmp/abc123 vs /tmp/abc1232
	os.MkdirAll(similarDir, 0755)

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

	result := ResolveLockDir(similarDir)
	if result != similarDir {
		t.Errorf("path %q should NOT match workspace %q, but resolved to %q", similarDir, wsDir, result)
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

	// repoA should resolve to wsA
	result := ResolveLockDir(repoA)
	if result != wsA {
		t.Errorf("repoA: expected %q, got %q", wsA, result)
	}

	// repoB should resolve to wsB
	result = ResolveLockDir(repoB)
	if result != wsB {
		t.Errorf("repoB: expected %q, got %q", wsB, result)
	}

	// Names should also be correct
	nameA := resolveWorkspaceName(repoA)
	if nameA != "workspace-a" {
		t.Errorf("expected 'workspace-a', got %q", nameA)
	}

	nameB := resolveWorkspaceName(repoB)
	if nameB != "workspace-b" {
		t.Errorf("expected 'workspace-b', got %q", nameB)
	}
}
