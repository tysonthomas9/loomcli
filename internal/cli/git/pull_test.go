package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

func TestPullCmdArgsValidation(t *testing.T) {
	original := pullAll
	t.Cleanup(func() { pullAll = original })
	for _, test := range []struct {
		all   bool
		args  []string
		valid bool
	}{
		{false, nil, false},
		{false, []string{"lead"}, true},
		{false, []string{"lead", "main"}, true},
		{false, []string{"lead", "main", "extra"}, false},
		{true, nil, true},
		{true, []string{"main"}, true},
		{true, []string{"main", "extra"}, false},
	} {
		pullAll = test.all
		if got := pullCmd.Args(pullCmd, test.args) == nil; got != test.valid {
			t.Fatalf("all=%t args=%v valid=%t, want %t", test.all, test.args, got, test.valid)
		}
	}
}

func TestPullRoutesToLocalRestackWithoutLegacyGit(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	original := pullLocal
	t.Cleanup(func() { pullLocal = original })
	var calls []struct{ path, remote, branch string }
	pullLocal = func(_ context.Context, path, remote, branch, requestID string) (pull.PullResult, error) {
		if requestID == "" {
			t.Fatal("missing request ID")
		}
		calls = append(calls, struct{ path, remote, branch string }{path, remote, branch})
		return pull.PullResult{HeadSHA: "restacked"}, nil
	}
	worktrees := []WorktreeInfo{
		{Name: "api", Path: "/ws/api", Branch: "lead", Repo: &RepoConfig{Name: "api", DefaultBranch: "develop", Remote: "upstream"}},
		{Name: "web", Path: "/ws/web", Branch: "lead", Repo: &RepoConfig{Name: "web", DefaultBranch: "main"}},
		{Name: "missing", Path: "/ws/missing"},
	}
	pullWorkspaceWorktrees(deps, worktrees, "")
	if len(calls) != 2 || calls[0].path != "/ws/api" || calls[0].remote != "upstream" || calls[0].branch != "" ||
		calls[1].path != "/ws/web" || calls[1].branch != "" {
		t.Fatalf("pull routes = %+v", calls)
	}
	if err := pullRepoWorktree(deps, "/ws/api", "lead", "main", ""); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[2].branch != "main" || calls[2].remote != "" {
		t.Fatalf("explicit pull route = %+v", calls)
	}
}

func TestPullReportsHeldPaths(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	original := pullLocal
	t.Cleanup(func() { pullLocal = original })
	pullLocal = func(context.Context, string, string, string, string) (pull.PullResult, error) {
		return pull.PullResult{Paths: []string{"file.txt"}}, errors.New("swap_held")
	}
	err := pullRepoWorktree(deps, "/ws/api", "lead", "main", "")
	if err == nil || !strings.Contains(err.Error(), "file.txt") {
		t.Fatalf("held path missing from error: %v", err)
	}
}

func TestPullCmdRegistration(t *testing.T) {
	if pullCmd.GroupID != "git" || pullCmd.Flags().Lookup("all") == nil || pullCmd.Flags().Lookup("workspace") == nil {
		t.Fatal("pull command registration changed")
	}
}

func TestRestackCommandRoutesOrderToLocalWorkingArea(t *testing.T) {
	original := restackLocal
	t.Cleanup(func() { restackLocal = original })
	called := false
	restackLocal = func(_ context.Context, path, base string, order []string, requestID string) (pull.PullResult, error) {
		called = true
		if path != "/ws/lead" || base != "base-sha" || len(order) != 2 || order[0] != "C2" || order[1] != "C1" || requestID == "" {
			t.Fatalf("restack route = path %q base %q order %v request %q", path, base, order, requestID)
		}
		return pull.PullResult{HeadSHA: "rebuilt"}, nil
	}
	if err := restackRepoWorktree(context.Background(), "/ws/lead", "base-sha", []string{"C2", "C1"}); err != nil || !called {
		t.Fatalf("restack route: called=%t err=%v", called, err)
	}
	if restackCmd.Flags().Lookup("workspace") == nil || restackCmd.GroupID != "git" || restackCmd.Args(restackCmd, []string{"lead", "base-sha"}) != nil {
		t.Fatal("restack command registration changed")
	}
}

func stubPullLocal(t *testing.T) {
	t.Helper()
	original := pullLocal
	t.Cleanup(func() { pullLocal = original })
	pullLocal = func(context.Context, string, string, string, string) (pull.PullResult, error) {
		return pull.PullResult{HeadSHA: "restacked"}, nil
	}
}
