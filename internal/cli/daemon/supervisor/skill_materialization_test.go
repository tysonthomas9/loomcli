package supervisor

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type supervisorSkillStore struct {
	store.SkillStore
	skills []*domain.Skill
	err    error
}

func (s supervisorSkillStore) List(context.Context, string, store.SkillFilter) ([]*domain.Skill, error) {
	return s.skills, s.err
}

type supervisorMaterializeStore struct {
	store.Store
	skills store.SkillStore
}

func (s supervisorMaterializeStore) Skills() store.SkillStore { return s.skills }
func (s supervisorMaterializeStore) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return nil
}

func supervisorSkillTestStore(t *testing.T, name, description, body string, bundles []domain.SkillFileTreeFile) *memstore.Store {
	t.Helper()
	st := memstore.New()
	snapshot, err := domain.BuildSkillFileTree(name, description, []byte(body), bundles)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]domain.WorkspaceFileInput, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	published, err := st.WorkspaceFiles().Publish(t.Context(), "WS", inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef(name), Description: description,
		FileTreeRevision: published.Tree.Revision, Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSpawnAndWaitMaterializationCollisionUsesSpawnFailurePath(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true, Path: "/bin/false", VersionMatchesPin: true}, nil
	})
	target := t.TempDir()
	skillDir := filepath.Join(target, ".agents", "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "README.md"), []byte("user file\n"), 0o644); err != nil {
		t.Fatalf("write collision: %v", err)
	}
	st := supervisorSkillTestStore(t, "alpha", "alpha", "body", []domain.SkillFileTreeFile{{
		Path: "readme.md", Bytes: []byte("managed"), MediaType: "text/markdown",
	}})
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = st
	s.Concurrency = NewConcurrencyTracker(nil)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: target,
	}
	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("failed to acquire test concurrency slot")
	}

	s.spawnAndWait(ap)

	if ap.Cmd != nil {
		t.Fatalf("Cmd = %v, want no subprocess after materialization collision", ap.Cmd)
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.SpawnFailureOutcome) {
		t.Fatalf("LastError = %+v, want spawn failure classification", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "README.md") || !strings.Contains(ap.LastError.Message, "readme.md") {
		t.Fatalf("spawn failure message = %q, want both colliding paths", ap.LastError.Message)
	}
}

func TestSpawnAgentContinuesWhenSkillStoreIsUnavailable(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true, Path: "/bin/false", VersionMatchesPin: true}, nil
	})
	target := t.TempDir()
	outage := &url.Error{Op: "GET", URL: "http://fleet-db/skills", Err: syscall.ECONNREFUSED}
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = supervisorMaterializeStore{skills: supervisorSkillStore{err: outage}}
	s.EmitEvent = func(events.Event) {} // spawn success path emits AgentStarted
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: target,
	}

	spawnErr := s.spawnAgent(ap)
	if spawnErr != nil && strings.Contains(spawnErr.Error(), "materialize skills") {
		t.Fatalf("skill-store outage blocked spawn: %v", spawnErr)
	}
	if spawnErr == nil {
		if ap.Cmd == nil {
			t.Fatal("Cmd = nil after successful spawn")
		}
		_ = s.waitForAgent(ap)
	}
	if _, err := os.Lstat(filepath.Join(target, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("skill outage touched target projection: %v", err)
	}
	hookConfig, err := os.ReadFile(filepath.Join(target, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read worker hook config: %v", err)
	}
	if !strings.Contains(string(hookConfig), "loom skill materialize") {
		t.Fatalf("worker hook config = %q", hookConfig)
	}
}

func TestEnsureHookConfigWarnsAndContinuesOnMalformedJSON(t *testing.T) {
	logs := captureSlog(t)
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, ".codex", "hooks.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-a"}, WorktreePath: target}

	s.ensureHookConfig(ap)

	if !strings.Contains(logs.String(), "hook configuration failed") || !strings.Contains(logs.String(), "continuing") {
		t.Fatalf("warning log = %q", logs.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "not-json" {
		t.Fatalf("malformed hook config changed: data=%q err=%v", data, err)
	}
}

func TestMaterializeSkillsWarnsWhenControlStoreIsMissing(t *testing.T) {
	logs := captureSlog(t)
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = nil
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
	}

	if err := s.materializeSkills(ap); err != nil {
		t.Fatalf("materializeSkills: %v", err)
	}
	for _, want := range []string{"skill", "not configured", "continuing", "worker-a", "WS"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("warning log = %q, want %q", logs.String(), want)
		}
	}
}

func TestMaterializeIdleSkillsConvergesNoWorkWorktree(t *testing.T) {
	target := t.TempDir()
	st := supervisorSkillTestStore(t, "alpha", "alpha", "body", nil)
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = st
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: target,
		LastNoWork:   true,
	}

	s.materializeIdleSkills(ap)

	if _, err := os.Stat(filepath.Join(target, ".agents", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("idle no-work loop did not materialize skills: %v", err)
	}
}

func TestMaterializeIdleSkillsSkipsNonNoWorkFailures(t *testing.T) {
	target := t.TempDir()
	st := supervisorSkillTestStore(t, "alpha", "alpha", "body", nil)
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = st
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: target,
		LastNoWork:   false,
	}

	s.materializeIdleSkills(ap)

	if _, err := os.Lstat(filepath.Join(target, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("non-no-work failure touched target projection: %v", err)
	}
}

func TestSpawnAgentStopsWhenSkillListingIsCanceled(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true, Path: "/bin/false", VersionMatchesPin: true}, nil
	})
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = supervisorMaterializeStore{skills: supervisorSkillStore{err: context.Canceled}}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
	}

	spawnErr := s.spawnAgent(ap)
	if spawnErr == nil {
		if ap.Cmd != nil {
			_ = s.waitForAgent(ap)
		}
		t.Fatal("spawnAgent error = nil, want canceled materialization to stop spawn")
	}
	if !errors.Is(spawnErr, context.Canceled) {
		t.Fatalf("spawnAgent error = %v, want context.Canceled", spawnErr)
	}
	if ap.Cmd != nil {
		t.Fatalf("Cmd = %v, want no subprocess after canceled materialization", ap.Cmd)
	}
}
