// Package taskcopy creates an attempt checkout and records its immutable base.
package taskcopy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

// Create records base before creating the copy. previousAttempt pins retries to
// the first attempt's base even if the selected branch advances meanwhile.
func Create(ctx context.Context, source, target, workspace, attempt, previousAttempt, base string) (string, error) {
	return CreateAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		source, target, workspace, attempt, previousAttempt, base)
}

// CreateAt permits an isolated pool journal in tests.
func CreateAt(ctx context.Context, journalPath, source, target, workspace, attempt, previousAttempt, base string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return "", err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, source)
	if err != nil {
		return "", err
	}
	ref, err := refname.AttemptBase(workspace, attempt)
	if err != nil {
		return "", err
	}
	var sha string
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		selected := base
		if previousAttempt != "" {
			prior, err := refname.AttemptBase(workspace, previousAttempt)
			if err != nil {
				return err
			}
			selected = prior
		}
		resolved, err := repo.Run(ctx, "rev-parse", "--verify", selected+"^{commit}")
		if err != nil {
			return fmt.Errorf("resolve task copy base: %w", err)
		}
		sha = strings.TrimSpace(string(resolved))
		return repo.UpdateRef(ctx, ref, sha, strings.Repeat("0", len(sha)))
	})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", loomgit.NewError(loomgit.TaskCopyCreateFailed, "create task copy parent", err)
	}
	if err := repo.LinkedWorktree(target).Create(ctx, sha); err != nil {
		return "", loomgit.NewError(loomgit.TaskCopyCreateFailed, "create task copy", err)
	}
	return sha, nil
}
