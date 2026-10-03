// Package gitread serves immutable revision diffs from recorded Git refs.
package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// DefaultFileBudget is the maximum patch text returned for one file.
const DefaultFileBudget = 1 << 20

type File struct {
	Path      string   `json:"path"`
	PatchSize int      `json:"patchSize"`
	Truncated bool     `json:"truncated"`
	Patch     string   `json:"patch,omitempty"`
	Hunks     []string `json:"hunks,omitempty"`
}

type Diff struct {
	Revision                  int    `json:"revision"`
	Against                   int    `json:"against,omitempty"`
	Files                     []File `json:"files"`
	InterdiffAgainst          int    `json:"interdiffAgainst,omitempty"`
	DerivedFromChange         string `json:"derivedFromChange,omitempty"`
	DerivedFromNumber         int    `json:"derivedFromNumber,omitempty"`
	DerivedRangeDiffAvailable bool   `json:"derivedRangeDiffAvailable,omitempty"`
	DerivedRangeDiff          string `json:"derivedRangeDiff,omitempty"`
}

// Reader is the read-only revision view. OpenRepo must return the repository
// selected from WorkspaceRepos; callers cannot supply an arbitrary Git path.
type Reader struct {
	Revisions  loomgit.RevisionStore
	Workspaces interface {
		WorkspaceRepos(context.Context, string) ([]loomgit.WorkspaceRepo, error)
	}
	OpenRepo   func(string, string) (loomgit.RepoStore, error)
	Tasks      TaskStore
	FileBudget int
}

func (r *Reader) repo(ctx context.Context, workspace, repo string) (loomgit.RepoStore, error) {
	if repo == "" {
		return nil, loomgit.NewError(loomgit.RepoSelectionRequired, "repo is required", nil)
	}
	repos, err := r.Workspaces.WorkspaceRepos(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, item := range repos {
		if item.Repo == repo {
			return r.OpenRepo(workspace, repo)
		}
	}
	return nil, loomgit.NewError(loomgit.RepoSelectionRequired, "repo is not in the workspace", nil)
}

func (r *Reader) revision(ctx context.Context, workspace, change string, number int) (loomgit.Revision, error) {
	if number < 1 {
		return loomgit.Revision{}, loomgit.NewError(loomgit.LineageUnresolved, "invalid revision number", nil)
	}
	rev, err := r.Revisions.GetRevision(ctx, workspace, change, number)
	if err != nil {
		return rev, err
	}
	if !rev.Ready {
		return rev, loomgit.NewError(loomgit.LineageUnresolved, "revision is unfinished", nil)
	}
	return rev, nil
}

func refs(rev loomgit.Revision) (string, string, error) {
	n := strconv.Itoa(rev.Number)
	base, err := refname.RevisionBase(rev.Workspace, rev.Change, n)
	if err != nil {
		return "", "", err
	}
	head, err := refname.RevisionHead(rev.Workspace, rev.Change, n)
	return base, head, err
}

func resolve(ctx context.Context, repo loomgit.RepoStore, ref, expected string, code loomgit.Code) error {
	out, err := repo.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || strings.TrimSpace(string(out)) != expected {
		return loomgit.NewError(code, "recorded revision ref is unavailable or changed", err)
	}
	return nil
}

func (r *Reader) pair(ctx context.Context, repo loomgit.RepoStore, rev loomgit.Revision) (string, string, error) {
	base, head, err := refs(rev)
	if err != nil {
		return "", "", err
	}
	if err := resolve(ctx, repo, base, rev.BaseSHA, loomgit.BaseRefUnresolvable); err != nil {
		return "", "", err
	}
	if err := resolve(ctx, repo, head, rev.HeadSHA, loomgit.LineageUnresolved); err != nil {
		return "", "", err
	}
	return base, head, nil
}

func (r *Reader) files(ctx context.Context, repo loomgit.RepoStore, from, to string) ([]File, error) {
	out, err := repo.Run(ctx, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", from, to)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0)
	budget := r.FileBudget
	if budget <= 0 {
		budget = DefaultFileBudget
	}
	for _, path := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		if len(path) == 0 {
			continue
		}
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--patch", from, to, "--", string(path)}
		patch, err := repo.Run(ctx, args...)
		if errors.Is(err, gitexec.ErrTruncated) {
			size, sizeErr := blobSize(ctx, repo, from, to, string(path))
			if sizeErr != nil {
				return nil, fmt.Errorf("size %q: %w", path, sizeErr)
			}
			files = append(files, File{Path: string(path), PatchSize: size, Truncated: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("diff %q: %w", path, err)
		}
		file := File{Path: string(path), PatchSize: len(patch), Truncated: len(patch) > budget}
		if !file.Truncated {
			file.Patch = string(patch)
			file.Hunks = hunks(patch)
		}
		files = append(files, file)
	}
	return files, nil
}

func blobSize(ctx context.Context, repo loomgit.RepoStore, from, to, path string) (int, error) {
	var size int
	var found bool
	for _, ref := range []string{from, to} {
		out, err := repo.Run(ctx, "cat-file", "-s", ref+":"+path)
		if err != nil {
			continue // Added or deleted paths exist on only one side.
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			return 0, err
		}
		if n > size {
			size = n
		}
		found = true
	}
	if !found {
		return 0, fmt.Errorf("file is absent from both revisions")
	}
	return size, nil
}

func hunks(patch []byte) []string {
	var result []string
	for _, line := range strings.SplitAfter(string(patch), "\n") {
		if strings.HasPrefix(line, "@@ ") {
			result = append(result, line)
		} else if len(result) > 0 {
			result[len(result)-1] += line
		}
	}
	return result
}

// Diff returns the complete base-to-head patch for one immutable revision.
func (r *Reader) Diff(ctx context.Context, workspace, change, repoName string, number int) (Diff, error) {
	repo, err := r.repo(ctx, workspace, repoName)
	if err != nil {
		return Diff{}, err
	}
	rev, err := r.revision(ctx, workspace, change, number)
	if err != nil {
		return Diff{}, err
	}
	base, head, err := r.pair(ctx, repo, rev)
	if err != nil {
		return Diff{}, err
	}
	files, err := r.files(ctx, repo, base, head)
	if err != nil {
		return Diff{}, err
	}
	result := Diff{Revision: number, Files: files}
	if number > 1 {
		result.InterdiffAgainst = number - 1
	}
	if rev.Kind == "derived" {
		if rev.DerivedFromChange == "" || rev.DerivedFromNumber < 1 {
			return Diff{}, loomgit.NewError(loomgit.LineageUnresolved, "derived revision has no source", nil)
		}
		from, err := r.revision(ctx, workspace, rev.DerivedFromChange, rev.DerivedFromNumber)
		if err != nil {
			return Diff{}, err
		}
		fromBase, fromHead, err := r.pair(ctx, repo, from)
		if err != nil {
			return Diff{}, err
		}
		rangeDiff, err := repo.Run(ctx, "range-diff", "--no-color", fromBase+".."+fromHead, base+".."+head)
		if err != nil {
			return Diff{}, fmt.Errorf("range-diff: %w", err)
		}
		result.DerivedFromChange, result.DerivedFromNumber = from.Change, from.Number
		result.DerivedRangeDiffAvailable = true
		result.DerivedRangeDiff = string(rangeDiff)
	}
	return result, nil
}

// Interdiff compares the heads of two recorded revisions, including Snapshot commits.
func (r *Reader) Interdiff(ctx context.Context, workspace, change, repoName string, number, against int) (Diff, error) {
	if number == against {
		return Diff{}, loomgit.NewError(loomgit.LineageUnresolved, "choose two revisions", nil)
	}
	repo, err := r.repo(ctx, workspace, repoName)
	if err != nil {
		return Diff{}, err
	}
	rev, err := r.revision(ctx, workspace, change, number)
	if err != nil {
		return Diff{}, err
	}
	other, err := r.revision(ctx, workspace, change, against)
	if err != nil {
		return Diff{}, err
	}
	_, head, err := r.pair(ctx, repo, rev)
	if err != nil {
		return Diff{}, err
	}
	_, againstHead, err := r.pair(ctx, repo, other)
	if err != nil {
		return Diff{}, err
	}
	files, err := r.files(ctx, repo, againstHead, head)
	if err != nil {
		return Diff{}, err
	}
	return Diff{Revision: number, Against: against, Files: files}, nil
}

// OpenLocal binds a reader to serve's local revision journal and repo resolver.
// A missing journal is a v2 workspace error and never creates empty state.
func OpenLocal(repoPath func(string, string) string) (*Reader, func() error, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, loomgit.NewError(loomgit.WorkspaceUnsupported, "revision journal is unavailable", err)
		}
		return nil, nil, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, nil, err
	}
	r := &Reader{Revisions: store, Workspaces: store, Tasks: store, OpenRepo: func(workspace, repo string) (loomgit.RepoStore, error) {
		path := repoPath(workspace, repo)
		if path == "" {
			return nil, loomgit.NewError(loomgit.WorkspaceUnsupported, "local repo path is unavailable", nil)
		}
		return gitexec.New(path, gitexec.Options{ReadOnly: true, OutputCap: 64 << 20})
	}}
	return r, store.Close, nil
}

func IsNotFound(err error) bool { return errors.Is(err, journal.ErrNotFound) }
