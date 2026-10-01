// Package loomgit gives each agent a plain git worktree under a Loom-owned
// root. It wraps localworkspace's worktree code and adds the ownership checks
// that make Ensure safe to repeat: an existing folder is reused only when it
// is a worktree of the same repo on the expected branch (or, for detached
// reviewers, clean at the expected commit). Anything else is left untouched
// and refused.
package loomgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// Target names the environment worktrees are made in. Phase 1 supports only
// TargetLocal; a remote provider would plug in behind the same checks.
type Target string

// TargetLocal makes worktrees on this machine.
const TargetLocal Target = "local"

// ErrNotOwned reports that the worktree path holds something Ensure must not
// reuse or replace. The folder is left as it was.
var ErrNotOwned = errors.New("worktree path is not owned by this spec")

// Spec describes the worktree an agent needs.
type Spec struct {
	Key      string // from the AgentID; one path segment
	Repo     string // the workspace's repo clone
	BaseRef  string // branch or SHA to start from; local or remote-tracking
	Branch   string // "loom/agent/<id>", or empty for detached
	Detached bool   // reviewers: detached at BaseRef (a head SHA)
}

// Worktree is an ensured worktree.
type Worktree struct {
	Path   string
	Branch string // empty when detached
	HEAD   string
}

// Worktrees makes and checks agent worktrees under Root.
type Worktrees struct {
	root  string
	locks sync.Map // path -> *sync.Mutex
}

// New returns Worktrees rooted at root (for example ~/.loom/worktrees) for
// target. Only TargetLocal is supported in Phase 1.
func New(root string, target Target) (*Worktrees, error) {
	if target != TargetLocal {
		return nil, fmt.Errorf("loomgit: unsupported target %q", target)
	}
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("loomgit: root must be an absolute path, got %q", root)
	}
	return &Worktrees{root: filepath.Clean(root)}, nil
}

// Path returns where the worktree for s lives: <root>/<repo name>/<key>.
func (w *Worktrees) Path(s Spec) (string, error) {
	key := strings.TrimSpace(s.Key)
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, `/\`) {
		return "", fmt.Errorf("loomgit: invalid key %q", s.Key)
	}
	repo := filepath.Base(filepath.Clean(s.Repo))
	if strings.TrimSpace(s.Repo) == "" || repo == "." || repo == string(filepath.Separator) {
		return "", fmt.Errorf("loomgit: invalid repo %q", s.Repo)
	}
	return filepath.Join(w.root, repo, key), nil
}

// Ensure makes the worktree for s, or reuses it when it already exists and
// belongs to s. A removed worktree is remade from its kept branch. It is
// idempotent by s.Key.
func (w *Worktrees) Ensure(ctx context.Context, s Spec) (Worktree, error) {
	if s.Detached == (s.Branch != "") {
		return Worktree{}, errors.New("loomgit: set exactly one of Branch or Detached")
	}
	if s.Detached && s.BaseRef == "" {
		return Worktree{}, errors.New("loomgit: detached worktree needs BaseRef")
	}
	path, err := w.Path(s)
	if err != nil {
		return Worktree{}, err
	}
	lockAny, _ := w.locks.LoadOrStore(path, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Lstat(path); err == nil {
		return w.checkOwned(ctx, s, path)
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("loomgit: stat %s: %w", path, err)
	}

	// Drop registrations of worktree folders that were deleted, so a removed
	// worktree can be added again at the same path.
	if _, err := git(ctx, s.Repo, "worktree", "prune"); err != nil {
		return Worktree{}, err
	}
	if s.Detached {
		err = localworkspace.EnsureDetachedGitWorktreeFromBranch(s.Repo, path, "", s.BaseRef)
	} else {
		if s.BaseRef == "" {
			if _, verr := git(ctx, s.Repo, "rev-parse", "--verify", "refs/heads/"+s.Branch); verr != nil {
				return Worktree{}, fmt.Errorf("loomgit: new branch %q needs BaseRef", s.Branch)
			}
		}
		err = localworkspace.EnsureGitWorktreeFromBranch(s.Repo, path, s.Branch, "", s.BaseRef)
	}
	if err != nil {
		return Worktree{}, fmt.Errorf("loomgit: add worktree %s: %w", path, err)
	}
	return w.checkOwned(ctx, s, path)
}

// checkOwned verifies that path is a worktree of s.Repo on s.Branch, or, when
// detached, clean at s.BaseRef's commit.
func (w *Worktrees) checkOwned(ctx context.Context, s Spec, path string) (Worktree, error) {
	refuse := func(format string, args ...any) (Worktree, error) {
		return Worktree{}, fmt.Errorf("%w: %s: %s", ErrNotOwned, path, fmt.Sprintf(format, args...))
	}
	top, err := git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(top, path) {
		return refuse("not a git worktree root")
	}
	have, err := git(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return refuse("read git dir: %v", err)
	}
	want, err := git(ctx, s.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Worktree{}, fmt.Errorf("loomgit: read repo git dir: %w", err)
	}
	if !samePath(have, want) {
		return refuse("belongs to repo %s, not %s", have, want)
	}
	head, err := git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return refuse("read HEAD: %v", err)
	}
	branch, _ := git(ctx, path, "symbolic-ref", "--short", "-q", "HEAD")
	if !s.Detached {
		if branch != s.Branch {
			return refuse("on branch %q, want %q", branch, s.Branch)
		}
		return Worktree{Path: path, Branch: branch, HEAD: head}, nil
	}
	if branch != "" {
		return refuse("on branch %q, want detached", branch)
	}
	sha, err := git(ctx, s.Repo, "rev-parse", "--verify", s.BaseRef+"^{commit}")
	if err != nil {
		return Worktree{}, fmt.Errorf("loomgit: resolve %q: %w", s.BaseRef, err)
	}
	if head != sha {
		return refuse("at %s, want %s", head, sha)
	}
	if dirty, err := git(ctx, path, "status", "--porcelain"); err != nil || dirty != "" {
		return refuse("reviewer worktree has local changes")
	}
	return Worktree{Path: path, HEAD: head}, nil
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git executable; args come from loomgit.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
