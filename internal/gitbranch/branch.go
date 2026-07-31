// Package gitbranch contains shared branch-ref inspection and recovery helpers.
package gitbranch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrRepositoryNotUsable indicates the source repository cannot answer basic git queries.
var ErrRepositoryNotUsable = errors.New("source repo is not usable")

// RefState is the observed state of a local branch ref.
type RefState string

const (
	StateHealthy RefState = "healthy"
	StateMissing RefState = "missing"
	StateBroken  RefState = "broken"
)

// Recovery describes the chosen recovery base for a missing or broken branch.
type Recovery struct {
	Branch  string
	State   RefState
	Base    string
	BaseSHA string
}

// Inspect reports whether refs/heads/<branch> resolves to a commit, is missing,
// or has a broken loose ref that blocks git from creating the branch.
func Inspect(sourceRepo, branch string) (Recovery, error) {
	return InspectContext(context.Background(), sourceRepo, branch)
}

// InspectContext is Inspect with cancellation propagated to every Git query.
func InspectContext(
	ctx context.Context,
	sourceRepo,
	branch string,
) (Recovery, error) {
	info := Recovery{Branch: branch, State: StateMissing}
	if _, err := runGitContext(ctx, sourceRepo, "rev-parse", "--git-dir"); err != nil {
		return info, fmt.Errorf("%w: %v", ErrRepositoryNotUsable, err)
	}
	sha, err := revisionCommit(ctx, sourceRepo, "refs/heads/"+branch)
	if err == nil {
		info.State = StateHealthy
		info.BaseSHA = sha
		return info, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return info, cause
	}
	broken, err := looseBranchRefExists(ctx, sourceRepo, branch)
	if err != nil {
		return info, err
	}
	if broken {
		info.State = StateBroken
	}
	return info, nil
}

// Recover selects the best available recovery base and clears a broken branch
// ref so callers can recreate it with git worktree add -b.
func Recover(sourceRepo, branch, baseBranch string, info Recovery) (Recovery, error) {
	return RecoverContext(
		context.Background(),
		sourceRepo,
		branch,
		baseBranch,
		info,
	)
}

// RecoverContext is Recover with cancellation checked before every Git or
// filesystem mutation.
func RecoverContext(
	ctx context.Context,
	sourceRepo,
	branch,
	baseBranch string,
	info Recovery,
) (Recovery, error) {
	recovery, err := selectRecoveryBase(ctx, sourceRepo, branch, baseBranch, info)
	if err != nil {
		return recovery, err
	}
	if info.State == StateBroken {
		if err := ClearBrokenBranchContext(ctx, sourceRepo, branch); err != nil {
			return recovery, err
		}
	}
	return recovery, nil
}

// ClearBrokenBranch removes a corrupt branch ref, falling back to renaming a
// loose ref aside if git update-ref cannot resolve it.
func ClearBrokenBranch(sourceRepo, branch string) error {
	return ClearBrokenBranchContext(context.Background(), sourceRepo, branch)
}

// ClearBrokenBranchContext is ClearBrokenBranch with cancellation propagated
// through Git and checked immediately before the fallback rename.
func ClearBrokenBranchContext(
	ctx context.Context,
	sourceRepo,
	branch string,
) error {
	ref := "refs/heads/" + branch
	if _, err := runGitContext(ctx, sourceRepo, "update-ref", "-d", ref); err == nil {
		return nil
	} else if cause := context.Cause(ctx); cause != nil {
		return cause
	} else if renamed, renameErr := renameBrokenLooseRef(
		ctx,
		sourceRepo,
		branch,
	); !renamed || renameErr != nil {
		return fmt.Errorf("clear corrupt branch ref %q: %w", branch, firstError(renameErr, err))
	}
	return nil
}

// CommonDir returns git's common metadata directory for sourceRepo.
func CommonDir(sourceRepo string) (string, error) {
	return CommonDirContext(context.Background(), sourceRepo)
}

func CommonDirContext(ctx context.Context, sourceRepo string) (string, error) {
	out, err := runGitContext(ctx, sourceRepo, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(sourceRepo, common)
	}
	return filepath.Abs(common)
}

func selectRecoveryBase(
	ctx context.Context,
	sourceRepo,
	branch,
	baseBranch string,
	info Recovery,
) (Recovery, error) {
	if sha, ok, err := recoverFromReflog(ctx, sourceRepo, branch); err != nil || ok {
		info.Base = "reflog"
		info.BaseSHA = sha
		return info, err
	}
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch != "" {
		if sha, err := revisionCommit(ctx, sourceRepo, "refs/heads/"+baseBranch); err == nil {
			info.Base = "default branch " + baseBranch
			info.BaseSHA = sha
			return info, nil
		}
		if cause := context.Cause(ctx); cause != nil {
			return info, cause
		}
	}
	if sha, err := revisionCommit(ctx, sourceRepo, "HEAD"); err == nil {
		info.Base = "HEAD"
		info.BaseSHA = sha
		return info, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return info, cause
	}
	return info, fmt.Errorf("recover branch %q: no reflog, default branch, or HEAD commit is usable", branch)
}

func revisionCommit(ctx context.Context, sourceRepo, revision string) (string, error) {
	out, err := runGitContext(
		ctx,
		sourceRepo,
		"rev-parse",
		"--verify",
		"--quiet",
		revision+"^{commit}",
	)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func recoverFromReflog(
	ctx context.Context,
	sourceRepo,
	branch string,
) (string, bool, error) {
	logPath, err := commonPath(
		ctx,
		sourceRepo,
		"logs",
		"refs",
		"heads",
		filepath.FromSlash(branch),
	)
	if err != nil {
		return "", false, err
	}
	if err := context.Cause(ctx); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(logPath) //nolint:gosec // reflog path is resolved under git common dir.
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read branch reflog %s: %w", logPath, err)
	}
	if err := context.Cause(ctx); err != nil {
		return "", false, err
	}
	return latestValidReflogSHA(ctx, sourceRepo, string(data))
}

func latestValidReflogSHA(
	ctx context.Context,
	sourceRepo,
	data string,
) (string, bool, error) {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if err := context.Cause(ctx); err != nil {
			return "", false, err
		}
		fields := strings.Fields(lines[i])
		if len(fields) >= 2 {
			exists, err := commitExists(ctx, sourceRepo, fields[1])
			if err != nil {
				return "", false, err
			}
			if exists {
				return fields[1], true, nil
			}
		}
	}
	return "", false, nil
}

func commitExists(
	ctx context.Context,
	sourceRepo,
	sha string,
) (bool, error) {
	if sha == "" {
		return false, nil
	}
	_, err := runGitContext(ctx, sourceRepo, "cat-file", "-e", sha+"^{commit}")
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return false, cause
		}
		return false, nil
	}
	return true, nil
}

func renameBrokenLooseRef(
	ctx context.Context,
	sourceRepo,
	branch string,
) (bool, error) {
	refPath, err := commonPath(ctx, sourceRepo, "refs", "heads", filepath.FromSlash(branch))
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(refPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	return true, os.Rename(refPath, uniqueBackupPath(refPath, "broken"))
}

func looseBranchRefExists(
	ctx context.Context,
	sourceRepo,
	branch string,
) (bool, error) {
	refPath, err := commonPath(ctx, sourceRepo, "refs", "heads", filepath.FromSlash(branch))
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(refPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func commonPath(
	ctx context.Context,
	sourceRepo string,
	parts ...string,
) (string, error) {
	common, err := CommonDirContext(ctx, sourceRepo)
	if err != nil {
		return "", err
	}
	pathParts := append([]string{common}, parts...)
	path, err := filepath.Abs(filepath.Join(pathParts...))
	if err != nil {
		return "", err
	}
	if path != common && !pathContains(common, path) {
		return "", fmt.Errorf("git common path escapes repository metadata")
	}
	return path, nil
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func uniqueBackupPath(path, tag string) string {
	base := fmt.Sprintf("%s.%s-%d", path, tag, time.Now().Unix())
	candidate := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func pathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func runGit(dir string, args ...string) (string, error) {
	return runGitContext(context.Background(), dir, args...)
}

func runGitContext(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git binary; args are controlled by branch recovery code.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return string(out), cause
		}
		return string(out), fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
