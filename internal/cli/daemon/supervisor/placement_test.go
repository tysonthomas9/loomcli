package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// actorMockBackend adds the actor-scoped claim/release methods the supervisor
// probes for. The bare clitest mock implements neither, so a placement failure
// would silently skip the release we want to assert on.
type actorMockBackend struct {
	*clitest.MockIssueBackend
	claimedAs []string
	released  []string
}

func (m *actorMockBackend) ClaimIssueAsActor(_ context.Context, id string, _ time.Duration, actor string) error {
	m.claimedAs = append(m.claimedAs, id+"/"+actor)
	return nil
}

func (m *actorMockBackend) ReleaseIssueAsActor(_ context.Context, id string, actor string) error {
	m.released = append(m.released, id+"/"+actor)
	return nil
}

// placementFixture wires a supervisor whose worktree resolver is a pure
// function over a temp root, so no git or filesystem state is needed.
type placementFixture struct {
	sup        *Supervisor
	ap         *AgentProcess
	mock       *actorMockBackend
	root       string
	resolved   []string // (agent, repo) pairs the resolver was asked for
	resolveErr error
}

func newPlacementFixture(t *testing.T, sourceRepo string) *placementFixture {
	t.Helper()
	root := t.TempDir()
	f := &placementFixture{
		root: root,
		mock: &actorMockBackend{MockIssueBackend: clitest.NewMockIssueBackend()},
	}
	f.mock.ReadyResult = []backend.IssueData{{
		ID: "PUPPET-604", IssueType: "task", Status: "open", Priority: 1,
		Title: "Ready", Design: "plan", SourceRepo: sourceRepo,
	}}
	f.sup = &Supervisor{
		IssueBackend: f.mock,
		Repos: []cfgpkg.RepoConfig{
			{Name: "fleet-db", Path: "fleet-db"},
			{Name: "loomcli", Path: "loomcli", DefaultBranch: "v5"},
		},
		FindRepoConfig: func(name string) *cfgpkg.RepoConfig {
			for i := range f.sup.Repos {
				if f.sup.Repos[i].Name == name {
					return &f.sup.Repos[i]
				}
			}
			return nil
		},
		ResolveWorktree: func(agentName, repo string) (string, error) {
			f.resolved = append(f.resolved, agentName+"/"+repo)
			if f.resolveErr != nil {
				return "", f.resolveErr
			}
			return filepath.Join(root, "worktrees", repo, agentName), nil
		},
	}
	f.ap = &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-2", Role: "task", Repo: "fleet-db"},
		RoleConfig:   cfgpkg.RoleConfig{TaskFilter: "has_design"},
		WorktreePath: filepath.Join(root, "worktrees", "fleet-db", "worker-2"),
		RepoConfig:   &f.sup.Repos[0],
	}
	return f
}

// TestApplyTaskPlacement_FollowsClaimedSourceRepo is the ticket's acceptance
// test: agent configured for fleet-db claims a loomcli task and must be handed
// the loomcli worktree (the PUPPET-604 case).
func TestApplyTaskPlacement_FollowsClaimedSourceRepo(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if f.ap.AssignedTaskRepo != "loomcli" {
		t.Fatalf("AssignedTaskRepo = %q, want loomcli", f.ap.AssignedTaskRepo)
	}
	if !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned false")
	}

	want := filepath.Join(f.root, "worktrees", "loomcli", "worker-2")
	if got := f.ap.WorkDir(); got != want {
		t.Fatalf("WorkDir() = %q, want %q", got, want)
	}
	if f.ap.Placement().Repo != "loomcli" {
		t.Fatalf("placement repo = %q, want loomcli", f.ap.Placement().Repo)
	}
	if len(f.resolved) != 1 || f.resolved[0] != "worker-2/loomcli" {
		t.Fatalf("resolver calls = %v, want [worker-2/loomcli]", f.resolved)
	}
	// The base placement is untouched, so the orphan sweeper still knows about it.
	if f.ap.WorktreePath != filepath.Join(f.root, "worktrees", "fleet-db", "worker-2") {
		t.Fatalf("base WorktreePath was mutated: %q", f.ap.WorktreePath)
	}
}

func TestApplyTaskPlacement_UnknownRepoFailsLoudly(t *testing.T) {
	f := newPlacementFixture(t, "ghost-repo")
	before := f.ap.WorkDir()

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned true for an unknown repo")
	}
	if got := f.ap.WorkDir(); got != before {
		t.Fatalf("WorkDir() = %q, want unchanged %q", got, before)
	}
	if f.ap.LastError == nil || !f.ap.LastError.Class.Is(agenterr.WorktreeUnavailableOutcome) {
		t.Fatalf("LastError = %#v, want WorktreeUnavailable", f.ap.LastError)
	}
	msg := f.ap.LastError.Message
	for _, want := range []string{"ghost-repo", "fleet-db", "loomcli"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not name %q", msg, want)
		}
	}
	if len(f.mock.released) != 1 || f.mock.released[0] != "PUPPET-604/worker-2" {
		t.Fatalf("releases = %v, want [PUPPET-604/worker-2]", f.mock.released)
	}
	if f.ap.AssignedTaskID != "" || f.ap.AssignedTaskRepo != "" {
		t.Fatalf("claim not cleared: id=%q repo=%q", f.ap.AssignedTaskID, f.ap.AssignedTaskRepo)
	}
}

func TestApplyTaskPlacement_ResolverErrorDoesNotFallBack(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")
	f.resolveErr = os.ErrNotExist
	before := f.ap.WorkDir()

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned true despite a resolver error")
	}
	// The cycle aborts, so no spawn happens; crucially the agent is NOT left
	// pointed at the previous repo's worktree with a loomcli task in hand.
	if f.ap.Placement().Repo == "loomcli" {
		t.Fatal("placement was published despite the resolver failing")
	}
	if got := f.ap.WorkDir(); got != before {
		t.Fatalf("WorkDir() = %q, want unchanged %q", got, before)
	}
	if f.ap.LastError == nil || !f.ap.LastError.Class.Is(agenterr.WorktreeUnavailableOutcome) {
		t.Fatalf("LastError = %#v, want WorktreeUnavailable", f.ap.LastError)
	}
	if !strings.Contains(f.ap.LastError.Message, "loomcli") {
		t.Fatalf("message %q does not name the repo", f.ap.LastError.Message)
	}
}

// TestApplyTaskPlacement_EmptySourceRepoKeepsPlacement pins the deliberate
// tolerance for tasks that carry no source_repo, so tightening it later is a
// conscious edit rather than an accident.
func TestApplyTaskPlacement_EmptySourceRepoKeepsPlacement(t *testing.T) {
	f := newPlacementFixture(t, "")
	before := f.ap.WorkDir()

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned false for an empty source_repo")
	}
	if got := f.ap.WorkDir(); got != before {
		t.Fatalf("WorkDir() = %q, want unchanged %q", got, before)
	}
	if len(f.resolved) != 0 {
		t.Fatalf("resolver was called %v; an empty source_repo must not re-resolve", f.resolved)
	}
}

func TestApplyTaskPlacement_SameRepoIsNoop(t *testing.T) {
	f := newPlacementFixture(t, "fleet-db")
	f.ap.SetPlacement(AgentPlacement{
		Repo:       "fleet-db",
		WorkDir:    filepath.Join(f.root, "worktrees", "fleet-db", "worker-2"),
		RepoConfig: &f.sup.Repos[0],
	})

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned false")
	}
	if got := f.ap.WorkDir(); got != filepath.Join(f.root, "worktrees", "fleet-db", "worker-2") {
		t.Fatalf("WorkDir() = %q", got)
	}
}

func TestApplyTaskPlacement_RoutingDisabledWhenResolverNil(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")
	f.sup.ResolveWorktree = nil
	before := f.ap.WorkDir()

	if !f.sup.claimTask(f.ap, "") {
		t.Fatal("claimTask returned false")
	}
	if !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("applyTaskPlacement returned false with routing disabled")
	}
	if got := f.ap.WorkDir(); got != before {
		t.Fatalf("WorkDir() = %q, want unchanged %q", got, before)
	}
}

func TestResolveWorkspaceRepoName(t *testing.T) {
	repos := []cfgpkg.RepoConfig{
		{Name: "fleet-db", Path: "fleet-db"},
		{Name: "loomcli", Path: "/Users/x/Work/loom/loomcli", SourceRepoID: "tysonthomas9/loomcli"},
	}
	cases := []struct {
		selector string
		want     string
		ok       bool
	}{
		{"loomcli", "loomcli", true},
		{"tysonthomas9/loomcli", "loomcli", true},
		{"LoomCLI", "loomcli", true},
		{"loomcli.git", "loomcli", true},
		{"git@github.com:tysonthomas9/loomcli.git", "loomcli", true},
		{"https://github.com/tysonthomas9/loomcli", "loomcli", true},
		{"fleet-db", "fleet-db", true},
		{"", "", false},
		{"ghost-repo", "", false},
	}
	for _, tc := range cases {
		got, ok := resolveWorkspaceRepoName(tc.selector, repos)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveWorkspaceRepoName(%q) = (%q, %v), want (%q, %v)", tc.selector, got, ok, tc.want, tc.ok)
		}
	}
}

func writeAgentLock(t *testing.T, dir string, info cli.LockInfo) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli.ResolveLockDir(dir), cli.LockFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptCarriedWorktree(t *testing.T) {
	t.Run("newest task start wins", func(t *testing.T) {
		f := newPlacementFixture(t, "loomcli")
		older := filepath.Join(f.root, "worktrees", "fleet-db", "worker-2")
		newer := filepath.Join(f.root, "worktrees", "loomcli", "worker-2")
		writeAgentLock(t, older, cli.LockInfo{PID: 1, AgentName: "worker-2", TaskID: "PUPPET-1", TaskStartedAt: time.Now().Add(-time.Hour)})
		writeAgentLock(t, newer, cli.LockInfo{PID: 2, AgentName: "worker-2", TaskID: "PUPPET-604", TaskStartedAt: time.Now()})

		f.sup.adoptCarriedWorktree(f.ap)

		if got := f.ap.WorkDir(); got != newer {
			t.Fatalf("WorkDir() = %q, want %q", got, newer)
		}
		if got := f.ap.Placement().Repo; got != "loomcli" {
			t.Fatalf("placement repo = %q, want loomcli", got)
		}
	})

	t.Run("no lock leaves the base placement", func(t *testing.T) {
		f := newPlacementFixture(t, "loomcli")
		base := f.ap.WorkDir()

		f.sup.adoptCarriedWorktree(f.ap)

		if got := f.ap.WorkDir(); got != base {
			t.Fatalf("WorkDir() = %q, want unchanged %q", got, base)
		}
	})

	t.Run("an already-published placement is never overwritten", func(t *testing.T) {
		f := newPlacementFixture(t, "loomcli")
		carried := filepath.Join(f.root, "worktrees", "loomcli", "worker-2")
		f.ap.SetPlacement(AgentPlacement{Repo: "loomcli", WorkDir: carried})
		writeAgentLock(t, filepath.Join(f.root, "worktrees", "fleet-db", "worker-2"),
			cli.LockInfo{PID: 1, AgentName: "worker-2", TaskID: "PUPPET-1", TaskStartedAt: time.Now()})

		f.sup.adoptCarriedWorktree(f.ap)

		if got := f.ap.WorkDir(); got != carried {
			t.Fatalf("WorkDir() = %q, want %q", got, carried)
		}
	})
}

// TestResolveRemoteBranchFollowsPlacement covers the "branch cut from the wrong
// trunk" half of the ticket: loomcli's trunk is v5, everything else is main.
func TestResolveRemoteBranchFollowsPlacement(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")

	if got := f.ap.ResolveRemoteBranch(); got != "origin/main" {
		t.Fatalf("base ResolveRemoteBranch() = %q, want origin/main", got)
	}
	if !f.sup.claimTask(f.ap, "") || !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("claim + placement failed")
	}
	if got := f.ap.ResolveRemoteBranch(); got != "origin/v5" {
		t.Fatalf("ResolveRemoteBranch() = %q, want origin/v5", got)
	}
	if got := f.ap.ResolveRemote(); got != "origin" {
		t.Fatalf("ResolveRemote() = %q, want origin", got)
	}
}

func TestBuildAgentExecCmdUsesEffectiveWorkDir(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")
	if !f.sup.claimTask(f.ap, "") || !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("claim + placement failed")
	}
	want := filepath.Join(f.root, "worktrees", "loomcli", "worker-2")

	cmd, err := buildAgentExecCmd(f.ap, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) < 3 || cmd.Args[2] != want {
		t.Fatalf("role argument = %v, want %q at index 2", cmd.Args, want)
	}
}

func TestManagedWorktreePathsCoversBaseAndEffective(t *testing.T) {
	f := newPlacementFixture(t, "loomcli")
	base := f.ap.WorkDir()
	if !f.sup.claimTask(f.ap, "") || !f.sup.applyTaskPlacement(f.ap) {
		t.Fatal("claim + placement failed")
	}
	f.sup.Agents = []*AgentProcess{f.ap}

	paths := f.sup.managedWorktreePaths()
	want := map[string]bool{base: false, f.ap.WorkDir(): false}
	for _, p := range paths {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Fatalf("managedWorktreePaths() = %v, missing %q", paths, p)
		}
	}
}
