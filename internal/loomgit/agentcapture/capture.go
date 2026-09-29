// Package agentcapture connects daemon agent exits to the Loom Git capture engine.
package agentcapture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
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

// CaptureWorkingArea saves a reset candidate under a unique workspace WIP ref.
func CaptureWorkingArea(ctx context.Context, repo, workspace, lead string) (Result, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Result{}, err
	}
	ref, err := refname.WIP(workspace, lead, hex.EncodeToString(id))
	if err != nil {
		return Result{}, err
	}
	result, err := captureWithParams(ctx, repo, capture.Params{Workspace: workspace, Attempt: hex.EncodeToString(id), TaskTitle: "reset", Ref: ref})
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
