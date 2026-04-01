package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/configlock"
)

func TestGetConfigDir(t *testing.T) {
	// Test env var override
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)
	if got := GetConfigDir(); got != tmpDir {
		t.Errorf("GetConfigDir() = %q, want %q", got, tmpDir)
	}

	// Test default (home dir based)
	t.Setenv("LOOM_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	want := filepath.Join(home, ".loom")
	if got := GetConfigDir(); got != want {
		t.Errorf("GetConfigDir() = %q, want %q", got, want)
	}
}

func TestGetConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)
	want := filepath.Join(tmpDir, "config.yaml")
	if got := GetConfigPath(); got != want {
		t.Errorf("GetConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Errorf("LoadConfig() = %+v, want nil", cfg)
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yaml := `default_workspace: myproject
workspaces:
  myproject:
    path: /home/user/workspaces/myproject
    repos:
      - name: backend
        path: /home/user/repos/backend
        default_branch: main
      - name: frontend
        path: /home/user/repos/frontend
        default_branch: develop
        remote: upstream
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil")
	}
	if cfg.DefaultWorkspace != "myproject" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "myproject")
	}
	ws, ok := cfg.Workspaces["myproject"]
	if !ok {
		t.Fatal("workspace 'myproject' not found")
	}
	if ws.Path != "/home/user/workspaces/myproject" {
		t.Errorf("workspace path = %q, want %q", ws.Path, "/home/user/workspaces/myproject")
	}
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(ws.Repos))
	}
	if ws.Repos[0].Name != "backend" {
		t.Errorf("repos[0].Name = %q, want %q", ws.Repos[0].Name, "backend")
	}
	if ws.Repos[1].Remote != "upstream" {
		t.Errorf("repos[1].Remote = %q, want %q", ws.Repos[1].Remote, "upstream")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err == nil {
		t.Fatalf("LoadConfig() error = nil, want error; cfg = %+v", cfg)
	}
	if cfg != nil {
		t.Errorf("LoadConfig() cfg = %+v, want nil on error", cfg)
	}
}

func TestLoadConfigEmptyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil for empty file, want zero-value config")
	}
}

func TestGetWorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	want := filepath.Join(dir, "workspaces", "myws")
	if got := GetWorkspaceDir("myws"); got != want {
		t.Errorf("GetWorkspaceDir() = %q, want %q", got, want)
	}
}

func TestIsWorkspaceMode(t *testing.T) {
	// No config file → false
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if IsWorkspaceMode() {
		t.Error("IsWorkspaceMode() = true, want false (no config)")
	}

	// Config with workspaces → true
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	yaml := `workspaces:
  test:
    path: /tmp/test
    repos: []
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsWorkspaceMode() {
		t.Error("IsWorkspaceMode() = false, want true")
	}

	// Empty config → false
	dir2 := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir2)
	if err := os.WriteFile(filepath.Join(dir2, "config.yaml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if IsWorkspaceMode() {
		t.Error("IsWorkspaceMode() = true, want false (empty config)")
	}
}

func TestValidateRemoteName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: false},
		{name: "origin", input: "origin", wantErr: false},
		{name: "upstream", input: "upstream", wantErr: false},
		{name: "dot", input: "my.remote", wantErr: false},
		{name: "underscore", input: "my_remote", wantErr: false},
		{name: "hyphen", input: "my-remote", wantErr: false},
		{name: "starts with dash", input: "-evil", wantErr: true},
		{name: "flag injection", input: "--receive-pack=/evil", wantErr: true},
		{name: "space", input: "my remote", wantErr: true},
		{name: "slash", input: "my/remote", wantErr: true},
		{name: "colon", input: "host:path", wantErr: true},
		{name: "at sign", input: "user@host", wantErr: true},
		{name: "too long", input: strings.Repeat("a", 256), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRemoteName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRemoteName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfig_InvalidRemote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yamlData := `workspaces:
  myproject:
    path: /tmp/myproject
    repos:
      - name: backend
        path: /tmp/myproject/backend
        remote: "--receive-pack=/evil"
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err == nil {
		t.Fatalf("LoadConfig() error = nil, want error for invalid remote; cfg = %+v", cfg)
	}
	if cfg != nil {
		t.Errorf("LoadConfig() cfg = %+v, want nil on error", cfg)
	}
}

func TestRepoConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yaml := `workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	repo := cfg.Workspaces["ws"].Repos[0]
	if repo.DefaultBranch != "" {
		t.Errorf("DefaultBranch = %q, want empty (omitempty)", repo.DefaultBranch)
	}
	if repo.Remote != "" {
		t.Errorf("Remote = %q, want empty (omitempty)", repo.Remote)
	}
}

func TestRepoConfig_ResolveAbsPath_Relative(t *testing.T) {
	rc := RepoConfig{Path: "repos/myrepo"}
	got := rc.ResolveAbsPath("/workspace")
	want := "/workspace/repos/myrepo"
	if got != want {
		t.Errorf("ResolveAbsPath(%q) = %q, want %q", "/workspace", got, want)
	}
}

func TestRepoConfig_ResolveAbsPath_Absolute(t *testing.T) {
	rc := RepoConfig{Path: "/abs/myrepo"}
	got := rc.ResolveAbsPath("/workspace")
	want := "/abs/myrepo"
	if got != want {
		t.Errorf("ResolveAbsPath(%q) = %q, want %q", "/workspace", got, want)
	}
}

func TestRepoConfigGroupsParsing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yamlData := `workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
        groups:
          - backend
          - infra
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	repo := cfg.Workspaces["ws"].Repos[0]
	if len(repo.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2", len(repo.Groups))
	}
	if repo.Groups[0] != "backend" || repo.Groups[1] != "infra" {
		t.Errorf("Groups = %v, want [backend infra]", repo.Groups)
	}
}

func TestRepoConfigSourceRepoIDDefaulting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yamlData := `workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	repo := cfg.Workspaces["ws"].Repos[0]
	if repo.SourceRepoID != "myrepo" {
		t.Errorf("SourceRepoID = %q, want %q (defaulted from Name)", repo.SourceRepoID, "myrepo")
	}
}

func TestRepoConfigSourceRepoIDExplicit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yamlData := `workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
        source_repo_id: custom-id
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	repo := cfg.Workspaces["ws"].Repos[0]
	if repo.SourceRepoID != "custom-id" {
		t.Errorf("SourceRepoID = %q, want %q (explicit value preserved)", repo.SourceRepoID, "custom-id")
	}
}

func TestRepoConfig_ResolveAbsPath_EmptyWorkspace(t *testing.T) {
	rc := RepoConfig{Path: "repos/myrepo"}
	got := rc.ResolveAbsPath("")
	want := filepath.Join("", "repos/myrepo")
	if got != want {
		t.Errorf("ResolveAbsPath(%q) = %q, want %q", "", got, want)
	}
}

// captureStderr redirects os.Stderr to a pipe, runs fn, and returns
// whatever was written to stderr as a string.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = oldStderr

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return string(out)
}

func TestLoadConfig_AutoMigration(t *testing.T) {
	resetConfigVersionWarnOnce()
	t.Cleanup(resetConfigVersionWarnOnce)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Write a version-0 config (version field omitted defaults to 0)
	yamlData := `workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
`
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	// LoadConfig should auto-migrate without warnings
	stderr := captureStderr(t, func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg == nil {
			t.Fatal("LoadConfig() returned nil")
		}
		if cfg.Version != CurrentConfigVersion {
			t.Errorf("cfg.Version = %d, want %d", cfg.Version, CurrentConfigVersion)
		}
		// Verify workspace data preserved
		ws, ok := cfg.Workspaces["ws"]
		if !ok {
			t.Fatal("workspace 'ws' not found after auto-migration")
		}
		if ws.ID == "" {
			t.Error("workspace 'ws' should have a UUID after auto-migration")
		}
	})
	if strings.Contains(stderr, "loom config migrate") {
		t.Errorf("auto-migration should not produce a warning, got stderr = %q", stderr)
	}

	// Verify file was updated on disk
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var diskMap map[string]interface{}
	if err := yaml.Unmarshal(raw, &diskMap); err != nil {
		t.Fatal(err)
	}
	if v := getConfigVersion(diskMap); v != CurrentConfigVersion {
		t.Errorf("on-disk version = %d, want %d", v, CurrentConfigVersion)
	}

	// Verify backup was created
	matches, _ := filepath.Glob(filepath.Join(dir, "config.yaml.bak.*"))
	if len(matches) == 0 {
		t.Error("expected a backup file to be created during auto-migration")
	}

	// Second call should not create another backup (file is already current)
	cfg2, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() #2 error = %v", err)
	}
	if cfg2.Version != CurrentConfigVersion {
		t.Errorf("cfg2.Version = %d, want %d", cfg2.Version, CurrentConfigVersion)
	}
	matches2, _ := filepath.Glob(filepath.Join(dir, "config.yaml.bak.*"))
	if len(matches2) != len(matches) {
		t.Errorf("second LoadConfig created additional backup: %d vs %d files", len(matches2), len(matches))
	}
}

func TestLoadConfig_AutoMigration_CurrentVersion(t *testing.T) {
	resetConfigVersionWarnOnce()
	t.Cleanup(resetConfigVersionWarnOnce)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	yamlData := fmt.Sprintf("version: %d\nbackend: claude\n", CurrentConfigVersion)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg == nil {
			t.Fatal("returned nil")
		}
	})
	if strings.Contains(stderr, "loom config migrate") {
		t.Errorf("current version config should not warn, got stderr = %q", stderr)
	}

	// No backup should be created
	matches, _ := filepath.Glob(filepath.Join(dir, "config.yaml.bak.*"))
	if len(matches) != 0 {
		t.Errorf("no backup expected for current-version config, got %d files", len(matches))
	}
}

func TestLoadConfig_AutoMigration_PreservesEnvVars(t *testing.T) {
	resetConfigVersionWarnOnce()
	t.Cleanup(resetConfigVersionWarnOnce)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("MY_BACKEND", "claude")

	// v0 config with an env var template
	yamlData := `backend: ${MY_BACKEND}
workspaces:
  ws:
    path: /tmp/ws
    repos:
      - name: myrepo
        path: /tmp/ws/myrepo
`
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Backend != "claude" {
		t.Errorf("cfg.Backend = %q, want %q", cfg.Backend, "claude")
	}

	// Verify the file on disk still has the env var template (not expanded)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "${MY_BACKEND}") {
		t.Errorf("env var template should be preserved in migrated file, got:\n%s", raw)
	}
}

func TestWorkspaceByID(t *testing.T) {
	id1 := "11111111-1111-1111-1111-111111111111"
	id2 := "22222222-2222-2222-2222-222222222222"
	id3 := "33333333-3333-3333-3333-333333333333"
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {ID: id1, Path: "/tmp/alpha"},
			"beta":  {ID: id2, Path: "/tmp/beta"},
			"gamma": {ID: id3, Path: "/tmp/gamma"},
		},
	}

	// Found by known ID
	name, ws, ok := WorkspaceByID(cfg, id2)
	if !ok {
		t.Fatal("WorkspaceByID() returned false for known ID")
	}
	if name != "beta" {
		t.Errorf("name = %q, want %q", name, "beta")
	}
	if ws.Path != "/tmp/beta" {
		t.Errorf("ws.Path = %q, want %q", ws.Path, "/tmp/beta")
	}

	// Unknown UUID
	_, _, ok = WorkspaceByID(cfg, "99999999-9999-9999-9999-999999999999")
	if ok {
		t.Error("WorkspaceByID() returned true for unknown ID")
	}

	// Empty ID
	_, _, ok = WorkspaceByID(cfg, "")
	if ok {
		t.Error("WorkspaceByID() returned true for empty ID")
	}

	// Nil config
	_, _, ok = WorkspaceByID(nil, id1)
	if ok {
		t.Error("WorkspaceByID() returned true for nil config")
	}
}

func TestLoadConfig_ResolvesDefaultWorkspaceID(t *testing.T) {
	resetConfigVersionWarnOnce()
	t.Cleanup(resetConfigVersionWarnOnce)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	wsID := uuid.New().String()
	yamlData := fmt.Sprintf(`version: %d
default_workspace: myws
workspaces:
  myws:
    id: %s
    path: /tmp/myws
    repos:
      - name: repo1
        path: /tmp/myws/repo1
  other:
    id: %s
    path: /tmp/other
    repos:
      - name: repo2
        path: /tmp/other/repo2
`, CurrentConfigVersion, wsID, uuid.New().String())

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil")
	}
	if cfg.DefaultWorkspaceID != wsID {
		t.Errorf("DefaultWorkspaceID = %q, want %q", cfg.DefaultWorkspaceID, wsID)
	}
}

func TestNewWorkspaceID(t *testing.T) {
	id1 := NewWorkspaceID()
	id2 := NewWorkspaceID()

	// Both should be valid UUIDs
	if _, err := uuid.Parse(id1); err != nil {
		t.Errorf("NewWorkspaceID() #1 = %q, not a valid UUID: %v", id1, err)
	}
	if _, err := uuid.Parse(id2); err != nil {
		t.Errorf("NewWorkspaceID() #2 = %q, not a valid UUID: %v", id2, err)
	}

	// Should be different
	if id1 == id2 {
		t.Errorf("NewWorkspaceID() returned same ID twice: %s", id1)
	}
}

func TestWithConfigLock_Serializes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Seed an empty config with version set so auto-migration doesn't run.
	seed := fmt.Sprintf("version: %d\nworkspaces: {}\n", CurrentConfigVersion)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = WithConfigLock(func() error {
				cfg, err := loadConfigUnlocked()
				if err != nil {
					return err
				}
				if cfg == nil {
					cfg = &LoomConfig{
						Version:    CurrentConfigVersion,
						Workspaces: map[string]WorkspaceConfig{},
					}
				}
				if cfg.Workspaces == nil {
					cfg.Workspaces = map[string]WorkspaceConfig{}
				}
				name := fmt.Sprintf("ws-%d", idx)
				cfg.Workspaces[name] = WorkspaceConfig{
					ID:   NewWorkspaceID(),
					Path: filepath.Join("/tmp", name),
				}
				return saveConfigUnlocked(cfg)
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	// Verify all 10 workspaces are present (no lost updates).
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig returned nil")
	}
	if len(cfg.Workspaces) != 10 {
		t.Errorf("expected 10 workspaces, got %d", len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			t.Logf("  workspace: %s", name)
		}
	}
}

func TestWithConfigLock_ErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	sentinel := fmt.Errorf("deliberate error")
	err := WithConfigLock(func() error {
		return sentinel
	})
	if err == nil || err.Error() != sentinel.Error() {
		t.Fatalf("WithConfigLock returned %v, want %v", err, sentinel)
	}

	// Lock should be released — a subsequent call must succeed.
	err = WithConfigLock(func() error { return nil })
	if err != nil {
		t.Fatalf("subsequent WithConfigLock failed (lock not released?): %v", err)
	}
}

func TestWithConfigLock_CreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	lockPath := filepath.Join(dir, configlock.ConfigLockFileName)

	// Lock file should not exist yet.
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("lock file should not exist before WithConfigLock")
	}

	err := WithConfigLock(func() error {
		// Inside fn, the lock file should exist.
		if _, statErr := os.Stat(lockPath); statErr != nil {
			t.Errorf("lock file should exist inside fn: %v", statErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithConfigLock: %v", err)
	}

	// Lock file should still exist on disk after release (flock doesn't delete it).
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should persist on disk after WithConfigLock: %v", err)
	}
}
