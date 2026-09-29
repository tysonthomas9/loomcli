// Package pool admits source repositories and manages their linked task copies.
package pool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

var ErrCaptureRequired = errors.New("complete capture required before removing task copy")

const leaseTTL = 10 * time.Second

// CaptureComplete identifies a completed capture. The capture operation will
// issue this token; callers cannot remove a task copy with its zero value.
type CaptureComplete struct{ ref string }

func CompleteCapture(ref string) CaptureComplete { return CaptureComplete{ref: ref} }

type TaskCopyBackend interface {
	Create(context.Context, string) error
	Remove(context.Context, CaptureComplete) error
	Path() string
	Kind() string
}

type Pool struct {
	leases     loomgit.Store
	gitOptions gitexec.Options
}

func New(leases loomgit.Store, opts ...gitexec.Options) *Pool {
	p := &Pool{leases: leases}
	if len(opts) > 0 {
		p.gitOptions = opts[0]
	}
	return p
}

type LocalRepo struct {
	path   string
	common string
	runner *gitexec.Runner
	pool   *Pool
}

var _ loomgit.RepoStore = (*LocalRepo)(nil)

func (r *LocalRepo) Path() string { return r.path }

// WithLock serializes a revision's object and ref writes with worktree operations.
func (r *LocalRepo) WithLock(ctx context.Context, action func(context.Context) error) error {
	return r.locked(ctx, action)
}
func (r *LocalRepo) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runner.Run(ctx, args...)
}
func (r *LocalRepo) RunWithEnv(ctx context.Context, env map[string]string, args ...string) ([]byte, error) {
	return r.runner.RunWithEnv(ctx, env, args...)
}
func (r *LocalRepo) UpdateRef(ctx context.Context, ref, next, old string) error {
	return r.runner.UpdateRef(ctx, ref, next, old)
}

// Admit resolves the shared Git directory so separate worktrees of one source
// repository share a lease scope, including when opened by different processes.
func (p *Pool) Admit(ctx context.Context, path string) (*LocalRepo, error) {
	if p == nil || p.leases == nil {
		return nil, errors.New("pool store is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	runner, err := gitexec.New(abs, p.gitOptions)
	if err != nil {
		return nil, err
	}
	out, err := runner.Run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("admit repository: %w", err)
	}
	common, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, err
	}
	return &LocalRepo{path: abs, common: common, runner: runner, pool: p}, nil
}

func (r *LocalRepo) LinkedWorktree(path string) TaskCopyBackend {
	return &linkedWorktree{repo: r, path: path}
}

type linkedWorktree struct {
	repo *LocalRepo
	path string
}

func (w *linkedWorktree) Path() string { return w.path }
func (w *linkedWorktree) Kind() string { return "linked_worktree" }

func (w *linkedWorktree) Create(ctx context.Context, base string) error {
	if w.path == "" || base == "" {
		return errors.New("path and base are required")
	}
	abs, err := filepath.Abs(w.path)
	if err != nil {
		return err
	}
	return w.repo.locked(ctx, func(ctx context.Context) error {
		if _, err := os.Lstat(abs); err == nil {
			return fmt.Errorf("task copy path exists: %s", abs)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Reserve the path before invoking Git. A failed add may only clean up
		// this directory when this call created it.
		if err := os.Mkdir(abs, 0700); err != nil {
			return err
		}
		_, err := w.repo.runner.Run(ctx, "worktree", "add", "--detach", abs, base)
		if err != nil {
			// Git may leave a partial directory and registration on failure.
			_, _ = w.repo.runner.Run(context.WithoutCancel(ctx), "worktree", "remove", "--force", abs)
			listed, listErr := w.repo.runner.Run(context.WithoutCancel(ctx), "worktree", "list", "--porcelain")
			if listErr != nil {
				return errors.Join(err, listErr)
			}
			if strings.Contains("\n"+string(listed), "\nworktree "+abs+"\n") {
				return errors.Join(err, errors.New("failed add left a registered worktree"))
			}
			if _, statErr := os.Lstat(abs); statErr == nil {
				if cleanupErr := os.Remove(abs); cleanupErr != nil {
					return errors.Join(err, cleanupErr)
				}
			}
			return loomgit.NewError(loomgit.TaskCopyCreateFailed, "worktree add failed", err)
		}
		w.path = abs
		return nil
	})
}

func (w *linkedWorktree) Remove(ctx context.Context, complete CaptureComplete) error {
	if complete.ref == "" {
		return ErrCaptureRequired
	}
	if w.path == "" {
		return errors.New("task copy path is required")
	}
	abs, err := filepath.Abs(w.path)
	if err != nil {
		return err
	}
	return w.repo.locked(ctx, func(ctx context.Context) error {
		_, err := w.repo.runner.Run(ctx, "worktree", "remove", "--force", abs)
		return err
	})
}

func (r *LocalRepo) locked(ctx context.Context, action func(context.Context) error) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	owner := hex.EncodeToString(id[:])
	scope := "repo:" + r.common
	var lease loomgit.Lease
	for {
		var err error
		lease, err = r.pool.leases.ClaimLease(ctx, scope, owner, leaseTTL)
		if err == nil {
			break
		}
		if !errors.Is(err, loomgit.ErrLeaseHeld) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(leaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				var err error
				lease, err = r.pool.leases.RenewLease(runCtx, lease, leaseTTL)
				if err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	err := action(runCtx)
	cancel()
	renewErr := <-done
	releaseErr := r.pool.leases.ReleaseLease(context.WithoutCancel(ctx), lease)
	return errors.Join(err, renewErr, releaseErr)
}
