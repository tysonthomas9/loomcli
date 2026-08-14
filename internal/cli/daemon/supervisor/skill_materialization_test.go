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
	st := supervisorMaterializeStore{skills: supervisorSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []domain.SkillFile{{Path: "readme.md", Content: "managed"}},
	}}}}
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
