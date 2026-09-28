// Package agentcapture connects daemon agent exits to the Loom Git capture engine.
package agentcapture

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

type Result struct {
	Ref      string
	SHA      string
	Complete bool
}

func Capture(ctx context.Context, repo, workspace, attempt, taskID, taskTitle string) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	runner, err := gitexec.New(repo, gitexec.Options{GlobalConfig: filepath.Join(home, ".gitconfig")})
	if err != nil {
		return Result{}, err
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{
		Workspace: workspace, Attempt: attempt, TaskID: taskID, TaskTitle: taskTitle,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Ref: result.CaptureRef, SHA: result.CaptureSHA, Complete: result.Manifest.Complete}, nil
}
