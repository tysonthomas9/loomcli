package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestGetConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)
	if got := GetConfigDir(); got != tmpDir {
		t.Errorf("GetConfigDir() = %q, want %q", got, tmpDir)
	}

	// With LOOM_CONFIG_DIR unset, bootstrap.LoomDir's testing guard must
	// redirect away from the real ~/.loom (LOOMDEV-14).
	t.Setenv("LOOM_CONFIG_DIR", "placeholder")
	if err := os.Unsetenv("LOOM_CONFIG_DIR"); err != nil {
		t.Fatalf("unset LOOM_CONFIG_DIR: %v", err)
	}
	got := GetConfigDir()
	if got == "" {
		t.Error("GetConfigDir() = \"\", want non-empty test temp dir")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if got == filepath.Join(home, ".loom") {
			t.Errorf("GetConfigDir() = %q, must not be the real ~/.loom under go test", got)
		}
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

func TestValidateRemoteName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: false},
		{name: "origin", input: "origin", wantErr: false},
		{name: "dot", input: "my.remote", wantErr: false},
		{name: "underscore", input: "my_remote", wantErr: false},
		{name: "hyphen", input: "my-remote", wantErr: false},
		{name: "starts with dash", input: "-evil", wantErr: true},
		{name: "space", input: "my remote", wantErr: true},
		{name: "slash", input: "my/remote", wantErr: true},
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

func TestRepoConfigResolveAbsPath(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "ws")
	repo := RepoConfig{Name: "api", Path: "services/api"}
	if got, want := repo.ResolveAbsPath(wsPath), filepath.Join(wsPath, "services/api"); got != want {
		t.Errorf("ResolveAbsPath() = %q, want %q", got, want)
	}

	abs := filepath.Join(t.TempDir(), "repo")
	repo.Path = abs
	if got := repo.ResolveAbsPath(wsPath); got != abs {
		t.Errorf("ResolveAbsPath() absolute = %q, want %q", got, abs)
	}
}

func TestLoadConfigFromStoreProjectsFleetDBWithLocalState(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "api",
		Remote:        "upstream",
		DefaultBranch: "develop",
		Groups:        []string{"backend"},
		SourceRepoID:  "service-api",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path:  "/tmp/ws1",
			Repos: map[string]string{"api": "/tmp/ws1/api"},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	cfg, err := loadConfigFromStore(ctx, st)
	if err != nil {
		t.Fatalf("loadConfigFromStore() error = %v", err)
	}
	if cfg.DefaultWorkspace != "WS1" || cfg.DefaultWorkspaceID != "WS1" {
		t.Fatalf("default workspace = %q/%q, want WS1/WS1", cfg.DefaultWorkspace, cfg.DefaultWorkspaceID)
	}
	ws := cfg.Workspaces["WS1"]
	if ws.Path != "/tmp/ws1" {
		t.Errorf("workspace path = %q, want /tmp/ws1", ws.Path)
	}
	if len(ws.Repos) != 1 {
		t.Fatalf("repos len = %d, want 1", len(ws.Repos))
	}
	repo := ws.Repos[0]
	if repo.Path != "/tmp/ws1/api" || repo.Remote != "upstream" || repo.DefaultBranch != "develop" || repo.SourceRepoID != "service-api" {
		t.Fatalf("repo projection = %+v", repo)
	}
}

func TestLoadConfigFromStoreCopiesDesignFormat(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WSHTML", Name: "HTML WS", DesignFormat: "html"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WSPLAIN", Name: "Plain WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cfg, err := loadConfigFromStore(ctx, st)
	if err != nil {
		t.Fatalf("loadConfigFromStore() error = %v", err)
	}
	if got := cfg.Workspaces["WSHTML"].DesignFormat; got != "html" {
		t.Errorf("WSHTML DesignFormat = %q, want html", got)
	}
	if got := cfg.Workspaces["WSPLAIN"].DesignFormat; got != "" {
		t.Errorf("WSPLAIN DesignFormat = %q, want empty", got)
	}
}
