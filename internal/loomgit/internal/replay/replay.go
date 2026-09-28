// Package replay replays a revision onto an explicit commit without changing refs or checkouts.
package replay

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

// Result describes the derived commit chain. HeadSHA is never installed in a ref.
type Result struct {
	HeadSHA          string
	TreeSHA          string
	ConflictCommit   string
	ConflictingPaths []string
	DroppedCommits   []string
}

type Engine struct {
	repo       loomgit.RepoStore
	once       sync.Once
	versionErr error
}

func New(repo loomgit.RepoStore) *Engine { return &Engine{repo: repo} }

var versionPattern = regexp.MustCompile(`^git version (\d+)\.(\d+)(?:\.\d+)?(?:\s|$)`)
var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func supportedVersion(output string) bool {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 2 || major == 2 && minor >= 40
}

func (e *Engine) checkVersion(ctx context.Context) error {
	e.once.Do(func() {
		out, err := e.repo.Run(ctx, "version")
		if err != nil {
			e.versionErr = fmt.Errorf("git version check: %w", err)
			return
		}
		if !supportedVersion(string(out)) {
			e.versionErr = fmt.Errorf("trial merge requires Git 2.40 or newer (found %q)", strings.TrimSpace(string(out)))
		}
	})
	return e.versionErr
}

func git(ctx context.Context, repo loomgit.RepoStore, args ...string) (string, error) {
	out, err := repo.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// TrialMerge replays base..head onto target. Its only side effects are Git objects.
func (e *Engine) TrialMerge(ctx context.Context, base, head, target string) (Result, error) {
	if err := e.checkVersion(ctx); err != nil {
		return Result{}, err
	}
	for _, sha := range []string{base, head, target} {
		if !shaPattern.MatchString(sha) {
			return Result{}, fmt.Errorf("trial merge requires explicit commit SHAs")
		}
		if _, err := e.repo.Run(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
			return Result{}, fmt.Errorf("invalid commit %s: %w", sha, err)
		}
	}
	if _, err := e.repo.Run(ctx, "merge-base", "--is-ancestor", base, head); err != nil {
		return Result{}, fmt.Errorf("revision base is not an ancestor of its head: %w", err)
	}
	if target == base {
		tree, err := git(ctx, e.repo, "rev-parse", head+"^{tree}")
		return Result{HeadSHA: head, TreeSHA: tree}, err
	}
	commits, err := git(ctx, e.repo, "rev-list", "--reverse", "--first-parent", base+".."+head)
	if err != nil {
		return Result{}, err
	}
	result := Result{HeadSHA: target}
	for _, commit := range strings.Fields(commits) {
		parent, err := git(ctx, e.repo, "rev-parse", commit+"^1")
		if err != nil {
			return result, fmt.Errorf("revision commit %s has no first parent: %w", commit, err)
		}
		// Name-only NUL output begins with the tree OID, then conflicted paths.
		out, mergeErr := e.repo.Run(ctx, "merge-tree", "--write-tree", "--merge-base="+parent, "--name-only", "--no-messages", "-z", result.HeadSHA, commit)
		if mergeErr != nil {
			var commandErr *gitexec.CommandError
			var exitErr *exec.ExitError
			if !errors.As(mergeErr, &commandErr) || !errors.As(mergeErr, &exitErr) || exitErr.ExitCode() != 1 {
				return result, fmt.Errorf("replay commit %s: %w", commit, mergeErr)
			}
			result.ConflictCommit = commit
			result.ConflictingPaths = conflictPaths(commandErr.Stdout)
			if len(result.ConflictingPaths) == 0 {
				return result, fmt.Errorf("merge-tree conflicted without paths at %s: %w", commit, mergeErr)
			}
			return result, nil
		}
		tree := strings.SplitN(string(out), "\x00", 2)[0]
		if !shaPattern.MatchString(tree) {
			return result, fmt.Errorf("merge-tree returned invalid tree for %s", commit)
		}
		priorTree, err := git(ctx, e.repo, "rev-parse", result.HeadSHA+"^{tree}")
		if err != nil {
			return result, err
		}
		if tree == priorTree {
			result.DroppedCommits = append(result.DroppedCommits, commit)
			continue
		}
		metaOut, err := e.repo.Run(ctx, "show", "-s", "--format=%an%x00%ae%x00%aI%x00%B%x00", commit)
		if err != nil {
			return result, err
		}
		parts := strings.SplitN(string(metaOut), "\x00", 4)
		if len(parts) != 4 {
			return result, fmt.Errorf("invalid author metadata for %s", commit)
		}
		message := strings.TrimSuffix(parts[3], "\x00\n")
		commitOut, err := e.repo.RunWithEnv(ctx, map[string]string{"GIT_AUTHOR_NAME": parts[0], "GIT_AUTHOR_EMAIL": parts[1], "GIT_AUTHOR_DATE": parts[2]}, "commit-tree", tree, "-p", result.HeadSHA, "-m", message)
		if err != nil {
			return result, fmt.Errorf("commit replay %s: %w", commit, err)
		}
		result.HeadSHA = strings.TrimSpace(string(commitOut))
	}
	result.TreeSHA, err = git(ctx, e.repo, "rev-parse", result.HeadSHA+"^{tree}")
	return result, err
}

func conflictPaths(output string) []string {
	parts := strings.Split(output, "\x00")
	if len(parts) < 2 {
		return nil
	}
	var paths []string
	for _, p := range parts[1:] {
		if p == "" {
			break
		}
		paths = append(paths, p)
	}
	return paths
}
