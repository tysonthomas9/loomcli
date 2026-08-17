package workspace

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// `workspace apply` used to store an agent without ever creating its worktree.
// On a headless deployment nothing provisions one later, so the next daemon
// restart hit a fatal boot error — with the config already written. These tests
// pin both halves of the fix: provision first, and write nothing if you cannot.

const testWorkspaceKey = "TEST"

// applyFixture returns a store handle, the workspace root, and the seeded repo
// path. The state cache is isolated per test, so nothing here can touch the
// developer's real ~/.loom/state.json.
func applyFixture(t *testing.T, repoNames ...string) (*bootstrap.StoreHandle, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	wsPath := t.TempDir()
	st := memstore.New()
	ctx := context.Background()
	for _, name := range repoNames {
		createGitRepo(t, filepath.Join(wsPath, name))
		if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: testWorkspaceKey, Name: name}); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
	}
	if err := bootstrap.MutateWorkspaceLocalState(testWorkspaceKey, func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = wsPath
		return nil
	}); err != nil {
		t.Fatalf("seed local workspace state: %v", err)
	}
	return &bootstrap.StoreHandle{Store: st}, wsPath
}

// worktreeSpec is a one-agent spec whose role is a plain worker.
func worktreeSpec(agent cfgpkg.AgentEntry) *cfgpkg.DaemonConfig {
	return &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"task": {Kind: "worker", TaskFilter: "any"}},
		Agents: []cfgpkg.AgentEntry{agent},
	}
}

// captureStdout collects what the helpers print, so the dry-run report can be
// asserted on rather than merely not-crashing.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func storeIsEmpty(t *testing.T, h *bootstrap.StoreHandle) bool {
	t.Helper()
	ctx := context.Background()
	roles, err := h.Store.Roles().List(ctx, testWorkspaceKey)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	agents, err := h.Store.Agents().List(ctx, testWorkspaceKey)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	return len(roles) == 0 && len(agents) == 0
}

func TestApply_ProvisionsMissingWorktrees(t *testing.T) {
	h, wsPath := applyFixture(t, "alpha")
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task", Auto: true})

	err := captureApplyErr(t, func() error {
		if err := ensureSpecWorktrees(context.Background(), h, testWorkspaceKey, spec); err != nil {
			return err
		}
		return applySpec(context.Background(), h, testWorkspaceKey, spec, parsePresence(nil))
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	target := localworkspace.AgentWorktreePath(wsPath, "alpha", "planner")
	if _, statErr := os.Stat(filepath.Join(target, ".git")); statErr != nil {
		t.Fatalf("worktree %s was not provisioned: %v", target, statErr)
	}
	agent, err := h.Store.Agents().Get(context.Background(), testWorkspaceKey, "planner")
	if err != nil || agent == nil {
		t.Fatalf("agent was not created: %v", err)
	}
}

// The invariant the command's doc comment promises: a provisioning failure
// aborts with NOTHING written — no role, no agent, no daemon profile.
func TestApply_RefusesWhenWorktreeCannotBeProvisioned(t *testing.T) {
	h, _ := applyFixture(t, "alpha")
	// An agent bound to a repo the workspace does not have.
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task", Repos: []string{"nonexistent"}})

	err := captureApplyErr(t, func() error {
		if err := ensureSpecWorktrees(context.Background(), h, testWorkspaceKey, spec); err != nil {
			return err
		}
		return applySpec(context.Background(), h, testWorkspaceKey, spec, parsePresence(nil))
	})
	if err == nil {
		t.Fatal("expected apply to refuse an agent whose worktree cannot be provisioned")
	}
	if !strings.Contains(err.Error(), `agent "planner"`) || !strings.Contains(err.Error(), "cannot provision worktree") {
		t.Fatalf("error must name the agent and the reason, got: %v", err)
	}
	if !storeIsEmpty(t, h) {
		t.Fatal("apply wrote to the store despite refusing; a half-applied pipeline still starts")
	}
}

func TestApplyDryRun_ReportsMissingWorktreesAndWritesNothing(t *testing.T) {
	h, wsPath := applyFixture(t, "alpha")
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task"})

	var err error
	out := captureStdout(t, func() {
		err = reportWorktreePlan(context.Background(), h, testWorkspaceKey, spec)
	})
	if err != nil {
		t.Fatalf("dry run on a provisionable spec: %v", err)
	}
	if !strings.Contains(out, "0 present, 1 to create") {
		t.Fatalf("dry-run report = %q, want a per-agent present/to-create count", out)
	}
	if !strings.Contains(out, filepath.Join("worktrees", "alpha", "planner")) {
		t.Fatalf("dry-run report = %q, want the path it would create", out)
	}
	if _, statErr := os.Stat(localworkspace.AgentWorktreePath(wsPath, "alpha", "planner")); !os.IsNotExist(statErr) {
		t.Fatal("dry run created a worktree; it must touch no files")
	}
	if !storeIsEmpty(t, h) {
		t.Fatal("dry run wrote to the store")
	}
}

// A dry run must exit non-zero when any agent has a hard problem, and report
// EVERY problem — one round-trip per typo is what validateSpec exists to avoid.
func TestApplyDryRun_AggregatesHardProblems(t *testing.T) {
	h, _ := applyFixture(t, "alpha")
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task", Repos: []string{"nope"}})
	spec.Agents = append(spec.Agents, cfgpkg.AgentEntry{Worktree: "critic", Role: "task", Repos: []string{"alsonope"}})

	var err error
	stderr := captureStderr(t, func() {
		err = reportWorktreePlan(context.Background(), h, testWorkspaceKey, spec)
	})
	if err == nil {
		t.Fatal("expected a non-nil error so the command exits non-zero")
	}
	for _, agent := range []string{`agent "planner"`, `agent "critic"`} {
		if !strings.Contains(stderr, agent) {
			t.Fatalf("problem list = %q, want it to include %s", stderr, agent)
		}
	}
}

// A distributed or headless workspace legitimately has no checkout here. That
// is a note, not a failure — refusing would break managing such a workspace.
func TestApply_NoLocalWorkspacePath_WarnsAndSucceeds(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	h := &bootstrap.StoreHandle{Store: memstore.New()}
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task"})

	var ensureErr, planErr error
	out := captureStdout(t, func() {
		ensureErr = ensureSpecWorktrees(context.Background(), h, testWorkspaceKey, spec)
		planErr = reportWorktreePlan(context.Background(), h, testWorkspaceKey, spec)
	})
	if ensureErr != nil || planErr != nil {
		t.Fatalf("ensure = %v, plan = %v; a workspace with no local checkout must not fail", ensureErr, planErr)
	}
	if !strings.Contains(out, "no local checkout on this machine") {
		t.Fatalf("output = %q, want the no-local-checkout note", out)
	}
}

// A lead/terminal agent gets no worktree, so demanding one would refuse specs
// that are perfectly valid.
func TestApply_SkipsInteractiveRoleAgents(t *testing.T) {
	h, wsPath := applyFixture(t, "alpha")
	spec := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"lead": {Kind: "interactive"}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "boss", Role: "lead", Repos: []string{"nonexistent"}}},
	}

	if err := captureApplyErr(t, func() error {
		return ensureSpecWorktrees(context.Background(), h, testWorkspaceKey, spec)
	}); err != nil {
		t.Fatalf("interactive agent must be skipped, got: %v", err)
	}
	if _, statErr := os.Stat(localworkspace.AgentWorktreePath(wsPath, "alpha", "boss")); !os.IsNotExist(statErr) {
		t.Fatal("an interactive agent must not get a worktree")
	}
	var planErr error
	captureStdout(t, func() {
		planErr = reportWorktreePlan(context.Background(), h, testWorkspaceKey, spec)
	})
	if planErr != nil {
		t.Fatalf("plan must skip interactive agents too, got: %v", planErr)
	}
}

// Re-running apply after the worktrees exist is a no-op, not a failure.
func TestApply_ReRunIsIdempotent(t *testing.T) {
	h, _ := applyFixture(t, "alpha")
	spec := worktreeSpec(cfgpkg.AgentEntry{Worktree: "planner", Role: "task"})

	for i := 0; i < 2; i++ {
		if err := captureApplyErr(t, func() error {
			return ensureSpecWorktrees(context.Background(), h, testWorkspaceKey, spec)
		}); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

// captureApplyErr runs fn with stdout swallowed, returning only its error.
func captureApplyErr(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = fn() })
	return err
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("pipe: %v", perr)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
