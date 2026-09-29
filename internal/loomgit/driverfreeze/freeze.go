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
)

type Request struct {
	Workspace, Task, Repo, Attempt string
	Worktree, Base                 string
	Patch                          []byte
	Outcome                        string
}

// Freeze uses production journal and Git defaults. The patch is staged in a
// private index rooted at Base; neither the worktree nor its index is changed.
func Freeze(ctx context.Context, in Request) (loomgit.Revision, error) {
	return FreezeAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), in)
}

// ChangeForTask resolves the same durable task layer used by Freeze.
func ChangeForTask(ctx context.Context, workspace, task, repo string) (string, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return "", err
	}
	defer store.Close()
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
	defer store.Close()
	repo, err := pool.New(store).Admit(ctx, in.Worktree)
	if err != nil {
		return loomgit.Revision{}, err
	}
	runner, err := gitexec.New(in.Worktree, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return loomgit.Revision{}, err
	}
	var revision loomgit.Revision
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		base, err := runner.Run(ctx, "rev-parse", "--verify", in.Base+"^{commit}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(base)) != in.Base {
			return fmt.Errorf("base must be an exact commit SHA")
		}
		index, err := os.CreateTemp("", "loom-driver-index-*")
		if err != nil {
			return err
		}
		indexPath := index.Name()
		_ = index.Close()
		defer os.Remove(indexPath)
		env := map[string]string{"GIT_INDEX_FILE": indexPath}
		if _, err = runner.RunWithEnv(ctx, env, "read-tree", in.Base); err != nil {
			return err
		}
		if _, err = runner.RunWithInput(ctx, in.Patch, env, "apply", "--cached", "--binary"); err != nil {
			return err
		}
		tree, err := runner.RunWithEnv(ctx, env, "write-tree")
		if err != nil {
			return err
		}
		change, err := changeForTask(ctx, store, in.Workspace, in.Task, in.Repo)
		if err != nil {
			return err
		}
		requestID := "driver:" + in.Attempt
		stored, err := store.RevisionByRequest(ctx, requestID)
		captureSHA := ""
		if err == nil {
			if stored.Workspace != in.Workspace || stored.Change != change || stored.BaseSHA != in.Base ||
				stored.TreeHash != strings.TrimSpace(string(tree)) || stored.Outcome != in.Outcome {
				return fmt.Errorf("attempt %q was already recorded with different content", in.Attempt)
			}
			if stored.Ready {
				revision = stored
				return nil
			}
			captureSHA = stored.SourceHeadSHA
		} else if !errors.Is(err, journal.ErrNotFound) {
			return err
		}
		if captureSHA == "" {
			commit, err := runner.Run(ctx, "commit-tree", strings.TrimSpace(string(tree)), "-p", in.Base, "-m", "Loom driver attempt "+in.Attempt)
			if err != nil {
				return err
			}
			captureSHA = strings.TrimSpace(string(commit))
		}
		revision, err = changeset.FreezeSource(ctx, store, runner, changeset.SourceInput{
			Workspace: in.Workspace, Change: change, RequestID: requestID,
			Attempt: in.Attempt, TaskID: in.Task, BaseSHA: in.Base,
			CaptureSHA: captureSHA, Outcome: in.Outcome, Complete: true,
		})
		return err
	})
	return revision, err
}
