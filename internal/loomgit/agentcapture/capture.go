// Package agentcapture connects daemon agent exits to the Loom Git capture engine.
package agentcapture

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

type Result struct {
	Ref      string
	SHA      string
	Complete bool
}

func Capture(ctx context.Context, repo, workspace, attempt, taskID, taskTitle string) (Result, error) {
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return Result{}, err
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{
		Workspace: workspace, Attempt: attempt, TaskID: taskID, TaskTitle: taskTitle,
	})
	if err != nil {
		return Result{}, err
	}
	ref := ""
	if result.CaptureSHA != "" {
		ref = result.CaptureRef
	}
	return Result{Ref: ref, SHA: result.CaptureSHA, Complete: result.Manifest.Complete}, nil
}
