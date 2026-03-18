package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLoadConfig_VersionWarningDedup(t *testing.T) {
	// Reset the once guard so this test starts clean.
	resetConfigVersionWarnOnce()
	t.Cleanup(resetConfigVersionWarnOnce)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Write a version-0 config (version field omitted defaults to 0,
	// which is below CurrentConfigVersion=1).
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

	const warnSubstr = "Run 'loom config migrate' to upgrade"

	// First call: warning should appear.
	stderr1 := captureStderr(t, func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() #1 error = %v", err)
		}
		if cfg == nil {
			t.Fatal("LoadConfig() #1 returned nil")
		}
	})
	if count := strings.Count(stderr1, warnSubstr); count != 1 {
		t.Errorf("first LoadConfig(): want exactly 1 warning, got %d; stderr = %q", count, stderr1)
	}

	// Second call: warning should be suppressed by sync.Once.
	stderr2 := captureStderr(t, func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() #2 error = %v", err)
		}
		if cfg == nil {
			t.Fatal("LoadConfig() #2 returned nil")
		}
	})
	if strings.Contains(stderr2, warnSubstr) {
		t.Errorf("second LoadConfig(): want no warning, got stderr = %q", stderr2)
	}

	// Reset the guard and call again: warning should reappear.
	resetConfigVersionWarnOnce()
	stderr3 := captureStderr(t, func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() #3 error = %v", err)
		}
		if cfg == nil {
			t.Fatal("LoadConfig() #3 returned nil")
		}
	})
	if count := strings.Count(stderr3, warnSubstr); count != 1 {
		t.Errorf("after reset, LoadConfig(): want exactly 1 warning, got %d; stderr = %q", count, stderr3)
	}
}
