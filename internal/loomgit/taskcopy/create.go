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

type Result struct {
	BaseSHA string
	Kind    string
	Reason  string
}

func CreateDetailed(ctx context.Context, source, target, workspace, attempt, previousAttempt, base string) (Result, error) {
	return CreateDetailedAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), source, target, workspace, attempt, previousAttempt, base)
}

func CreateDetailedAt(ctx context.Context, journalPath, source, target, workspace, attempt, previousAttempt, base string) (Result, error) {
	return createDetailedAt(ctx, journalPath, source, target, workspace, attempt, previousAttempt, base, false)
}

// ResumeDetailedAt starts a fresh copy at the prior capture while recording
// the original attempt base. The crash-dirty copy is never reused or cleaned.
func ResumeDetailedAt(ctx context.Context, journalPath, source, target, workspace, attempt, previousAttempt string) (Result, error) {
	if previousAttempt == "" {
		return Result{}, fmt.Errorf("previous attempt is required for resume")
	}
	return createDetailedAt(ctx, journalPath, source, target, workspace, attempt, previousAttempt, "", true)
}

func ResumeDetailed(ctx context.Context, source, target, workspace, attempt, previousAttempt string) (Result, error) {
	return ResumeDetailedAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		source, target, workspace, attempt, previousAttempt)
}

func createDetailedAt(ctx context.Context, journalPath, source, target, workspace, attempt, previousAttempt, base string, resume bool) (Result, error) {
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return Result{}, err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, source)
	if err != nil {
		return Result{}, err
	}
	ref, err := refname.AttemptBase(workspace, attempt)
	if err != nil {
		return Result{}, err
	}
	var sha, checkout string
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		var err error
		sha, checkout, err = resolveCheckout(ctx, repo, workspace, previousAttempt, base, resume)
		if err != nil {
			return err
		}
		return repo.UpdateRef(ctx, ref, sha, strings.Repeat("0", len(sha)))
	})
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return Result{}, loomgit.NewError(loomgit.TaskCopyCreateFailed, "create task copy parent", err)
	}
	copy := repo.TaskCopy(target)
	if err := copy.Create(ctx, checkout); err != nil {
		return Result{}, loomgit.NewError(loomgit.TaskCopyCreateFailed, "create task copy", err)
	}
	return Result{BaseSHA: sha, Kind: copy.Kind(), Reason: copy.Reason()}, nil
}

func resolveCheckout(ctx context.Context, repo *pool.LocalRepo, workspace, previousAttempt, base string, resume bool) (string, string, error) {
	selected := base
	if previousAttempt != "" {
		prior, err := refname.AttemptBase(workspace, previousAttempt)
		if err != nil {
			return "", "", err
		}
		selected = prior
	}
	resolved, err := repo.Run(ctx, "rev-parse", "--verify", selected+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("resolve task copy base: %w", err)
	}
	sha := strings.TrimSpace(string(resolved))
	if !resume {
		return sha, sha, nil
	}
	capture, err := refname.AttemptCapture(workspace, previousAttempt)
	if err != nil {
		return "", "", err
	}
	resolved, err = repo.Run(ctx, "rev-parse", "--verify", capture+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("resolve resume capture: %w", err)
	}
	checkout := strings.TrimSpace(string(resolved))
	if _, err := repo.Run(ctx, "merge-base", "--is-ancestor", sha, checkout); err != nil {
		return "", "", fmt.Errorf("resume capture is not based on original attempt: %w", err)
	}
	return sha, checkout, nil
}
