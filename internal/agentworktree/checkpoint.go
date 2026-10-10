package agentworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/gitrunner"
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
	commit, err := git.RunEnv(ctx, path, env, "commit-tree", tree, "-p", "HEAD", "-m", msg)
	if err != nil {
		return err
	}
	checkpointCrash("update-ref")
	if _, err := git.RunEnv(ctx, path, env, "update-ref", ref, commit, ""); err != nil {
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
