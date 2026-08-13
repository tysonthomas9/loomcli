package git

import (
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

func pushResponse(result *sourcecontrol.PushResult) *loomapi.GitMergeResponse {
	if result == nil {
		return nil
	}
	response := &loomapi.GitMergeResponse{
		Success: result.Success, Message: result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
	}
	if len(result.ConflictedFiles) > 0 {
		files := append([]string(nil), result.ConflictedFiles...)
		response.ConflictedFiles = &files
	}
	return response
}

func pullResponse(result *sourcecontrol.PullResult) *loomapi.GitMergeResponse {
	if result == nil {
		return nil
	}
	response := &loomapi.GitMergeResponse{
		Success: result.Success, Message: result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
	}
	if len(result.ConflictedFiles) > 0 {
		files := append([]string(nil), result.ConflictedFiles...)
		response.ConflictedFiles = &files
	}
	return response
}

func syncResponse(result *sourcecontrol.SyncResult) loomapi.GitSyncResponse {
	return loomapi.GitSyncResponse{PushResult: pushResponse(result.Push), PullResult: pullResponse(result.Pull)}
}

func pushAllResponse(result *sourcecontrol.PushAllResult) loomapi.GitPushAllResponse {
	response := loomapi.GitPushAllResponse{
		Pushed: result.Pushed, Failed: result.Failed,
		Results: make([]loomapi.GitPushAllCheckoutResponse, len(result.Results)),
	}
	for index, row := range result.Results {
		response.Results[index] = loomapi.GitPushAllCheckoutResponse{Name: row.AgentID, Success: row.Success}
		if row.Message != "" {
			response.Results[index].Message = gitPointer(row.Message)
		}
		if row.Error != "" {
			response.Results[index].Error = gitPointer(row.Error)
		}
	}
	return response
}

func pullRequestCreationResponse(result *sourcecontrol.PullRequestCreation) loomapi.GitPullRequestCreationResponse {
	response := loomapi.GitPullRequestCreationResponse{
		Created: result.Created, AlreadyExists: result.AlreadyExists, NoCommits: result.NoCommits,
	}
	if result.URL != "" {
		response.Url = gitPointer(result.URL)
	}
	return response
}

func resetResponse(result *sourcecontrol.ResetResult) loomapi.GitResetResponse {
	response := loomapi.GitResetResponse{Success: result.Success, Message: result.Message, Pushed: result.Pushed}
	if result.PreviousBranch != "" {
		response.PreviousBranch = gitPointer(result.PreviousBranch)
	}
	return response
}

func statusResponse(result *sourcecontrol.AgentStatusResult) loomapi.GitStatusResponse {
	changedFiles := append([]string(nil), result.ChangedFiles...)
	if changedFiles == nil {
		changedFiles = []string{}
	}
	conflictedFiles := append([]string(nil), result.ConflictedFiles...)
	if conflictedFiles == nil {
		conflictedFiles = []string{}
	}
	return loomapi.GitStatusResponse{
		Branch: result.Branch, TargetBranch: result.TargetBranch, IsClean: result.Clean,
		Ahead: result.Ahead, Behind: result.Behind, ChangedFiles: changedFiles,
		ConflictedFiles: conflictedFiles, HasConflicts: result.HasConflicts, StashCount: result.StashCount,
	}
}

func gitPointer[T any](value T) *T { return &value }
