// Package agentworktree gives each agent a plain git worktree under a Loom-owned
// root. It wraps localworkspace's worktree code and adds the ownership checks
// that make Ensure safe to repeat: an existing folder is reused only when it
// is a worktree of the same repo on the expected branch (or, for detached
// reviewers, clean at the expected commit). Anything else is left untouched
// and refused.
package agentworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/gitrunner"
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
	git   gitrunner.Runner
	locks sync.Map // path -> *sync.Mutex
}

// New returns Worktrees rooted at root (for example ~/.loom/worktrees) for
// target, running git through git. Only TargetLocal is supported in Phase 1.
func New(root string, target Target, git gitrunner.Runner) (*Worktrees, error) {
	if target != TargetLocal {
		return nil, fmt.Errorf("agentworktree: unsupported target %q", target)
	}
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("agentworktree: root must be an absolute path, got %q", root)
	}
	if git == nil {
		return nil, errors.New("agentworktree: git runner is required")
	}
	return &Worktrees{root: filepath.Clean(root), git: git}, nil
}

// Path returns where the worktree for s lives: <root>/<repo name>/<key>.
func (w *Worktrees) Path(s Spec) (string, error) {
	key := strings.TrimSpace(s.Key)
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, `/\`) {
		return "", fmt.Errorf("agentworktree: invalid key %q", s.Key)
	}
	repo := filepath.Base(filepath.Clean(s.Repo))
	if strings.TrimSpace(s.Repo) == "" || repo == "." || repo == string(filepath.Separator) {
		return "", fmt.Errorf("agentworktree: invalid repo %q", s.Repo)
	}
	return filepath.Join(w.root, repo, key), nil
}

// Ensure makes the worktree for s, or reuses it when it already exists and
// belongs to s. A removed worktree is remade from its kept branch. It is
// idempotent by s.Key.
func (w *Worktrees) Ensure(ctx context.Context, s Spec) (Worktree, error) {
	if err := checkSpec(s); err != nil {
		return Worktree{}, err
	}
	if s.Detached && s.BaseRef == "" {
		return Worktree{}, errors.New("agentworktree: detached worktree needs BaseRef")
	}
	path, err := w.Path(s)
	if err != nil {
		return Worktree{}, err
	}
	defer w.lock(path)()

	if _, err := os.Lstat(path); err == nil {
		return w.checkOwned(ctx, s, path)
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("agentworktree: stat %s: %w", path, err)
	}

	// Drop registrations of worktree folders that were deleted, so a removed
	// worktree can be added again at the same path.
	if _, err := w.git.Run(ctx, s.Repo, "worktree", "prune"); err != nil {
		return Worktree{}, err
	}
	if s.Detached {
		err = localworkspace.EnsureDetachedGitWorktreeFromBranchWith(ctx, w.git.Run, s.Repo, path, "", s.BaseRef)
	} else {
		if s.BaseRef == "" {
			if _, verr := w.git.Run(ctx, s.Repo, "rev-parse", "--verify", "refs/heads/"+s.Branch); verr != nil {
				return Worktree{}, fmt.Errorf("agentworktree: new branch %q needs BaseRef", s.Branch)
			}
		}
		err = localworkspace.EnsureGitWorktreeFromBranchWith(ctx, w.git.Run, s.Repo, path, s.Branch, "", s.BaseRef)
	}
	if err != nil {
		return Worktree{}, fmt.Errorf("agentworktree: add worktree %s: %w", path, err)
	}
	return w.checkOwned(ctx, s, path)
}

// checkOwned verifies that path is a worktree of s.Repo on s.Branch, or, when
// detached, clean at s.BaseRef's commit.
func (w *Worktrees) checkOwned(ctx context.Context, s Spec, path string) (Worktree, error) {
	wt, err := w.owned(ctx, s, path)
	if err != nil || !s.Detached {
		return wt, err
	}
	sha, err := w.git.Run(ctx, s.Repo, "rev-parse", "--verify", s.BaseRef+"^{commit}")
	if err != nil {
		return Worktree{}, fmt.Errorf("agentworktree: resolve %q: %w", s.BaseRef, err)
	}
	if wt.HEAD != sha {
		return Worktree{}, notOwned(path, "at %s, want %s", wt.HEAD, sha)
	}
	if dirty, err := w.git.Run(ctx, path, "status", "--porcelain"); err != nil || dirty != "" {
		return Worktree{}, notOwned(path, "reviewer worktree has local changes")
	}
	return wt, nil
}

// owned verifies that path is a worktree root of s.Repo on s.Branch, or
// detached when s is. It does not look at the commit or local changes.
func (w *Worktrees) owned(ctx context.Context, s Spec, path string) (Worktree, error) {
	top, err := w.git.Run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(top, path) {
		return Worktree{}, notOwned(path, "not a git worktree root")
	}
	have, err := w.git.Run(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Worktree{}, notOwned(path, "read git dir: %v", err)
	}
	want, err := w.git.Run(ctx, s.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Worktree{}, fmt.Errorf("agentworktree: read repo git dir: %w", err)
	}
	if !samePath(have, want) {
		return Worktree{}, notOwned(path, "belongs to repo %s, not %s", have, want)
	}
	head, err := w.git.Run(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return Worktree{}, notOwned(path, "read HEAD: %v", err)
	}
	branch, _ := w.git.Run(ctx, path, "symbolic-ref", "--short", "-q", "HEAD")
	if s.Detached && branch != "" {
		return Worktree{}, notOwned(path, "on branch %q, want detached", branch)
	}
	if !s.Detached && branch != s.Branch {
		return Worktree{}, notOwned(path, "on branch %q, want %q", branch, s.Branch)
	}
	return Worktree{Path: path, Branch: branch, HEAD: head}, nil
}

func checkSpec(s Spec) error {
	if s.Detached == (s.Branch != "") {
		return errors.New("agentworktree: set exactly one of Branch or Detached")
	}
	return nil
}

func notOwned(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrNotOwned, path, fmt.Sprintf(format, args...))
}

// lock serializes work on one worktree path and returns the unlock.
func (w *Worktrees) lock(path string) func() {
	l, _ := w.locks.LoadOrStore(path, &sync.Mutex{})
	m := l.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
