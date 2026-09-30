package pool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

var errCoWUnsupported = errors.New("filesystem copy-on-write clone unsupported")

func unsupportedClone(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EINVAL)
}

type taskClone struct {
	repo               *LocalRepo
	path, kind, reason string
	cloneGit           func(string, string) error
}

func (c *taskClone) Path() string   { return c.path }
func (c *taskClone) Kind() string   { return c.kind }
func (c *taskClone) Reason() string { return c.reason }

func (r *LocalRepo) TaskCopy(path string) TaskCopyBackend {
	return &taskClone{repo: r, path: path}
}

func (c *taskClone) Create(ctx context.Context, base string) error {
	if c.path == "" || base == "" {
		return errors.New("path and base are required")
	}
	abs, err := filepath.Abs(c.path)
	if err != nil {
		return err
	}
	worktreeFallback := false
	owned := false
	err = c.repo.locked(ctx, func(ctx context.Context) error {
		var err error
		worktreeFallback, err = c.createLocked(ctx, abs, base, &owned)
		return err
	})
	if err == nil && worktreeFallback {
		err = c.repo.LinkedWorktree(abs).Create(ctx, base)
		if err == nil {
			c.kind = "worktree"
		}
	}
	if err != nil && owned {
		_ = os.RemoveAll(abs)
	}
	return err
}

func (c *taskClone) createLocked(ctx context.Context, abs, base string, owned *bool) (bool, error) {
	if _, err := os.Lstat(abs); err == nil {
		return false, fmt.Errorf("task copy path exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.Mkdir(abs, 0700); err != nil {
		return false, err
	}
	*owned, c.path = true, abs
	clone := c.cloneGit
	if clone == nil {
		clone = copyGitCoW
	}
	if err := clone(filepath.Join(c.repo.path, ".git"), filepath.Join(abs, ".git")); err == nil {
		c.kind = "cow"
		return false, c.checkout(ctx, base)
	} else if !errors.Is(err, errCoWUnsupported) {
		return false, loomgit.NewError(loomgit.TaskCopyCreateFailed, "clone git directory", err)
	} else {
		c.reason = err.Error()
	}
	if err := os.RemoveAll(filepath.Join(abs, ".git")); err != nil {
		return false, err
	}
	bare, err := c.repo.runner.Run(ctx, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, err
	}
	if string(bare) != "true\n" {
		if err := os.Remove(abs); err != nil {
			return false, err
		}
		*owned = false
		return true, nil
	}
	if _, err := c.repo.runner.Run(ctx, "clone", "--shared", "--no-checkout", "--local", c.repo.path, abs); err != nil {
		return false, loomgit.NewError(loomgit.TaskCopyCreateFailed, "shared clone", err)
	}
	c.kind = "shared"
	return false, c.checkout(ctx, base)
}

func (c *taskClone) checkout(ctx context.Context, base string) error {
	runner, err := gitexec.New(c.path, c.repo.pool.gitOptions)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "config", "--local", "remote.origin.url", c.repo.path); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "config", "--local", "remote.origin.pushurl", "loom-no-push://task-copy"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "checkout", "--force", "--detach", base); err != nil {
		return loomgit.NewError(loomgit.TaskCopyCreateFailed, "checkout task base", err)
	}
	return nil
}

func copyGitCoW(source, dest string) error {
	info, err := os.Lstat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errCoWUnsupported
		}
		return err
	}
	if !info.IsDir() {
		return errCoWUnsupported
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dest, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.Mkdir(to, info.Mode().Perm())
		case entry.Type().IsRegular():
			err := cloneFile(path, to)
			if unsupportedClone(err) {
				return fmt.Errorf("%w: %v", errCoWUnsupported, err)
			}
			return err
		case entry.Type()&os.ModeSymlink != 0:
			return errCoWUnsupported
		default:
			return fmt.Errorf("unsupported git entry %s", path)
		}
	})
}

func (c *taskClone) Remove(ctx context.Context, complete CaptureComplete) error {
	if complete.ref == "" {
		return ErrCaptureRequired
	}
	if c.kind == "" {
		return errors.New("task copy kind is required before removal")
	}
	if c.kind == "worktree" {
		return c.repo.LinkedWorktree(c.path).Remove(ctx, complete)
	}
	return c.repo.locked(ctx, func(context.Context) error { return os.RemoveAll(c.path) })
}
