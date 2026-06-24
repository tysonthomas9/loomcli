//go:build e2e
// +build e2e

package stackpublish_test

// Real GitHub smoke tests for the stack publisher's critical flows — the exact
// scenarios validated by hand against spr and git-town: initial stacked publish,
// idempotent re-run, drop-a-unit, and reorder/swap (which must produce NO
// ghost-merge and NO 422). They are gated on env so plain `go test` skips them:
//
//	LOOM_STACK_E2E=1
//	LOOM_STACK_E2E_REPO=owner/name   (an existing repo you can open PRs on)
//	GITHUB_TOKEN/GH_TOKEN, or `gh auth login`
//
// The repo is left in place (token has no delete_repo scope); the test cleans up
// only the PRs and branches it creates, namespaced by a unique stack id per run.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

const e2eWS = "E2E"

func e2eToken(t *testing.T) string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	t.Fatal("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or `gh auth login`)")
	return ""
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// materialize (re)creates each unit's local branch at base+<task>.txt, in order.
// Re-running with a different order regenerates the branch commits — exactly the
// SHA-regenerating behavior a real Loom assembler exhibits.
func materialize(t *testing.T, repoPath string, id sl.StackID, order []string, rootBase string) {
	t.Helper()
	for i, task := range order {
		base := rootBase
		if i > 0 {
			base = sl.OutputBranchName(id, order[i-1])
		}
		branch := sl.OutputBranchName(id, task)
		gitT(t, repoPath, "checkout", "-B", branch, base)
		// Content is unique per run (id carries a UnixNano suffix) so a re-run
		// against a repo whose main was polluted by a prior merge still commits.
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, task+".txt"), []byte(string(id)+" "+task+"\n"), 0o644))
		gitT(t, repoPath, "add", task+".txt")
		gitT(t, repoPath, "commit", "-m", fmt.Sprintf("%s %s", id, task))
	}
	gitT(t, repoPath, "checkout", "main")
}

// prsByHead returns the namespace's PRs keyed by head ref (preferring open).
func prsByHead(t *testing.T, f stackpublish.Forge, owner, repo string, id sl.StackID) map[string]stackpublish.PR {
	t.Helper()
	prs, err := f.ListStackPRs(context.Background(), owner, repo, sl.StackBranchPrefix(id))
	require.NoError(t, err)
	m := map[string]stackpublish.PR{}
	for _, p := range prs {
		if ex, ok := m[p.Head]; ok && ex.State == "open" && p.State != "open" {
			continue
		}
		m[p.Head] = p
	}
	return m
}

func TestE2EStackPublisher(t *testing.T) {
	if os.Getenv("LOOM_STACK_E2E") != "1" {
		t.Skip("set LOOM_STACK_E2E=1 (and LOOM_STACK_E2E_REPO=owner/name) to run real GitHub smoke tests")
	}
	slug := strings.TrimSpace(os.Getenv("LOOM_STACK_E2E_REPO"))
	require.NotEmpty(t, slug, "LOOM_STACK_E2E_REPO=owner/name is required")
	parts := strings.SplitN(slug, "/", 2)
	require.Len(t, parts, 2, "LOOM_STACK_E2E_REPO must be owner/name")
	owner, repo := parts[0], parts[1]
	token := e2eToken(t)

	ctx := context.Background()
	forge := stackpublish.NewGitHubForge(token, nil, "")

	// Unique stack id per run so the shared repo never collides.
	id := sl.StackID(fmt.Sprintf("manual:e2e-%d", time.Now().UnixNano()))
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))

	repoPath := t.TempDir()
	out, err := exec.Command("gh", "repo", "clone", slug, repoPath).CombinedOutput()
	require.NoErrorf(t, err, "gh repo clone: %s", out)

	rec := &stackpublish.Reconciler{Store: store, Forge: forge}

	// Cleanup: close all PRs and delete all branches under this run's namespace.
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})

	br := func(task string) string { return sl.OutputBranchName(id, task) }

	// --- Scenario 1: initial stacked publish ---------------------------------
	for _, task := range []string{"T1", "T2", "T3"} {
		base := map[string]string{"T1": "", "T2": "T1", "T3": "T2"}[task]
		_, e := store.AddNode(ctx, e2eWS, id, task, base, "")
		require.NoError(t, e)
	}
	materialize(t, repoPath, id, []string{"T1", "T2", "T3"}, "main")

	rep, err := rec.Publish(ctx, e2eWS, id, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	require.Len(t, rep.Created, 3)

	prs := prsByHead(t, forge, owner, repo, id)
	require.Len(t, prs, 3)
	assert.Equal(t, "main", prs[br("T1")].Base)
	assert.Equal(t, br("T1"), prs[br("T2")].Base)
	assert.Equal(t, br("T2"), prs[br("T3")].Base)
	baseNum := map[string]int{"T1": prs[br("T1")].Number, "T2": prs[br("T2")].Number, "T3": prs[br("T3")].Number}

	// Stack listing is written into each PR body.
	for _, task := range []string{"T1", "T2", "T3"} {
		assert.Containsf(t, prs[br(task)].Body, "Loom stack", "%s PR body has the stack listing", task)
	}
	// Live status enrichment: rows carry health + a next-to-merge marker.
	status, serr := rec.StackStatus(ctx, e2eWS, id, repoPath)
	require.NoError(t, serr)
	assert.True(t, status.Live)
	require.Len(t, status.Rows, 3)
	assert.True(t, status.Rows[0].NextToMerge, "T1 (bottom) is next to merge")
	assert.False(t, status.Rows[1].NextToMerge)
	assert.NotEmpty(t, status.Rows[0].Mergeable, "mergeable is populated from GitHub")

	// --- Scenario 2: idempotent re-run (zero churn) --------------------------
	rep, err = rec.Publish(ctx, e2eWS, id, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	assert.Len(t, rep.Created, 0)
	assert.Len(t, rep.Reparented, 0)
	assert.Len(t, rep.Skipped, 3, "re-publish with no change must be all skips")
	prs = prsByHead(t, forge, owner, repo, id)
	assert.Equal(t, baseNum["T1"], prs[br("T1")].Number, "PR numbers stable across idempotent re-run")

	// --- Scenario 3: drop the middle unit ------------------------------------
	require.NoError(t, store.RemoveNode(ctx, e2eWS, id, "T2"))
	materialize(t, repoPath, id, []string{"T1", "T3"}, "main") // T3 reparented onto T1
	_, err = rec.Publish(ctx, e2eWS, id, repoPath, stackpublish.Options{})
	require.NoError(t, err)

	prs = prsByHead(t, forge, owner, repo, id)
	assert.Equal(t, "closed", prs[br("T2")].State, "dropped unit's PR is closed")
	assert.False(t, prs[br("T2")].Merged, "dropped unit must NOT be merged")
	assert.Equal(t, "open", prs[br("T3")].State)
	assert.Equal(t, br("T1"), prs[br("T3")].Base, "T3 reparents onto T1 after the drop")
	assert.Equal(t, baseNum["T3"], prs[br("T3")].Number, "T3's PR identity is preserved across the drop")

	// --- Scenario 4: reorder/swap (the spr ghost-merge + git-town 422 case) ---
	// Fresh stack so the swap starts from a clean baseline.
	id2 := sl.StackID(fmt.Sprintf("manual:e2e-swap-%d", time.Now().UnixNano()))
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id2, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))
	br2 := func(task string) string { return sl.OutputBranchName(id2, task) }
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id2) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})
	for _, task := range []string{"U1", "U2", "U3"} {
		base := map[string]string{"U1": "", "U2": "U1", "U3": "U2"}[task]
		_, e := store.AddNode(ctx, e2eWS, id2, task, base, "")
		require.NoError(t, e)
	}
	materialize(t, repoPath, id2, []string{"U1", "U2", "U3"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id2, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	swapNum := func(task string) int { return prsByHead(t, forge, owner, repo, id2)[br2(task)].Number }
	preU3 := swapNum("U3")

	// Swap U2 and U3 → desired U1 -> U3 -> U2.
	require.NoError(t, store.MoveNode(ctx, e2eWS, id2, "U3", "U1"))
	materialize(t, repoPath, id2, []string{"U1", "U3", "U2"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id2, repoPath, stackpublish.Options{})
	require.NoError(t, err, "reorder publish must not 422")

	prs = prsByHead(t, forge, owner, repo, id2)
	for _, task := range []string{"U1", "U2", "U3"} {
		assert.Falsef(t, prs[br2(task)].Merged, "%s must NOT be ghost-merged by the reorder", task)
		assert.Equalf(t, "open", prs[br2(task)].State, "%s PR stays open", task)
	}
	assert.Equal(t, "main", prs[br2("U1")].Base)
	assert.Equal(t, br2("U1"), prs[br2("U3")].Base, "U3 now bases on U1")
	assert.Equal(t, br2("U3"), prs[br2("U2")].Base, "U2 now bases on U3")
	assert.Equal(t, preU3, prs[br2("U3")].Number, "U3's PR identity preserved across reorder (no replacement PR)")

	// --- Scenario 5: external merge → unit terminal, descendant auto-slides ----
	// Validates decision 1 (fully control-plane authoritative): a PR merged on
	// GitHub is terminal and its descendants slide to RootBase on the next publish.
	id3 := sl.StackID(fmt.Sprintf("manual:e2e-merge-%d", time.Now().UnixNano()))
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id3, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))
	br3 := func(task string) string { return sl.OutputBranchName(id3, task) }
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id3) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})
	for _, task := range []string{"M1", "M2"} {
		base := map[string]string{"M1": "", "M2": "M1"}[task]
		_, e := store.AddNode(ctx, e2eWS, id3, task, base, "")
		require.NoError(t, e)
	}
	materialize(t, repoPath, id3, []string{"M1", "M2"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id3, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	m1num := prsByHead(t, forge, owner, repo, id3)[br3("M1")].Number
	require.NotZero(t, m1num)

	// Merge M1 externally, as a human would.
	mout, merr := exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", m1num),
		"--repo", slug, "--merge", "--delete-branch=false").CombinedOutput()
	require.NoErrorf(t, merr, "gh pr merge: %s", mout)

	_, err = rec.Publish(ctx, e2eWS, id3, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	prs = prsByHead(t, forge, owner, repo, id3)
	assert.True(t, prs[br3("M1")].Merged, "M1's PR is merged")
	assert.Equal(t, "open", prs[br3("M2")].State)
	assert.Equal(t, "main", prs[br3("M2")].Base, "M2 auto-slides to RootBase past the merged M1")
	for _, n := range func() []sl.Node { ns, _ := store.ListNodes(ctx, e2eWS, id3); return ns }() {
		if n.TaskID == "M1" {
			assert.Equal(t, sl.NodeStateMerged, n.State, "M1 recorded as merged (terminal)")
		}
	}

	// --- Scenario 6: forest — two parallel linear chains off the same base ----
	id4 := sl.StackID(fmt.Sprintf("manual:e2e-forest-%d", time.Now().UnixNano()))
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id4, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))
	br4 := func(task string) string { return sl.OutputBranchName(id4, task) }
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id4) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})
	for _, e := range []struct{ id, base string }{{"A1", ""}, {"A2", "A1"}, {"B1", ""}, {"B2", "B1"}} {
		_, ae := store.AddNode(ctx, e2eWS, id4, e.id, e.base, "")
		require.NoError(t, ae)
	}
	materialize(t, repoPath, id4, []string{"A1", "A2"}, "main")
	materialize(t, repoPath, id4, []string{"B1", "B2"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id4, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	prs = prsByHead(t, forge, owner, repo, id4)
	require.Len(t, prs, 4)
	assert.Equal(t, "main", prs[br4("A1")].Base)
	assert.Equal(t, br4("A1"), prs[br4("A2")].Base)
	assert.Equal(t, "main", prs[br4("B1")].Base, "second chain roots on main independently")
	assert.Equal(t, br4("B1"), prs[br4("B2")].Base)

	// --- Scenario 7: a published unit that goes empty has its PR closed --------
	id5 := sl.StackID(fmt.Sprintf("manual:e2e-empty-%d", time.Now().UnixNano()))
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id5, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))
	br5 := func(task string) string { return sl.OutputBranchName(id5, task) }
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id5) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})
	_, err = store.AddNode(ctx, e2eWS, id5, "E1", "", "")
	require.NoError(t, err)
	materialize(t, repoPath, id5, []string{"E1"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id5, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	require.Equal(t, "open", prsByHead(t, forge, owner, repo, id5)[br5("E1")].State)
	// Make E1 empty: reset its branch to the base with no commits ahead.
	gitT(t, repoPath, "checkout", "-B", br5("E1"), "main")
	gitT(t, repoPath, "checkout", "main")
	_, err = rec.Publish(ctx, e2eWS, id5, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	assert.Equal(t, "closed", prsByHead(t, forge, owner, repo, id5)[br5("E1")].State, "empty unit's PR is closed")

	// --- Scenario 8: squash-merged predecessor → slide guard fails closed ------
	id6 := sl.StackID(fmt.Sprintf("manual:e2e-squash-%d", time.Now().UnixNano()))
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id6, WorkspaceKey: e2eWS, RepoName: repo, RootBase: "main"}))
	br6 := func(task string) string { return sl.OutputBranchName(id6, task) }
	t.Cleanup(func() {
		for head, pr := range prsByHead(t, forge, owner, repo, id6) {
			if pr.State == "open" {
				_ = forge.ClosePR(ctx, owner, repo, pr.Number, "e2e cleanup")
			}
			_ = exec.Command("git", "-C", repoPath, "push", "origin", "--delete", head).Run()
		}
	})
	for _, e := range []struct{ id, base string }{{"S1", ""}, {"S2", "S1"}} {
		_, ae := store.AddNode(ctx, e2eWS, id6, e.id, e.base, "")
		require.NoError(t, ae)
	}
	materialize(t, repoPath, id6, []string{"S1", "S2"}, "main")
	_, err = rec.Publish(ctx, e2eWS, id6, repoPath, stackpublish.Options{})
	require.NoError(t, err)
	s1num := prsByHead(t, forge, owner, repo, id6)[br6("S1")].Number
	require.NotZero(t, s1num)
	sqout, sqerr := exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", s1num),
		"--repo", slug, "--squash", "--delete-branch=false").CombinedOutput()
	require.NoErrorf(t, sqerr, "gh pr merge --squash: %s", sqout)
	_, err = rec.Publish(ctx, e2eWS, id6, repoPath, stackpublish.Options{})
	require.Error(t, err, "publish must fail closed after a predecessor is squash-merged")
	assert.Contains(t, err.Error(), "rebased onto", "error gives actionable rebase guidance")
}
