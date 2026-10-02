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
	return inspect(runGit, sourceRepo, branch)
}

// GitFunc runs one git command in dir, for callers that inject their own runner.
type GitFunc func(ctx context.Context, dir string, args ...string) (string, error)

// InspectWith is Inspect with every git command run through git.
func InspectWith(ctx context.Context, git GitFunc, sourceRepo, branch string) (Recovery, error) {
	return inspect(bind(ctx, git), sourceRepo, branch)
}

// runFunc is the context-free git runner the helpers below share.
type runFunc func(dir string, args ...string) (string, error)

func bind(ctx context.Context, git GitFunc) runFunc {
	return func(dir string, args ...string) (string, error) { return git(ctx, dir, args...) }
}

func inspect(run runFunc, sourceRepo, branch string) (Recovery, error) {
	info := Recovery{Branch: branch, State: StateMissing}
	if _, err := run(sourceRepo, "rev-parse", "--git-dir"); err != nil {
		return info, fmt.Errorf("%w: %v", ErrRepositoryNotUsable, err)
	}
	sha, err := revisionCommit(run, sourceRepo, "refs/heads/"+branch)
	if err == nil {
		info.State = StateHealthy
		info.BaseSHA = sha
		return info, nil
	}
	broken, err := looseBranchRefExists(run, sourceRepo, branch)
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
	return recoverBranch(runGit, sourceRepo, branch, baseBranch, info)
}

// RecoverWith is Recover with every git command run through git.
func RecoverWith(ctx context.Context, git GitFunc, sourceRepo, branch, baseBranch string, info Recovery) (Recovery, error) {
	return recoverBranch(bind(ctx, git), sourceRepo, branch, baseBranch, info)
}

func recoverBranch(run runFunc, sourceRepo, branch, baseBranch string, info Recovery) (Recovery, error) {
	recovery, err := selectRecoveryBase(run, sourceRepo, branch, baseBranch, info)
	if err != nil {
		return recovery, err
	}
	if info.State == StateBroken {
		if err := clearBrokenBranch(run, sourceRepo, branch); err != nil {
			return recovery, err
		}
	}
	return recovery, nil
}

// ClearBrokenBranch removes a corrupt branch ref, falling back to renaming a
// loose ref aside if git update-ref cannot resolve it.
func ClearBrokenBranch(sourceRepo, branch string) error {
	return clearBrokenBranch(runGit, sourceRepo, branch)
}

func clearBrokenBranch(run runFunc, sourceRepo, branch string) error {
	ref := "refs/heads/" + branch
	if _, err := run(sourceRepo, "update-ref", "-d", ref); err == nil {
		return nil
	} else if renamed, renameErr := renameBrokenLooseRef(run, sourceRepo, branch); !renamed || renameErr != nil {
		return fmt.Errorf("clear corrupt branch ref %q: %w", branch, firstError(renameErr, err))
	}
	return nil
}

// CommonDir returns git's common metadata directory for sourceRepo.
func CommonDir(sourceRepo string) (string, error) {
	return commonDir(runGit, sourceRepo)
}

func commonDir(run runFunc, sourceRepo string) (string, error) {
	out, err := run(sourceRepo, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(sourceRepo, common)
	}
	return filepath.Abs(common)
}

func selectRecoveryBase(run runFunc, sourceRepo, branch, baseBranch string, info Recovery) (Recovery, error) {
	if sha, ok, err := recoverFromReflog(run, sourceRepo, branch); err != nil || ok {
		info.Base = "reflog"
		info.BaseSHA = sha
		return info, err
	}
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch != "" {
		if sha, err := revisionCommit(run, sourceRepo, "refs/heads/"+baseBranch); err == nil {
			info.Base = "default branch " + baseBranch
			info.BaseSHA = sha
			return info, nil
		}
	}
	if sha, err := revisionCommit(run, sourceRepo, "HEAD"); err == nil {
		info.Base = "HEAD"
		info.BaseSHA = sha
		return info, nil
	}
	return info, fmt.Errorf("recover branch %q: no reflog, default branch, or HEAD commit is usable", branch)
}

func revisionCommit(run runFunc, sourceRepo, revision string) (string, error) {
	out, err := run(sourceRepo, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func recoverFromReflog(run runFunc, sourceRepo, branch string) (string, bool, error) {
	logPath, err := commonPath(run, sourceRepo, "logs", "refs", "heads", filepath.FromSlash(branch))
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(logPath) //nolint:gosec // reflog path is resolved under git common dir.
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read branch reflog %s: %w", logPath, err)
	}
	sha := latestValidReflogSHA(run, sourceRepo, string(data))
	return sha, sha != "", nil
}

func latestValidReflogSHA(run runFunc, sourceRepo, data string) string {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) >= 2 && commitExists(run, sourceRepo, fields[1]) {
			return fields[1]
		}
	}
	return ""
}

func commitExists(run runFunc, sourceRepo, sha string) bool {
	if sha == "" {
		return false
	}
	_, err := run(sourceRepo, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

func renameBrokenLooseRef(run runFunc, sourceRepo, branch string) (bool, error) {
	refPath, err := commonPath(run, sourceRepo, "refs", "heads", filepath.FromSlash(branch))
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(refPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, os.Rename(refPath, uniqueBackupPath(refPath, "broken"))
}

func looseBranchRefExists(run runFunc, sourceRepo, branch string) (bool, error) {
	refPath, err := commonPath(run, sourceRepo, "refs", "heads", filepath.FromSlash(branch))
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

func commonPath(run runFunc, sourceRepo string, parts ...string) (string, error) {
	common, err := commonDir(run, sourceRepo)
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
	cmd := exec.Command("git", args...) //nolint:gosec // fixed git binary; args are controlled by branch recovery code.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
