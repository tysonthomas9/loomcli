package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
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

// supervisorLeaseStore fails every acquire with a fixed error, so a spawn can be
// driven down each lease-failure branch.
type supervisorLeaseStore struct {
	err error
}

func (s supervisorLeaseStore) Acquire(context.Context, store.SkillMaterializationLeaseAcquire) (*domain.SkillMaterializationLease, error) {
	return nil, s.err
}

func (s supervisorLeaseStore) Renew(context.Context, string, string, string, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}

func (s supervisorLeaseStore) Release(context.Context, string, string, string) error { return nil }

type supervisorLeasedStore struct {
	store.Store
	skills store.SkillStore
	leases store.SkillMaterializationLeaseStore
}

func (s supervisorLeasedStore) Skills() store.SkillStore { return s.skills }
func (s supervisorLeasedStore) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return s.leases
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
	st := supervisorMaterializeStore{skills: supervisorSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
	}}}}
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
	st := supervisorMaterializeStore{skills: supervisorSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
	}}}}
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

// stubLoomExecutable points the spawned "agent" at a real no-op binary for one
// test. TestMain's /bin/false is not present on every supported host, and a
// spawn test that asserts the child started cannot tolerate a missing exec.
func stubLoomExecutable(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("false")
	if err != nil {
		t.Skipf("no false(1) on PATH: %v", err)
	}
	prev := loomExecutablePath
	loomExecutablePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { loomExecutablePath = prev })
}

// The regression PUPPET-255 fixes end to end: a fleet-db that does not serve
// the lease routes must not stop an agent from starting.
func TestSpawnAgentContinuesWhenLeaseRouteIsMissing(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	target := t.TempDir()
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true, Path: "/bin/false", VersionMatchesPin: true}, nil
	})
	// TestMain's package-wide /bin/false is absent on some hosts (macOS ships
	// only /usr/bin/false), and this test asserts the process actually starts —
	// so resolve a stand-in that is guaranteed to exec here.
	stubLoomExecutable(t)
	routeMissing := fmt.Errorf("fleetdb: POST /api/v1/WS/skill-materialization-leases: HTTP 404: %w",
		domain.ErrSkillMaterializationLeaseRouteMissing)
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = supervisorLeasedStore{
		skills: supervisorSkillStore{},
		leases: supervisorLeaseStore{err: routeMissing},
	}
	s.EmitEvent = func(events.Event) {}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: target,
	}

	if err := s.spawnAgent(ap); err != nil {
		t.Fatalf("spawnAgent: %v, want the missing lease route to warn and continue", err)
	}
	if ap.Cmd == nil {
		t.Fatal("Cmd = nil, want the agent process started")
	}
	_ = s.waitForAgent(ap)
}

// The other side of the same branch: a genuine 409 conflict is not an outage
// and must still fail the spawn.
func TestSpawnAgentFailsOnLeaseConflictError(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true, Path: "/bin/false", VersionMatchesPin: true}, nil
	})
	conflict := fmt.Errorf("fleetdb: POST /api/v1/WS/skill-materialization-leases: HTTP 409: %w", domain.ErrConflict)
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Backend: "codex"})
	s.WorkspaceID = "WS"
	s.ControlStore = supervisorLeasedStore{
		skills: supervisorSkillStore{},
		leases: supervisorLeaseStore{err: conflict},
	}
	s.EmitEvent = func(events.Event) {}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-a", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
	}

	err := s.spawnAgent(ap)
	if err == nil {
		t.Fatal("spawnAgent = nil, want a lease conflict to fail the spawn")
	}
	if !strings.Contains(err.Error(), "materialize skills") {
		t.Fatalf("spawnAgent error = %v, want a materialize skills failure", err)
	}
	if ap.Cmd != nil {
		t.Fatalf("Cmd = %v, want no subprocess after a lease conflict", ap.Cmd)
	}
}
