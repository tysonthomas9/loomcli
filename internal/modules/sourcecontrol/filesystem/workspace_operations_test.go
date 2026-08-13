package filesystem

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

type browseLayout struct {
	checkout sourcecontrol.AgentCheckout
	err      error
}

func (layout browseLayout) ResolveAgentCheckout(
	_ context.Context,
	workspaceKey string,
	agentID string,
) (sourcecontrol.AgentCheckout, error) {
	if layout.err != nil {
		return sourcecontrol.AgentCheckout{}, layout.err
	}
	if workspaceKey != "WS-1" || agentID != "planner" {
		return sourcecontrol.AgentCheckout{}, errors.New("unexpected layout coordinates")
	}
	return layout.checkout, nil
}
func (layout browseLayout) ListAgentCheckouts(context.Context, string) ([]sourcecontrol.AgentCheckout, error) {
	return nil, nil
}
func (layout browseLayout) ListRepositoryCheckouts(context.Context, string) ([]sourcecontrol.RepositoryCheckoutView, error) {
	return nil, nil
}
func (layout browseLayout) SetRepositoryDefaultBranch(context.Context, string, string, string) error {
	return nil
}

type browseGit struct {
	mergeBaseCalls int
	diffCalls      int
	diffFileCalls  int
	patchCalls     int
	diffStatCalls  int
}

// browseFiles satisfies the private file-mechanics constructor dependency.
// Diff tests never call these methods; keeping the adapter explicit proves the
// public Browse port is composed as one Source Control capability.
type browseFiles struct{}

func (browseFiles) ResolveAgentWorktree(string, string) (*sourcecontrol.Worktree, error) {
	return nil, errors.New("unused")
}
func (browseFiles) ResolveAgentWorktreeForRepo(string, string, string) (*sourcecontrol.Worktree, error) {
	return nil, errors.New("unused")
}
func (browseFiles) ResolveWorkspaceRoot(string) (string, error) {
	return "", errors.New("unused")
}
func (browseFiles) ResolveWorkspaceData(string) (*sourcecontrol.WorkspaceTopology, error) {
	return nil, errors.New("unused")
}
func (browseFiles) ResolveLoomDataDir() (string, error) { return "", errors.New("unused") }
func (browseFiles) GitStatusPorcelain(context.Context, string) (sourcecontrol.GitFileStatusResult, error) {
	return sourcecontrol.GitFileStatusResult{}, errors.New("unused")
}
func (browseFiles) GitShowFileAtRev(context.Context, string, string, string, int64) (*sourcecontrol.GitFileContentAtRev, error) {
	return nil, errors.New("unused")
}
func (browseFiles) GitDiffFile(context.Context, string, string, string, string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, errors.New("unused")
}
func (browseFiles) GitLogFile(context.Context, string, string, int) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, errors.New("unused")
}
func (browseFiles) GitBlamePorcelain(context.Context, string, string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, errors.New("unused")
}
func (browseFiles) GitCurrentBranch(context.Context, string) (string, error) {
	return "", errors.New("unused")
}
func (browseFiles) RepairCheckout(string, string, string, string, bool) (sourcecontrol.RepairResult, error) {
	return sourcecontrol.RepairResult{}, errors.New("unused")
}

type browseBranches struct{}

func (browseBranches) Push(context.Context, string, string, string, string) (*sourcecontrol.PushResult, error) {
	return nil, errors.New("unused")
}
func (browseBranches) Pull(context.Context, string, string, string, string) (*sourcecontrol.PullResult, error) {
	return nil, errors.New("unused")
}
func (browseBranches) CurrentBranch(context.Context, string) (string, error) {
	return "", errors.New("unused")
}
func (browseBranches) Reset(context.Context, string, string, string, bool, bool) (*sourcecontrol.ResetResult, error) {
	return nil, errors.New("unused")
}
func (browseBranches) Status(context.Context, string, string) (*sourcecontrol.AgentStatusResult, error) {
	return nil, errors.New("unused")
}

type browseForge struct{}

func (browseForge) Available(context.Context) error { return errors.New("unused") }
func (browseForge) CreatePullRequest(context.Context, string, string, string, string) (*sourcecontrol.PullRequestCreation, error) {
	return nil, errors.New("unused")
}
func (browseForge) ListPullRequests(context.Context, string, string, int) ([]sourcecontrol.PullRequest, error) {
	return nil, errors.New("unused")
}

func newBrowse(layout sourcecontrol.CheckoutLayout, git sourcecontrol.GitBrowseMechanics) (sourcecontrol.Browse, error) {
	ports, err := sourcecontrol.NewWorkspacePorts(sourcecontrol.NewAccessGrantIssuer(), layout, git, New(browseFiles{}), browseBranches{}, browseForge{})
	return ports.Browse, err
}

func (git *browseGit) DiffStat(
	_ context.Context,
	checkoutPath string,
	baseBranch string,
) (sourcecontrol.DiffStat, error) {
	git.diffStatCalls++
	if checkoutPath != "/workspace/worktrees/repo/planner" || baseBranch != "main" {
		return sourcecontrol.DiffStat{}, errors.New("unexpected diff-stat coordinates")
	}
	return sourcecontrol.DiffStat{FilesChanged: 4, LinesAdded: 20, LinesRemoved: 7}, nil
}

func (git *browseGit) ResolveMergeBase(
	_ context.Context,
	checkoutPath string,
	baseBranch string,
) (string, error) {
	git.mergeBaseCalls++
	if checkoutPath != "/workspace/worktrees/repo/planner" || baseBranch != "main" {
		return "", errors.New("unexpected merge-base coordinates")
	}
	return "abc123", nil
}

func (git *browseGit) DiffCommits(
	_ context.Context,
	checkoutPath string,
	from string,
	limit int,
) ([]sourcecontrol.DiffCommit, error) {
	git.diffCalls++
	if checkoutPath != "/workspace/worktrees/repo/planner" || from != "abc123" || limit != 25 {
		return nil, errors.New("unexpected diff coordinates")
	}
	return []sourcecontrol.DiffCommit{{
		Hash:      "0123456789abcdef",
		ShortHash: "0123456",
		Subject:   "add durable source control browse",
		Author:    "Loom",
		Email:     "loom@example.test",
		Date:      "2026-08-13T12:00:00Z",
	}}, nil
}

func (git *browseGit) DiffFiles(
	_ context.Context,
	checkoutPath string,
	from string,
	to string,
) ([]sourcecontrol.DiffFile, error) {
	git.diffFileCalls++
	if checkoutPath != "/workspace/worktrees/repo/planner" || from != "abc123" || to != "HEAD" {
		return nil, errors.New("unexpected file-diff coordinates")
	}
	return []sourcecontrol.DiffFile{{
		Path: "internal/modules/sourcecontrol/browse.go", Status: "M",
		Additions: 12, Deletions: 3,
	}}, nil
}

func (git *browseGit) DiffFilePatch(
	_ context.Context,
	checkoutPath string,
	from string,
	to string,
	path string,
) (*sourcecontrol.DiffFilePatch, error) {
	git.patchCalls++
	if checkoutPath != "/workspace/worktrees/repo/planner" ||
		from != "abc123" || to != "HEAD" || path != "browse.go" {
		return nil, errors.New("unexpected patch coordinates")
	}
	return &sourcecontrol.DiffFilePatch{Patch: "@@ -1 +1 @@", Additions: 1, Deletions: 1}, nil
}

func TestBrowseDiffCommitsResolvesOpaqueCheckoutAndDefaultBase(t *testing.T) {
	git := &browseGit{}
	browse, err := newBrowse(
		browseLayout{checkout: sourcecontrol.AgentCheckout{
			WorkspaceKey:  "WS-1",
			AgentID:       "planner",
			RepositoryRef: "repo",
			CheckoutPath:  "/workspace/worktrees/repo/planner",
			Branch:        "planner",
			DefaultBranch: "main",
		}},
		git,
	)
	if err != nil {
		t.Fatalf("NewBrowse() error = %v", err)
	}

	got, err := browse.DiffCommits(t.Context(), sourcecontrol.DiffCommitsQuery{
		WorkspaceKey: "WS-1",
		AgentID:      "planner",
		Limit:        25,
	})
	if err != nil {
		t.Fatalf("DiffCommits() error = %v", err)
	}
	want := []sourcecontrol.DiffCommit{{
		Hash:      "0123456789abcdef",
		ShortHash: "0123456",
		Subject:   "add durable source control browse",
		Author:    "Loom",
		Email:     "loom@example.test",
		Date:      "2026-08-13T12:00:00Z",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffCommits() = %#v, want %#v", got, want)
	}
	if git.mergeBaseCalls != 1 || git.diffCalls != 1 {
		t.Fatalf("git calls = merge-base %d, diff %d; want 1 each", git.mergeBaseCalls, git.diffCalls)
	}
}

func TestBrowseDiffCommitsRejectsInvalidExplicitRefBeforeGit(t *testing.T) {
	git := &browseGit{}
	browse, err := newBrowse(
		browseLayout{checkout: sourcecontrol.AgentCheckout{
			WorkspaceKey: "WS-1", AgentID: "planner",
			CheckoutPath: "/workspace/worktrees/repo/planner", DefaultBranch: "main",
		}},
		git,
	)
	if err != nil {
		t.Fatalf("NewBrowse() error = %v", err)
	}

	_, err = browse.DiffCommits(t.Context(), sourcecontrol.DiffCommitsQuery{
		WorkspaceKey: "WS-1",
		AgentID:      "planner",
		From:         "../../outside",
	})
	if !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("DiffCommits() error = %v, want ErrInvalid", err)
	}
	if git.mergeBaseCalls != 0 || git.diffCalls != 0 {
		t.Fatalf("invalid ref reached Git: merge-base %d, diff %d", git.mergeBaseCalls, git.diffCalls)
	}
}

func TestBrowseDiffFilesUsesSameOpaquePlacementAndBasePolicy(t *testing.T) {
	git := &browseGit{}
	browse, err := newBrowse(
		browseLayout{checkout: sourcecontrol.AgentCheckout{
			WorkspaceKey: "WS-1", AgentID: "planner", RepositoryRef: "repo",
			CheckoutPath: "/workspace/worktrees/repo/planner", DefaultBranch: "main",
		}},
		git,
	)
	if err != nil {
		t.Fatalf("NewBrowse() error = %v", err)
	}

	got, err := browse.DiffFiles(t.Context(), sourcecontrol.DiffFilesQuery{
		WorkspaceKey: "WS-1",
		AgentID:      "planner",
		To:           "HEAD",
	})
	if err != nil {
		t.Fatalf("DiffFiles() error = %v", err)
	}
	want := []sourcecontrol.DiffFile{{
		Path: "internal/modules/sourcecontrol/browse.go", Status: "M",
		Additions: 12, Deletions: 3,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffFiles() = %#v, want %#v", got, want)
	}
	if git.mergeBaseCalls != 1 || git.diffFileCalls != 1 {
		t.Fatalf("git calls = merge-base %d, file-diff %d; want 1 each", git.mergeBaseCalls, git.diffFileCalls)
	}
}

func TestBrowseDiffFilePatchRejectsTraversalBeforePrivateAdapter(t *testing.T) {
	git := &browseGit{}
	browse, err := newBrowse(
		browseLayout{checkout: sourcecontrol.AgentCheckout{
			WorkspaceKey: "WS-1", AgentID: "planner", RepositoryRef: "repo",
			CheckoutPath: "/workspace/worktrees/repo/planner", DefaultBranch: "main",
		}},
		git,
	)
	if err != nil {
		t.Fatalf("NewBrowse() error = %v", err)
	}

	_, err = browse.DiffFilePatch(t.Context(), sourcecontrol.DiffFilePatchQuery{
		WorkspaceKey: "WS-1",
		AgentID:      "planner",
		To:           "HEAD",
		Path:         "../outside",
	})
	if !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("DiffFilePatch() error = %v, want ErrInvalid", err)
	}
	if git.mergeBaseCalls != 0 || git.patchCalls != 0 {
		t.Fatalf("traversal reached Git: merge-base %d, patch %d", git.mergeBaseCalls, git.patchCalls)
	}
}

func TestBrowseDiffStatKeepsCheckoutPathPrivate(t *testing.T) {
	git := &browseGit{}
	browse, err := newBrowse(
		browseLayout{checkout: sourcecontrol.AgentCheckout{
			WorkspaceKey: "WS-1", AgentID: "planner", RepositoryRef: "repo",
			CheckoutPath: "/workspace/worktrees/repo/planner", Branch: "planner",
			DefaultBranch: "main",
		}},
		git,
	)
	if err != nil {
		t.Fatalf("NewBrowse() error = %v", err)
	}

	got, err := browse.DiffStat(t.Context(), sourcecontrol.AgentQuery{
		WorkspaceKey: "WS-1",
		AgentID:      "planner",
	})
	if err != nil {
		t.Fatalf("DiffStat() error = %v", err)
	}
	want := sourcecontrol.AgentDiffStat{
		Branch: "planner", FilesChanged: 4, LinesAdded: 20, LinesRemoved: 7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffStat() = %#v, want %#v", got, want)
	}
	if git.diffStatCalls != 1 {
		t.Fatalf("diff-stat calls = %d, want 1", git.diffStatCalls)
	}
}
