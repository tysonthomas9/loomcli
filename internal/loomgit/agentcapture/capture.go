// Package agentcapture connects daemon agent exits to the Loom Git capture engine.
package agentcapture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

type Entry struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	Size   int64  `json:"size"`
	Reason string `json:"reason,omitempty"`
}

type Result struct {
	Ref      string
	SHA      string
	Complete bool
	Entries  []Entry
}

func ListIgnored(ctx context.Context, repo string) ([]Entry, error) {
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return nil, err
	}
	entries, err := capture.ListIgnored(ctx, runner, repo)
	if err != nil {
		return nil, err
	}
	return copyEntries(entries), nil
}

func copyEntries(entries []capture.Entry) []Entry {
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, Entry{Path: entry.Path, Class: entry.Class, Size: entry.Size, Reason: entry.Reason})
	}
	return result
}

func Capture(ctx context.Context, repo, workspace, attempt, taskID, taskTitle string) (Result, error) {
	return captureWithParams(ctx, repo, capture.Params{Workspace: workspace, Attempt: attempt, TaskID: taskID, TaskTitle: taskTitle})
}

// CaptureTaskCopy takes the agent lock before the source and copy repo leases.
// Linked copies share the source lease, so they must not claim it twice.
func CaptureTaskCopy(ctx context.Context, source, copyPath, workspace, attempt, taskID, taskTitle string) (Result, error) {
	journalPath := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	return CaptureTaskCopyAt(ctx, journalPath, source, copyPath, workspace, attempt, taskID, taskTitle)
}

func CaptureTaskCopyAt(ctx context.Context, journalPath, source, copyPath, workspace, attempt, taskID, taskTitle string) (Result, error) {
	var result Result
	err := WithTaskCopyLease(ctx, journalPath, source, copyPath, func(ctx context.Context) error {
		var captureErr error
		result, captureErr = Capture(ctx, copyPath, workspace, attempt, taskID, taskTitle)
		return captureErr
	})
	return result, err
}

// WithTaskCopyLease serializes a task-copy write with retention cleanup.
func WithTaskCopyLease(ctx context.Context, journalPath, source, copyPath string, write func(context.Context) error) error {
	if source == "" || copyPath == "" {
		return errors.New("source and task copy paths are required")
	}
	lock, running, err := cli.CheckLock(copyPath)
	if err != nil {
		return err
	}
	owned := running && lock != nil && lock.PID == os.Getpid() && lock.Command != "retention"
	if !owned {
		if err := cli.AcquireLock(copyPath, "capture", "loom"); err != nil {
			return err
		}
		defer func() { _ = cli.ReleaseLock(copyPath) }()
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	pool := pool.New(store, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	sourceRepo, err := pool.Admit(ctx, source)
	if err != nil {
		return err
	}
	copyRepo, err := pool.Admit(ctx, copyPath)
	if err != nil {
		return err
	}
	return sourceRepo.WithLock(ctx, func(ctx context.Context) error {
		if sourceRepo.SameStore(copyRepo) {
			return write(ctx)
		}
		return copyRepo.WithLock(ctx, write)
	})
}

// CaptureWorkingArea saves a reset candidate under a unique workspace WIP ref.
func CaptureWorkingArea(ctx context.Context, repo, workspace, lead string) (Result, error) {
	return captureWorkingArea(ctx, repo, workspace, lead, "reset")
}

// CaptureDelegatedWorkingArea saves the user's current edits as a WIP base.
func CaptureDelegatedWorkingArea(ctx context.Context, repo, workspace, lead string) (Result, error) {
	return captureWorkingArea(ctx, repo, workspace, lead, "delegation")
}

func captureWorkingArea(ctx context.Context, repo, workspace, lead, purpose string) (Result, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Result{}, err
	}
	ref, err := refname.WIP(workspace, lead, hex.EncodeToString(id))
	if err != nil {
		return Result{}, err
	}
	result, err := captureWithParams(ctx, repo, capture.Params{Workspace: workspace, Attempt: hex.EncodeToString(id), TaskTitle: purpose, Ref: ref})
	if err != nil {
		return Result{}, err
	}
	if result.SHA == "" {
		runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
		if err != nil {
			return Result{}, err
		}
		head, err := runner.Run(ctx, "rev-parse", "HEAD")
		if err != nil {
			return Result{}, err
		}
		sha := strings.TrimSpace(string(head))
		if err := runner.UpdateRef(ctx, ref, sha, strings.Repeat("0", len(sha))); err != nil {
			return Result{}, err
		}
		result.SHA = sha
	}
	result.Ref = ref
	return result, nil
}

func captureWithParams(ctx context.Context, repo string, params capture.Params) (Result, error) {
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return Result{}, err
	}
	result, err := capture.Capture(ctx, runner, repo, params)
	if err != nil {
		return Result{}, err
	}
	ref := ""
	if result.CaptureSHA != "" {
		ref = result.CaptureRef
	}
	entries := copyEntries(result.Manifest.Entries)
	return Result{Ref: ref, SHA: result.CaptureSHA, Complete: result.Manifest.Complete, Entries: entries}, nil
}
