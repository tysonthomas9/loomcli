// Package driverfreeze turns a runner's flat patch into an immutable source revision.
package driverfreeze

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
)

type Request struct {
	Workspace, Task, Repo, Attempt string
	Worktree, Base                 string
	SourceRepo                     string
	CommitHeadSHA                  string
	Patch                          []byte
	Outcome                        string
	AuthorKind, AuthorID           string
}

type CaptureRequest struct {
	Workspace, Task, Repo, Attempt string
	Worktree, Base, CaptureSHA     string
	SourceRepo                     string
	RequestID                      string
	Outcome                        string
	Complete                       bool
}

// FreezeCapture records a host capture after cancellation, including a
// partial capture. The caller retains the task copy for recovery.
func FreezeCapture(ctx context.Context, in CaptureRequest) (loomgit.Revision, error) {
	return FreezeCaptureAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), in)
}

func FreezeCaptureAt(ctx context.Context, journalPath string, in CaptureRequest) (loomgit.Revision, error) {
	if in.Workspace == "" || in.Task == "" || in.Repo == "" || in.Attempt == "" || in.Worktree == "" || in.Base == "" || (in.Outcome != "cancelled" && in.Outcome != "failed" && in.Outcome != "completed" && in.Outcome != "abandoned") {
		return loomgit.Revision{}, fmt.Errorf("capture requires workspace, task, repo, attempt, worktree, base and a terminal outcome")
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return loomgit.Revision{}, err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return loomgit.Revision{}, err
	}
	defer func() { _ = store.Close() }()
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, in.Worktree)
	if err != nil {
		return loomgit.Revision{}, err
	}
	runner, err := gitexec.New(in.Worktree, options)
	if err != nil {
		return loomgit.Revision{}, err
	}
	var revision loomgit.Revision
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		captureSHA := in.CaptureSHA
		if captureSHA == "" {
			head, err := runner.Run(ctx, "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			captureSHA = strings.TrimSpace(string(head))
		}
		change, err := changeForTask(ctx, store, in.Workspace, in.Task, in.Repo)
		if err != nil {
			return err
		}
		requestID := in.RequestID
		if requestID == "" {
			requestID = "driver:" + in.Attempt
		}
		revision, err = changeset.FreezeSource(ctx, store, runner, changeset.SourceInput{
			Workspace: in.Workspace, Change: change, RequestID: requestID,
			Attempt: in.Attempt, TaskID: in.Task, BaseSHA: in.Base, CaptureSHA: captureSHA,
			Outcome: in.Outcome, Complete: in.Complete,
		})
		return err
	})
	if err == nil && in.SourceRepo != "" {
		err = taskcopy.ImportSnapshot(ctx, journalPath, in.SourceRepo, in.Worktree, in.Workspace, in.Attempt, revision.Change, revision.Number)
	}
	if err == nil {
		err = store.RecordRetainedCopy(ctx, journal.RetainedCopy{Workspace: in.Workspace, Change: revision.Change,
			Attempt: in.Attempt, Path: in.Worktree, SourceRepo: in.SourceRepo, Complete: in.Complete})
	}
	return revision, err
}

// Freeze uses production journal and Git defaults. The patch is staged in a
// private index rooted at Base; neither the worktree nor its index is changed.
func Freeze(ctx context.Context, in Request) (loomgit.Revision, error) {
	return FreezeAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), in)
}

// ChangeForTask resolves the same durable task layer used by Freeze.
func ChangeForTask(ctx context.Context, workspace, task, repo string) (string, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	return ChangeForTaskAt(ctx, path, workspace, task, repo)
}

func ChangeForTaskAt(ctx context.Context, path, workspace, task, repo string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	return changeForTask(ctx, store, workspace, task, repo)
}

func changeForTask(ctx context.Context, store *journal.SQLite, workspace, task, repo string) (string, error) {
	key := sha256.Sum256([]byte(workspace + "\x00" + task + "\x00" + repo))
	return store.DriverChange(ctx, workspace, task, repo, "driver-"+hex.EncodeToString(key[:16]))
}

// FreezeAt permits isolated journals in tests while preserving the same Git path.
func FreezeAt(ctx context.Context, journalPath string, in Request) (loomgit.Revision, error) {
	if in.Workspace == "" || in.Task == "" || in.Repo == "" || in.Attempt == "" || in.Worktree == "" || in.Base == "" || len(in.Patch) == 0 {
		return loomgit.Revision{}, fmt.Errorf("workspace, task, repo, attempt, worktree, base and patch are required")
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return loomgit.Revision{}, err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return loomgit.Revision{}, err
	}
	defer func() { _ = store.Close() }()
	gitOptions := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, gitOptions).Admit(ctx, in.Worktree)
	if err != nil {
		return loomgit.Revision{}, err
	}
	runner, err := gitexec.New(in.Worktree, gitOptions)
	if err != nil {
		return loomgit.Revision{}, err
	}
	var revision loomgit.Revision
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		tree, err := stagePatch(ctx, runner, in)
		if err != nil {
			return err
		}
		revision, err = recordRevision(ctx, store, runner, in, tree)
		return err
	})
	if err == nil && in.SourceRepo != "" {
		err = taskcopy.ImportSnapshot(ctx, journalPath, in.SourceRepo, in.Worktree, in.Workspace, in.Attempt, revision.Change, revision.Number)
	}
	if err == nil {
		err = store.RecordRetainedCopy(ctx, journal.RetainedCopy{Workspace: in.Workspace, Change: revision.Change,
			Attempt: in.Attempt, Path: in.Worktree, SourceRepo: in.SourceRepo, Complete: true})
	}
	return revision, err
}

func stagePatch(ctx context.Context, runner *gitexec.Runner, in Request) (string, error) {
	base, err := runner.Run(ctx, "rev-parse", "--verify", in.Base+"^{commit}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(base)) != in.Base {
		return "", fmt.Errorf("base must be an exact commit SHA")
	}
	index, err := os.CreateTemp("", "loom-driver-index-*")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	_ = index.Close()
	defer func() { _ = os.Remove(indexPath) }()
	env := map[string]string{"GIT_INDEX_FILE": indexPath}
	if _, err = runner.RunWithEnv(ctx, env, "read-tree", in.Base); err != nil {
		return "", err
	}
	if _, err = runner.RunWithInput(ctx, in.Patch, env, "apply", "--cached", "--binary"); err != nil {
		return "", err
	}
	tree, err := runner.RunWithEnv(ctx, env, "write-tree")
	return strings.TrimSpace(string(tree)), err
}

func recordRevision(ctx context.Context, store *journal.SQLite, runner *gitexec.Runner, in Request, tree string) (loomgit.Revision, error) {
	change, err := changeForTask(ctx, store, in.Workspace, in.Task, in.Repo)
	if err != nil {
		return loomgit.Revision{}, err
	}
	requestID := "driver:" + in.Attempt
	stored, err := store.RevisionByRequest(ctx, requestID)
	captureSHA := ""
	if err == nil {
		if stored.Workspace != in.Workspace || stored.Change != change || stored.BaseSHA != in.Base ||
			stored.TreeHash != tree || stored.Outcome != in.Outcome {
			return loomgit.Revision{}, fmt.Errorf("attempt %q was already recorded with different content", in.Attempt)
		}
		if stored.Ready {
			if in.AuthorKind != "" && in.AuthorID != "" {
				if err := store.SetRevisionAuthor(ctx, stored, in.AuthorKind, in.AuthorID); err != nil {
					return loomgit.Revision{}, err
				}
			}
			return stored, nil
		}
		captureSHA = stored.SourceHeadSHA
	} else if !errors.Is(err, journal.ErrNotFound) {
		return loomgit.Revision{}, err
	}
	if captureSHA == "" {
		captureSHA, err = captureHead(ctx, runner, in, tree)
		if err != nil {
			return loomgit.Revision{}, err
		}
	}
	revision, err := changeset.FreezeSource(ctx, store, runner, changeset.SourceInput{
		Workspace: in.Workspace, Change: change, RequestID: requestID,
		Attempt: in.Attempt, TaskID: in.Task, BaseSHA: in.Base,
		CaptureSHA: captureSHA, Outcome: in.Outcome, Complete: true,
	})
	if err != nil {
		return revision, err
	}
	if in.AuthorKind != "" && in.AuthorID != "" {
		if err := store.SetRevisionAuthor(ctx, revision, in.AuthorKind, in.AuthorID); err != nil {
			return revision, err
		}
	}
	return revision, nil
}

func captureHead(ctx context.Context, runner *gitexec.Runner, in Request, tree string) (string, error) {
	parent := in.Base
	if in.CommitHeadSHA != "" {
		resolved, err := runner.Run(ctx, "rev-parse", "--verify", in.CommitHeadSHA+"^{commit}")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(resolved)) != in.CommitHeadSHA {
			return "", fmt.Errorf("runner commit head is not an exact commit SHA")
		}
		if _, err = runner.Run(ctx, "merge-base", "--is-ancestor", in.Base, in.CommitHeadSHA); err != nil {
			return "", fmt.Errorf("runner commit head is not based on recorded base: %w", err)
		}
		parent = in.CommitHeadSHA
	}
	parentTree, err := runner.Run(ctx, "rev-parse", parent+"^{tree}")
	if err != nil {
		return "", err
	}
	if in.CommitHeadSHA != "" && strings.TrimSpace(string(parentTree)) == tree {
		return parent, nil
	}
	commit, err := runner.Run(ctx, "commit-tree", tree, "-p", parent, "-m", "Loom driver attempt "+in.Attempt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(commit)), nil
}
