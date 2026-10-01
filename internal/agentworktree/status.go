package agentworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrDirty reports that Remove found uncommitted or untracked changes. The
// worktree is left as it was.
var ErrDirty = errors.New("worktree has uncommitted changes")

// Status is a worktree's uncommitted state and its branch and head.
type Status struct {
	Uncommitted []string // paths with uncommitted or untracked changes
	Fingerprint string   // sha256 over the porcelain status and each listed file's content
	Branch      string   // empty when detached
	HEAD        string
}

// Status reports the uncommitted paths of the worktree s owns, with a
// fingerprint that changes whenever a path or its content changes. A missing
// worktree reports a zero Status (nothing uncommitted); a path that exists but
// is not owned by s fails with ErrNotOwned.
func (w *Worktrees) Status(ctx context.Context, s Spec) (Status, error) {
	if err := checkSpec(s); err != nil {
		return Status{}, err
	}
	path, err := w.Path(s)
	if err != nil {
		return Status{}, err
	}
	defer w.lock(path)()
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return Status{}, nil // absent: nothing uncommitted
	} else if err != nil {
		return Status{}, fmt.Errorf("agentworktree: stat %s: %w", path, err)
	}
	wt, err := w.owned(ctx, s, path)
	if err != nil {
		return Status{}, err
	}
	return w.status(ctx, path, wt)
}

// status reads the uncommitted paths and fingerprint of the owned worktree wt.
func (w *Worktrees) status(ctx context.Context, path string, wt Worktree) (Status, error) {
	// v2 entries start with a letter or digit, so the runner's trim keeps them whole.
	out, err := w.git.Run(ctx, path, "status", "--porcelain=v2", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(out))
	st := Status{Branch: wt.Branch, HEAD: wt.HEAD}
	for _, entry := range strings.Split(out, "\x00") {
		name := statusPath(entry)
		if name == "" {
			continue
		}
		st.Uncommitted = append(st.Uncommitted, name)
		if err := hashFile(h, filepath.Join(path, name)); err != nil {
			return Status{}, err
		}
	}
	st.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return st, nil
}

// statusPath returns the path of one porcelain v2 entry: "1 XY sub mH mI mW
// hH hI <path>", "u XY sub m1 m2 m3 mW h1 h2 h3 <path>" or "? <path>".
func statusPath(entry string) string {
	var n int
	switch {
	case strings.HasPrefix(entry, "1 "):
		n = 9
	case strings.HasPrefix(entry, "u "):
		n = 11
	case strings.HasPrefix(entry, "? "):
		n = 2
	default:
		return ""
	}
	parts := strings.SplitN(entry, " ", n)
	if len(parts) < n {
		return ""
	}
	return parts[n-1]
}

// hashFile adds a separator and the content of a regular file, or the target
// of a symlink, to h. Missing paths (deletions) add only the separator.
func hashFile(h io.Writer, path string) error {
	_, _ = h.Write([]byte{0})
	fi, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return fmt.Errorf("agentworktree: stat %s: %w", path, err)
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("agentworktree: read link %s: %w", path, err)
		}
		_, _ = h.Write([]byte(target))
		return nil
	case !fi.Mode().IsRegular():
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is inside the owned worktree.
	if err != nil {
		return fmt.Errorf("agentworktree: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("agentworktree: read %s: %w", path, err)
	}
	return nil
}

// Remove deletes the worktree s owns and keeps its branch. It refuses a
// worktree that is not owned by s (ErrNotOwned), or one with uncommitted or
// untracked changes (ErrDirty) unless s.Confirm equals its current Status
// fingerprint: the user confirmed deleting exactly that work. A missing worktree is already removed.
// Deciding which worktrees are due for removal is the caller's job.
func (w *Worktrees) Remove(ctx context.Context, s Spec) error {
	if err := checkSpec(s); err != nil {
		return err
	}
	path, err := w.Path(s)
	if err != nil {
		return err
	}
	defer w.lock(path)()
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("agentworktree: stat %s: %w", path, err)
	}
	wt, err := w.owned(ctx, s, path)
	if err != nil {
		return err
	}
	st, err := w.status(ctx, path, wt)
	if err != nil {
		return err
	}
	args := []string{"worktree", "remove", path}
	if len(st.Uncommitted) > 0 {
		if s.Confirm == "" || s.Confirm != st.Fingerprint {
			return fmt.Errorf("%w: %s", ErrDirty, path)
		}
		args = []string{"worktree", "remove", "--force", path}
	}
	if _, err := w.git.Run(ctx, s.Repo, args...); err != nil {
		return fmt.Errorf("agentworktree: remove worktree %s: %w", path, err)
	}
	return nil
}
