package git

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/merkletrie"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

const (
	maxDiffPatchBytes = 500 * 1024 // 500KB cap on single-file patches
	maxDiffFiles      = 500        // cap on number of files returned
)

// ResolveMergeBase returns the merge-base commit hash between a likely base ref
// and HEAD. It uses go-git revision and merge-base semantics, with fallbacks for
// repos whose stored default branch is stale but whose remote HEAD is valid.
func ResolveMergeBase(worktreePath, branch string) (string, error) {
	if err := validateGitRef(branch); err != nil {
		return "", err
	}
	repo, err := openGoGitRepo(worktreePath)
	if err != nil {
		return "", fmt.Errorf("open repository: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", fmt.Errorf("load HEAD commit: %w", err)
	}

	candidates := diffBaseCandidates(repo, branch)
	tried := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		baseCommit, ok := resolveGoGitCommit(repo, candidate)
		if !ok {
			continue
		}
		tried = append(tried, candidate)
		bases, err := baseCommit.MergeBase(headCommit)
		if err != nil || len(bases) == 0 {
			continue
		}
		return bases[0].Hash.String(), nil
	}
	if len(tried) == 0 {
		return "", fmt.Errorf("%w: no candidate refs resolved for %q", ops.ErrDiffBaseNotFound, branch)
	}
	return "", fmt.Errorf("%w: no common ancestor for %q (tried: %s)", ops.ErrDiffBaseNotFound, branch, strings.Join(tried, ", "))
}

func openGoGitRepo(worktreePath string) (*gogit.Repository, error) {
	return gogit.PlainOpenWithOptions(worktreePath, &gogit.PlainOpenOptions{DetectDotGit: true})
}

func resolveGoGitCommit(repo *gogit.Repository, ref string) (*object.Commit, bool) {
	if ref == "" {
		return nil, false
	}
	if err := validateGitRef(ref); err != nil {
		return nil, false
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, false
	}
	commit, err := repo.CommitObject(*hash)
	if err == nil {
		return commit, true
	}
	tag, err := repo.TagObject(*hash)
	if err == nil {
		commit, err := tag.Commit()
		return commit, err == nil
	}
	return commit, err == nil
}

func diffBaseCandidates(repo *gogit.Repository, branch string) []string {
	candidates := make([]string, 0, 16)
	seen := make(map[string]struct{})
	add := func(ref string) {
		if ref == "" {
			return
		}
		if err := validateGitRef(ref); err != nil {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		candidates = append(candidates, ref)
	}

	remotes := repoRemotes(repo)
	add(branch)
	// The current branch's upstream is often origin/<agent-branch>, which would
	// hide already-pushed agent work from review diffs. Prefer integration refs.
	for _, remote := range remotes {
		if branch != "" {
			add(remote + "/" + branch)
		}
		add(remote + "/HEAD")
		add(remote + "/main")
		add(remote + "/master")
		add(remote + "/trunk")
	}
	add("main")
	add("master")
	add("trunk")

	return candidates
}

func repoRemotes(repo *gogit.Repository) []string {
	cfg, err := repo.Config()
	if err != nil || cfg == nil {
		return []string{"origin"}
	}

	remotes := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		if err := validateGitRef(name); err == nil {
			remotes = append(remotes, name)
		}
	}
	sort.Strings(remotes)
	if len(remotes) == 0 {
		remotes = []string{"origin"}
	}

	return remotes
}

// DiffCommits returns the list of commits between mergeBase and HEAD.
// Format: %H|%h|%an|%ae|%aI|%s — subject is last so pipes in it are preserved.
// ctx is currently unused at this layer (RunGitCommand has no ctx surface yet)
// but is accepted so the public API mirrors DiffFiles/DiffFilePatch.
func DiffCommits(ctx context.Context, worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	_ = ctx
	if err := validateGitRef(mergeBase); err != nil {
		return nil, err
	}
	args := []string{"log", mergeBase + "..HEAD", "--format=%H|%h|%an|%ae|%aI|%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", limit))
	}
	out, err := cli.RunGitCommand(worktreePath, args...)
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	results := make([]ops.DiffCommitResult, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		results = append(results, ops.DiffCommitResult{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Email:     parts[3],
			Date:      parts[4],
			Subject:   parts[5],
		})
	}
	return results, nil
}

// DiffFiles returns the list of changed files between two refs with status and stats.
// ctx is plumbed into go-git's DiffContext so a canceled request stops the
// in-process tree walk instead of running to natural completion.
func DiffFiles(ctx context.Context, worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	changes, err := diffChanges(ctx, worktreePath, from, to)
	if err != nil {
		return nil, err
	}
	results := make([]ops.DiffFileResult, 0, len(changes))
	for _, change := range changes {
		if len(results) >= maxDiffFiles {
			break
		}
		result, ok := diffFileResult(change)
		if !ok {
			continue
		}
		result.Additions, result.Deletions = diffFileStats(change)
		results = append(results, result)
	}
	return results, nil
}

func diffChanges(ctx context.Context, worktreePath, from, to string) (object.Changes, error) {
	if err := validateGitRef(from); err != nil {
		return nil, err
	}
	if err := validateGitRef(to); err != nil {
		return nil, err
	}
	repo, err := openGoGitRepo(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	fromCommit, ok := resolveGoGitCommit(repo, from)
	if !ok {
		return nil, fmt.Errorf("resolve from ref %q", from)
	}
	toCommit, ok := resolveGoGitCommit(repo, to)
	if !ok {
		return nil, fmt.Errorf("resolve to ref %q", to)
	}
	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("load from tree: %w", err)
	}
	toTree, err := toCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("load to tree: %w", err)
	}
	changes, err := fromTree.DiffContext(ctx, toTree)
	if err != nil {
		return nil, fmt.Errorf("diff trees: %w", err)
	}
	return changes, nil
}

func diffFileResult(change *object.Change) (ops.DiffFileResult, bool) {
	action, err := change.Action()
	if err != nil {
		return ops.DiffFileResult{}, false
	}
	switch action {
	case merkletrie.Insert:
		return ops.DiffFileResult{Status: "A", Path: change.To.Name}, true
	case merkletrie.Delete:
		return ops.DiffFileResult{Status: "D", Path: change.From.Name}, true
	case merkletrie.Modify:
		if change.From.Name != "" && change.To.Name != "" && change.From.Name != change.To.Name {
			return ops.DiffFileResult{Status: "R", OldPath: change.From.Name, Path: change.To.Name}, true
		}
		path := change.To.Name
		if path == "" {
			path = change.From.Name
		}
		return ops.DiffFileResult{Status: "M", Path: path}, path != ""
	default:
		return ops.DiffFileResult{}, false
	}
}

func diffFileStats(change *object.Change) (int, int) {
	if changeHasNonFileTreeEntry(change) {
		action, err := change.Action()
		if err != nil {
			return 0, 0
		}
		switch action {
		case merkletrie.Insert:
			return 1, 0
		case merkletrie.Delete:
			return 0, 1
		case merkletrie.Modify:
			return 1, 1
		}
	}
	patch, err := change.Patch()
	if err != nil {
		return 0, 0
	}
	var additions, deletions int
	for _, stat := range patch.Stats() {
		additions += stat.Addition
		deletions += stat.Deletion
	}
	return additions, deletions
}

func changeMatchesPath(change *object.Change, path string) bool {
	return change.From.Name == path || change.To.Name == path
}

// parseNumstatRenamePath extracts the new path from numstat rename output.
// Handles formats like: "old => new" and "{prefix/old => prefix/new}/suffix"
func parseNumstatRenamePath(s string) string {
	// Handle brace syntax: "{old => new}/rest" or "prefix/{old => new}/rest"
	braceStart := strings.Index(s, "{")
	braceEnd := strings.Index(s, "}")
	if braceStart >= 0 && braceEnd > braceStart {
		inner := s[braceStart+1 : braceEnd]
		prefix := s[:braceStart]
		suffix := s[braceEnd+1:]
		parts := strings.SplitN(inner, " => ", 2)
		if len(parts) == 2 {
			return prefix + parts[1] + suffix
		}
	}
	// Simple "old => new"
	parts := strings.SplitN(s, " => ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return s
}

// DiffFilePatch returns the unified diff patch for a single file between two refs.
// ctx is plumbed into the underlying go-git tree walk so a canceled request
// stops the walk.
func DiffFilePatch(ctx context.Context, worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
	if path == "" {
		return nil, fmt.Errorf("path must not be empty")
	}
	changes, err := diffChanges(ctx, worktreePath, from, to)
	if err != nil {
		return nil, err
	}
	result := &ops.DiffFilePatchResult{}
	for _, change := range changes {
		if !changeMatchesPath(change, path) {
			continue
		}
		result.Additions, result.Deletions = diffFileStats(change)
		if changeIsBinary(change) {
			result.IsBinary = true
			return result, nil
		}
		patch, err := change.Patch()
		if err != nil {
			return nil, fmt.Errorf("diff patch for file: %w", err)
		}
		var buf bytes.Buffer
		if err := patch.Encode(&buf); err != nil {
			return nil, fmt.Errorf("encode patch for file: %w", err)
		}
		if buf.Len() > maxDiffPatchBytes {
			result.IsTooLarge = true
			return result, nil
		}
		result.Patch = buf.String()
		return result, nil
	}
	return result, nil
}

func changeIsBinary(change *object.Change) bool {
	if changeHasNonFileTreeEntry(change) {
		return true
	}
	from, to, err := change.Files()
	if err != nil {
		return false
	}
	for _, file := range []*object.File{from, to} {
		if file == nil {
			continue
		}
		isBinary, err := file.IsBinary()
		if err == nil && isBinary {
			return true
		}
	}
	return false
}

func changeHasNonFileTreeEntry(change *object.Change) bool {
	for _, entry := range []object.ChangeEntry{change.From, change.To} {
		if entry.Name != "" && !entry.TreeEntry.Mode.IsFile() {
			return true
		}
	}
	return false
}
