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
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
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
	SkipRetention                  bool
	// ScreenTaskCopy applies the capture rules (D18) to the task copy before
	// freezing: a left-out secret file makes the revision incomplete and its
	// manifest records what was left out. For captures not made by Capture,
	// such as a run that changed nothing.
	ScreenTaskCopy bool
}

// FreezeCapture records a host capture as a source revision. The caller retains
// the worktree for recovery.
func FreezeCapture(ctx context.Context, in CaptureRequest) (loomgit.Revision, error) {
	return FreezeCaptureAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), in)
}

func CaptureAlreadyFrozen(ctx context.Context, workspace, task, repo, attempt string) (bool, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	change, err := changeForTask(ctx, store, workspace, task, repo)
	if err != nil {
		return false, err
	}
	revision, err := store.RevisionByRequest(ctx, "driver:"+attempt)
	if errors.Is(err, journal.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if revision.Workspace != workspace || revision.Change != change {
		return false, fmt.Errorf("attempt %q belongs to another task", attempt)
	}
	return revision.Ready, nil
}

func FreezeCaptureAt(ctx context.Context, journalPath string, in CaptureRequest) (loomgit.Revision, error) {
	if in.Workspace == "" || in.Task == "" || in.Repo == "" || in.Attempt == "" || in.Worktree == "" || in.Base == "" || !terminalOutcome(in.Outcome) {
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
	runner, err := gitexec.New(in.Worktree, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return loomgit.Revision{}, err
	}
	var revision loomgit.Revision
	err = agentcapture.WithTaskCopyLease(ctx, journalPath, sourceForFreeze(in.SourceRepo, in.Worktree), in.Worktree, func(ctx context.Context) error {
		captureSHA, err := captureSHAForRequest(ctx, runner, in.CaptureSHA)
		if err != nil {
			return err
		}
		change, err := changeForTask(ctx, store, in.Workspace, in.Task, in.Repo)
		if err != nil {
			return err
		}
		requestID := in.RequestID
		if requestID == "" {
			requestID = "driver:" + in.Attempt
		}
		complete := in.Complete
		if in.ScreenTaskCopy {
			if complete, err = screenTaskCopy(ctx, runner, in); err != nil {
				return err
			}
		}
		revision, err = changeset.FreezeSource(ctx, store, runner, changeset.SourceInput{
			Workspace: in.Workspace, Change: change, RequestID: requestID,
			Attempt: in.Attempt, TaskID: in.Task, BaseSHA: in.Base, CaptureSHA: captureSHA,
			Outcome: in.Outcome, Complete: complete,
		})
		if err != nil {
			return err
		}
		if in.SourceRepo != "" {
			if err := taskcopy.ImportSnapshotUnderLease(ctx, journalPath, in.SourceRepo, in.Worktree, in.Workspace, in.Attempt, revision.Change, revision.Number); err != nil {
				return err
			}
		}
		if in.SkipRetention {
			return nil
		}
		return store.RecordRetainedCopy(ctx, journal.RetainedCopy{Workspace: in.Workspace, Change: revision.Change,
			Attempt: in.Attempt, Path: in.Worktree, SourceRepo: in.SourceRepo, Complete: complete})
	})
	return revision, err
}

// screenTaskCopy records what the task copy's capture leaves out and reports
// whether the capture is still complete.
func screenTaskCopy(ctx context.Context, runner *gitexec.Runner, in CaptureRequest) (bool, error) {
	entries, err := capture.ScreenTaskCopy(ctx, runner, in.Worktree)
	if err != nil {
		return false, err
	}
	complete := in.Complete && capture.Complete(entries)
	if len(entries) > 0 {
		if _, err := capture.SaveManifest(ctx, runner, in.Worktree, capture.Manifest{Workspace: in.Workspace,
			Attempt: in.Attempt, Entries: entries, Complete: complete, Retained: !complete}); err != nil {
			return false, err
		}
	}
	return complete, nil
}

func captureSHAForRequest(ctx context.Context, runner *gitexec.Runner, captureSHA string) (string, error) {
	if captureSHA != "" {
		return captureSHA, nil
	}
	head, err := runner.Run(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(string(head)), err
}

func terminalOutcome(outcome string) bool {
	switch outcome {
	case "cancelled", "failed", "completed", "abandoned":
		return true
	default:
		return false
	}
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

func sourceForFreeze(source, worktree string) string {
	if source != "" {
		return source
	}
	return worktree
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
	runner, err := gitexec.New(in.Worktree, gitOptions)
	if err != nil {
		return loomgit.Revision{}, err
	}
	var revision loomgit.Revision
	err = agentcapture.WithTaskCopyLease(ctx, journalPath, sourceForFreeze(in.SourceRepo, in.Worktree), in.Worktree, func(ctx context.Context) error {
		tree, entries, err := stagePatch(ctx, runner, in)
		if err != nil {
			return err
		}
		entries, err = withIgnored(ctx, runner, in.Worktree, entries)
		if err != nil {
			return err
		}
		complete := capture.Complete(entries)
		if len(entries) > 0 {
			if _, err := capture.SaveManifest(ctx, runner, in.Worktree, capture.Manifest{Workspace: in.Workspace,
				Attempt: in.Attempt, Entries: entries, Complete: complete, Retained: !complete}); err != nil {
				return err
			}
		}
		revision, err = recordRevision(ctx, store, runner, in, tree, complete)
		if err != nil {
			return err
		}
		if in.SourceRepo != "" {
			if err := taskcopy.ImportSnapshotUnderLease(ctx, journalPath, in.SourceRepo, in.Worktree, in.Workspace, in.Attempt, revision.Change, revision.Number); err != nil {
				return err
			}
		}
		return store.RecordRetainedCopy(ctx, journal.RetainedCopy{Workspace: in.Workspace, Change: revision.Change,
			Attempt: in.Attempt, Path: in.Worktree, SourceRepo: in.SourceRepo, Complete: complete})
	})
	return revision, err
}

// withIgnored adds the task copy's ignored files to the screened patch entries
// (D18): they are listed, never captured, as in a cancel-time capture.
func withIgnored(ctx context.Context, runner *gitexec.Runner, worktree string, entries []capture.Entry) ([]capture.Entry, error) {
	ignored, err := capture.IgnoredEntries(ctx, runner, worktree)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seen[entry.Path] = true
	}
	for _, entry := range ignored {
		if !seen[entry.Path] {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// stagePatch stages the flat patch on Base in a private index, then applies
// the capture engine's D18 rules: untracked secret-pattern paths never enter
// the frozen tree.
func stagePatch(ctx context.Context, runner *gitexec.Runner, in Request) (string, []capture.Entry, error) {
	base, err := runner.Run(ctx, "rev-parse", "--verify", in.Base+"^{commit}")
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(string(base)) != in.Base {
		return "", nil, fmt.Errorf("base must be an exact commit SHA")
	}
	index, err := os.CreateTemp("", "loom-driver-index-*")
	if err != nil {
		return "", nil, err
	}
	indexPath := index.Name()
	_ = index.Close()
	defer func() { _ = os.Remove(indexPath) }()
	env := map[string]string{"GIT_INDEX_FILE": indexPath}
	if _, err = runner.RunWithEnv(ctx, env, "read-tree", in.Base); err != nil {
		return "", nil, err
	}
	if _, err = runner.RunWithInput(ctx, in.Patch, env, "apply", "--cached", "--binary"); err != nil {
		return "", nil, err
	}
	parent := in.Base
	if in.CommitHeadSHA != "" {
		parent = in.CommitHeadSHA
	}
	entries, err := capture.ScreenStaged(ctx, runner, env, parent)
	if err != nil {
		return "", nil, err
	}
	tree, err := runner.RunWithEnv(ctx, env, "write-tree")
	return strings.TrimSpace(string(tree)), entries, err
}

func recordRevision(ctx context.Context, store *journal.SQLite, runner *gitexec.Runner, in Request, tree string, complete bool) (loomgit.Revision, error) {
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
		CaptureSHA: captureSHA, Outcome: in.Outcome, Complete: complete,
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
