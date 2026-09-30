package driver

import (
	"context"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
)

func taskRunCompletionContext(ctx context.Context, status domain.TaskRunStatus) (context.Context, context.CancelFunc) {
	if status == domain.TaskRunCancelled && ctx.Err() != nil {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	return ctx, func() {}
}

func (e HostBridgeTaskExecutor) captureCancelledTask(req TaskExecRequest, copy TaskWorktree) (TaskExecResult, error) {
	result := TaskExecResult{Status: domain.TaskRunCancelled, ExitCode: 130,
		ErrorClass: "driver_cancelled", ErrorMessage: "task run cancelled",
		RuntimeMetadata: map[string]string{"retained_path": copy.Path, "patch_back_status": "retained"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	captured, err := agentcapture.Capture(ctx, copy.Path, req.WorkspaceKey, copy.AttemptID, req.TaskID, req.TaskID)
	if err == nil {
		revision, freezeErr := driverfreeze.FreezeCapture(ctx, driverfreeze.CaptureRequest{
			Workspace: req.WorkspaceKey, Task: req.TaskID, Repo: copy.RepoName, Attempt: copy.AttemptID,
			Worktree: copy.Path, Base: copy.BaseSHA, CaptureSHA: captured.SHA,
			SourceRepo: copy.SourcePath,
			Outcome:    "cancelled", Complete: captured.Complete,
		})
		err = freezeErr
		if err == nil {
			result.RuntimeMetadata["change_id"] = revision.Change
			result.RuntimeMetadata["revision"] = strconv.Itoa(revision.Number)
			result.RuntimeMetadata["revision_head_sha"] = revision.HeadSHA
			result.RuntimeMetadata["revision_incomplete"] = strconv.FormatBool(revision.Incomplete)
			result.RuntimeMetadata["capture_ref"] = captured.Ref
			result.RuntimeMetadata["patch_back_status"] = "frozen"
		}
	}
	if err != nil {
		result.ErrorClass = "capture_failed"
		result.ErrorMessage = err.Error()
	}
	return result, nil
}
