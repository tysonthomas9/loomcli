package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
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
	// redirect away from the real ~/.loom.
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

func TestAgentEntryShouldSuperviseSkipsLeadRoles(t *testing.T) {
	tests := []struct {
		name  string
		entry AgentEntry
		roles map[string]RoleConfig
		want  bool
	}{
		{name: "task default runs", entry: AgentEntry{Worktree: "worker", Role: "task"}, want: true},
		{name: "lead interactive is not daemon supervised", entry: AgentEntry{Worktree: "lead", Role: "lead"}, want: false},
		{name: "orchestrator interactive is not daemon supervised", entry: AgentEntry{Worktree: "lead", Role: "orchestrator"}, want: false},
		{
			name:  "custom interactive kind is not daemon supervised",
			entry: AgentEntry{Worktree: "operator", Role: "operator"},
			roles: map[string]RoleConfig{
				"operator": {Kind: string(domain.RoleKindInteractive)},
			},
			want: false,
		},
		{
			name:  "interactive kind ignores running desired state",
			entry: AgentEntry{Worktree: "operator", Role: "operator", DesiredState: domain.AgentDesiredRunning},
			roles: map[string]RoleConfig{
				"operator": {Kind: string(domain.RoleKindInteractive)},
			},
			want: false,
		},
		{
			name:  "worker kind uses desired state",
			entry: AgentEntry{Worktree: "operator", Role: "operator", DesiredState: domain.AgentDesiredRunning},
			roles: map[string]RoleConfig{
				"operator": {Kind: string(domain.RoleKindWorker)},
			},
			want: true,
		},
		{name: "stopped worker does not run", entry: AgentEntry{Worktree: "worker", Role: "task", DesiredState: domain.AgentDesiredStopped}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.ShouldSuperviseWithRoles(tt.roles, "", time.Now()); got != tt.want {
				t.Fatalf("ShouldSuperviseWithRoles() = %v, want %v", got, tt.want)
			}
		})
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

func TestLoadDaemonConfigFromStoreProjectsRoleLabels(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:  "WS1",
		Name:          "task",
		Labels:        []string{"plan-ready", "approved"},
		ExcludeLabels: []string{"plan-reviewed"},
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}

	dc, err := loadDaemonConfigFromStore(ctx, st, "WS1", newDefaultDaemonConfig(), t.TempDir())
	if err != nil {
		t.Fatalf("loadDaemonConfigFromStore() error = %v", err)
	}
	role, ok := dc.Roles["task"]
	if !ok {
		t.Fatal("role task not projected")
	}
	if len(role.Labels) != 2 || role.Labels[0] != "plan-ready" || role.Labels[1] != "approved" {
		t.Errorf("Labels = %v, want [plan-ready approved]", role.Labels)
	}
	if len(role.ExcludeLabels) != 1 || role.ExcludeLabels[0] != "plan-reviewed" {
		t.Errorf("ExcludeLabels = %v, want [plan-reviewed]", role.ExcludeLabels)
	}
}

// TestAgentEntryShouldSuperviseDrainIsOneShot pins the drain half of the
// supervision predicate: a drain parks only while it still belongs to the
// asking supervisor and has not lapsed.
func TestAgentEntryShouldSuperviseDrainIsOneShot(t *testing.T) {
	const thisNode = "loom-supervisor-h-222"
	const otherNode = "loom-supervisor-h-111"
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name  string
		entry AgentEntry
		want  bool
	}{
		{
			name: "drain for this supervisor parks",
			entry: AgentEntry{Worktree: "w", Role: "task",
				DesiredState: domain.AgentDesiredDraining, DrainNodeID: thisNode, DrainExpiresAt: &future},
			want: false,
		},
		{
			name: "untargeted drain parks, honoring a yield not yet stamped",
			entry: AgentEntry{Worktree: "w", Role: "task",
				DesiredState: domain.AgentDesiredDraining},
			want: false,
		},
		{
			name: "drain from a previous supervisor no longer parks",
			entry: AgentEntry{Worktree: "w", Role: "task",
				DesiredState: domain.AgentDesiredDraining, DrainNodeID: otherNode, DrainExpiresAt: &future},
			want: true,
		},
		{
			name: "expired drain no longer parks",
			entry: AgentEntry{Worktree: "w", Role: "task",
				DesiredState: domain.AgentDesiredDraining, DrainNodeID: thisNode, DrainExpiresAt: &past},
			want: true,
		},
		{
			name: "stopped parks regardless of drain metadata",
			entry: AgentEntry{Worktree: "w", Role: "task",
				DesiredState: domain.AgentDesiredStopped, DrainNodeID: otherNode, DrainExpiresAt: &past},
			want: false,
		},
		{
			name: "an interactive role is excluded even when the drain lapsed",
			entry: AgentEntry{Worktree: "lead", Role: "lead",
				DesiredState: domain.AgentDesiredDraining, DrainNodeID: otherNode},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.ShouldSuperviseWithRoles(nil, thisNode, now); got != tt.want {
				t.Fatalf("ShouldSuperviseWithRoles() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAgentEntryEqualDetectsDrainChanges: a drain change has to register as a
// config diff, or the reconciler would never act on it.
func TestAgentEntryEqualDetectsDrainChanges(t *testing.T) {
	t1 := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	base := AgentEntry{Worktree: "w", Role: "task", DesiredState: domain.AgentDesiredDraining}

	sameAsBase := AgentEntry{Worktree: "w", Role: "task", DesiredState: domain.AgentDesiredDraining}
	if !base.Equal(sameAsBase) {
		t.Fatal("two identical entries must compare equal")
	}
	withNode := base
	withNode.DrainNodeID = "node-1"
	if base.Equal(withNode) {
		t.Error("Equal ignored a drain_node_id change")
	}
	withExpiry := base
	withExpiry.DrainExpiresAt = &t1
	if base.Equal(withExpiry) {
		t.Error("Equal ignored a drain_expires_at change")
	}
	otherExpiry := base
	otherExpiry.DrainExpiresAt = &t2
	if withExpiry.Equal(otherExpiry) {
		t.Error("Equal ignored a differing drain_expires_at")
	}
	sameExpiry := base
	sameInstant := t1
	sameExpiry.DrainExpiresAt = &sameInstant
	if !withExpiry.Equal(sameExpiry) {
		t.Error("Equal must compare expiries by instant, not by pointer")
	}
}
