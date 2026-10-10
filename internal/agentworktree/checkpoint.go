package agentworktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// checkpointCrash runs at each Checkpoint crash point; tests use it to crash there.
var checkpointCrash = func(string) {}

// Checkpoint saves the worktree s owns, tracked and untracked files as they
// are on disk, as a commit on HEAD at ref, unless ref exists already. It
// works in a private index, so the branch, index and log are left as they
// are. Paths outside a sparse checkout keep their HEAD content (add -A
// leaves them alone); nested repositories are left out and named in the
// commit message. Objects and the ref are fsynced.
func (w *Worktrees) Checkpoint(ctx context.Context, s Spec, ref string) error {
	if err := checkSpec(s); err != nil {
		return err
	}
	path, err := w.Path(s)
	if err != nil {
		return err
	}
	defer w.lock(path)()
	if _, err := w.git.Run(ctx, path, "rev-parse", "--verify", "--quiet", ref); err == nil {
		return nil
	}
	if _, err := w.owned(ctx, s, path); err != nil {
		return err
	}
	return w.capture(ctx, path, ref)
}

// capture is Checkpoint for the owned worktree at path.
func (w *Worktrees) capture(ctx context.Context, path, ref string) error {
	git, ok := w.git.(gitrunner.EnvRunner)
	if !ok {
		return errors.New("agentworktree: Checkpoint needs a git runner that sets environment variables")
	}
	idx, err := w.git.Run(ctx, path, "rev-parse", "--path-format=absolute", "--git-path", "loom-checkpoint.index")
	if err != nil {
		return err
	}
	_ = os.Remove(idx + ".lock") // left by a crash; the agent's lock and w's serialize captures
	defer func() { _ = os.Remove(idx) }()
	env := []string{"GIT_INDEX_FILE=" + idx, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fsync",
		"GIT_CONFIG_VALUE_0=committed", "GIT_AUTHOR_NAME=Loom", "GIT_AUTHOR_EMAIL=loom@localhost",
		"GIT_COMMITTER_NAME=Loom", "GIT_COMMITTER_EMAIL=loom@localhost"}
	if _, err := git.RunEnv(ctx, path, env, "read-tree", "HEAD"); err != nil {
		return err
	}
	nested, err := nestedRepos(ctx, git, path, env)
	if err != nil {
		return err
	}
	add, msg := []string{"add", "-A", "--", "."}, "loom checkpoint "+ref
	for _, p := range nested {
		add = append(add, ":(exclude,literal)"+p)
		msg += "\nskipped nested repository " + p
	}
	if _, err := git.RunEnv(ctx, path, env, add...); err != nil {
		return err
	}
	tree, err := git.RunEnv(ctx, path, env, "write-tree")
	if err != nil {
		return err
	}
	commit, err := git.RunEnv(ctx, path, env, "commit-tree", strings.TrimSpace(tree), "-p", "HEAD", "-m", msg)
	if err != nil {
		return err
	}
	checkpointCrash("update-ref")
	if _, err := git.RunEnv(ctx, path, env, "update-ref", ref, strings.TrimSpace(commit), ""); err != nil {
		return fmt.Errorf("agentworktree: checkpoint %s: %w", ref, err)
	}
	return nil
}

// nestedRepos lists the nested repositories in the worktree at path, as seen
// from the private index env names (HEAD's tree): each gitlink, which keeps
// HEAD's commit, and each other repository, which ls-files lists as a
// directory (path/), including one only the agent's own index has staged.
func nestedRepos(ctx context.Context, git gitrunner.EnvRunner, path string, env []string) ([]string, error) {
	var out []string
	staged, err := git.RunEnv(ctx, path, env, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	for _, e := range strings.Split(staged, "\x00") {
		if mode, p, ok := strings.Cut(e, "\t"); ok && strings.HasPrefix(mode, "160000 ") {
			out = append(out, p)
		}
	}
	others, err := git.RunEnv(ctx, path, env, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(others, "\x00") {
		if strings.HasSuffix(p, "/") {
			out = append(out, p)
		}
	}
	return out, nil
}

// checkpointPrefix is where every checkpoint ref lives.
const checkpointPrefix = "refs/loom/checkpoints/"

// changeStatus names git diff --name-status letters.
var changeStatus = map[string]string{"A": "added", "M": "modified", "D": "deleted", "T": "type_changed"}

// CheckpointDiff is the change in repo from checkpoint ref from to ref to:
// each path that differs (no renames) and the patch, with no external diff
// or textconv run. A ref that does not exist fails with
// loomagent.ErrNoCheckpoint.
func (w *Worktrees) CheckpointDiff(ctx context.Context, repo, from, to string) (loomagent.CheckpointDiff, error) {
	git, ok := w.git.(gitrunner.EnvRunner)
	if !ok {
		return loomagent.CheckpointDiff{}, errors.New("agentworktree: CheckpointDiff needs a git runner with raw output")
	}
	for _, ref := range []string{from, to} {
		if !strings.HasPrefix(ref, checkpointPrefix) {
			return loomagent.CheckpointDiff{}, fmt.Errorf("agentworktree: %s is not a checkpoint ref", ref)
		}
		if err := w.hasCheckpoint(ctx, repo, ref); err != nil {
			return loomagent.CheckpointDiff{}, err
		}
	}
	diff := func(opt ...string) (string, error) {
		args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames"}, opt...)
		return git.RunEnv(ctx, repo, nil, append(args, from, to, "--")...)
	}
	names, err := diff("--name-status", "-z")
	if err != nil {
		return loomagent.CheckpointDiff{}, w.diffFailed(ctx, repo, err, from, to)
	}
	var out loomagent.CheckpointDiff
	f := strings.Split(strings.TrimSuffix(names, "\x00"), "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		st := changeStatus[f[i]]
		if st == "" {
			st = f[i]
		}
		out.Files = append(out.Files, loomagent.ChangedFile{Path: f[i+1], Status: st})
	}
	if out.Patch, err = diff(); err != nil {
		return loomagent.CheckpointDiff{}, w.diffFailed(ctx, repo, err, from, to)
	}
	return out, nil
}

// hasCheckpoint is nil when ref names a commit in repo, ErrNoCheckpoint when
// it does not exist (rev-parse exit 1), else the git failure.
func (w *Worktrees) hasCheckpoint(ctx context.Context, repo, ref string) error {
	_, err := w.git.Run(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return fmt.Errorf("%w: %s", loomagent.ErrNoCheckpoint, ref)
	}
	return err
}

// diffFailed is a failed diff's error: ErrNoCheckpoint when a ref went away
// meanwhile (a purge), else err.
func (w *Worktrees) diffFailed(ctx context.Context, repo string, err error, refs ...string) error {
	for _, ref := range refs {
		if e := w.hasCheckpoint(ctx, repo, ref); errors.Is(e, loomagent.ErrNoCheckpoint) {
			return e
		}
	}
	return err
}

// DropCheckpoints deletes every ref under prefix, one agent's checkpoint
// refs, in repo. A repo that is gone, or has no .git of its own, holds none.
func (w *Worktrees) DropCheckpoints(ctx context.Context, repo, prefix string) error {
	agent := strings.TrimSuffix(strings.TrimPrefix(prefix, checkpointPrefix), "/")
	if !strings.HasPrefix(prefix, checkpointPrefix) || !strings.HasSuffix(prefix, "/") || agent == "" ||
		strings.ContainsAny(agent, "/*?[\\") {
		return fmt.Errorf("agentworktree: %q is not one agent's checkpoint refs", prefix)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, fs.ErrNotExist) {
		return nil // git would find an enclosing repository's refs
	}
	refs, err := w.git.Run(ctx, repo, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return err
	}
	for _, ref := range strings.Fields(refs) {
		if _, err := w.git.Run(ctx, repo, "update-ref", "-d", ref); err != nil {
			return err
		}
	}
	return nil
}
