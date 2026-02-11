package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunWorkspaceList_Output(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create workspace directories so they show "ok" status
	wsAlpha := filepath.Join(tmpDir, "ws-alpha")
	if err := os.MkdirAll(wsAlpha, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {
				Path: wsAlpha,
				Repos: []RepoConfig{
					{Name: "api", Path: filepath.Join(wsAlpha, "api")},
					{Name: "web", Path: filepath.Join(wsAlpha, "web")},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	wsListJSON = false

	// Should not panic - exercises the output formatting path
	runWorkspaceList(nil, nil)
}

func TestRunWorkspaceList_JSONOutput(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: "/tmp/myws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/myws/api"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	wsListJSON = true

	// Should not panic - exercises the JSON output path
	runWorkspaceList(nil, nil)
}

func TestRunWorkspaceList_DefaultMarker(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create workspace dirs so they show "ok" status
	wsAlpha := filepath.Join(tmpDir, "ws-alpha")
	wsBeta := filepath.Join(tmpDir, "ws-beta")
	os.MkdirAll(wsAlpha, 0755)
	os.MkdirAll(wsBeta, 0755)

	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {
				Path:  wsAlpha,
				Repos: []RepoConfig{{Name: "api", Path: filepath.Join(wsAlpha, "api")}},
			},
			"beta": {
				Path:  wsBeta,
				Repos: []RepoConfig{{Name: "svc", Path: filepath.Join(wsBeta, "svc")}},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	wsListJSON = false

	// Exercises the default marker logic (default workspace gets " *" suffix)
	runWorkspaceList(nil, nil)
}

func TestRunWorkspaceList_NoWorkspacesMessage(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// No config file exists - should print "No workspaces configured." message
	wsListJSON = false
	runWorkspaceList(nil, nil)
}

func TestRunWorkspaceList_MissingDirStatus(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ghost",
		Workspaces: map[string]WorkspaceConfig{
			"ghost": {
				Path:  filepath.Join(tmpDir, "nonexistent-dir"),
				Repos: []RepoConfig{{Name: "r", Path: "/nowhere/r"}},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	wsListJSON = false

	// Exercises the "missing" dir status path
	runWorkspaceList(nil, nil)
}
