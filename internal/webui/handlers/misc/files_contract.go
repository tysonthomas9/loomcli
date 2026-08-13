package misc

import (
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

func fileCapabilitiesResponse(value sourcecontrol.FileCapabilities) loomapi.FileCapabilitiesResponse {
	return loomapi.FileCapabilitiesResponse{Read: value.Read, Write: value.Write, Sensitive: value.Sensitive}
}

func fileTreeResponse(value *sourcecontrol.FileTreeResult) loomapi.FileTreeResponse {
	entries := make([]loomapi.FileTreeEntry, len(value.Entries))
	for index, entry := range value.Entries {
		entries[index] = loomapi.FileTreeEntry{
			Name: entry.Name, IsDir: entry.IsDir, Size: entry.Size, ModTime: entry.ModTime,
		}
	}
	return loomapi.FileTreeResponse{Path: value.Path, Entries: entries}
}

func fileReadResponse(value *sourcecontrol.FileReadResult) loomapi.FileReadResponse {
	response := loomapi.FileReadResponse{
		Path: value.Path, Size: value.Size, Binary: value.Binary,
		Truncated: value.Truncated, Version: value.Version,
	}
	if value.Content != "" {
		response.Content = pointer(value.Content)
	}
	return response
}

func fileStatResponse(value *sourcecontrol.FileStatResult) loomapi.FileStatResponse {
	return loomapi.FileStatResponse{
		Path: value.Path, IsDir: value.IsDir, Size: value.Size,
		ModTime: value.ModTime, Version: value.Version,
	}
}

func fileIndexResponse(value *sourcecontrol.FileIndexResult) loomapi.FileIndexResponse {
	reasons := make([]loomapi.FilePartialReason, len(value.PartialReasons))
	for index, reason := range value.PartialReasons {
		reasons[index] = loomapi.FilePartialReason(reason)
	}
	paths := append([]string(nil), value.Paths...)
	if paths == nil {
		paths = []string{}
	}
	return loomapi.FileIndexResponse{Paths: paths, Truncated: value.Truncated, PartialReasons: reasons}
}

func fileSearchCommand(value loomapi.FileSearchRequest) sourcecontrol.FileSearchRequest {
	command := sourcecontrol.FileSearchRequest{Query: value.Query}
	if value.Repo != nil {
		command.Repo = *value.Repo
	}
	if value.Regex != nil {
		command.Regex = *value.Regex
	}
	if value.Include != nil {
		command.Include = append([]string(nil), (*value.Include)...)
	}
	if value.Exclude != nil {
		exclude := append([]string(nil), (*value.Exclude)...)
		command.Exclude = &exclude
	}
	if value.CaseSensitive != nil {
		command.CaseSensitive = *value.CaseSensitive
	}
	return command
}

func fileSearchResponse(value *sourcecontrol.FileSearchResult) loomapi.FileSearchResponse {
	results := make([]loomapi.FileSearchFileResult, len(value.Results))
	for fileIndex, file := range value.Results {
		matches := make([]loomapi.FileSearchMatch, len(file.Matches))
		for matchIndex, match := range file.Matches {
			matches[matchIndex] = loomapi.FileSearchMatch{Line: match.Line, Col: match.Col, Preview: match.Preview}
		}
		results[fileIndex] = loomapi.FileSearchFileResult{Path: file.Path, Matches: matches}
	}
	reasons := make([]loomapi.FilePartialReason, len(value.PartialReasons))
	for index, reason := range value.PartialReasons {
		reasons[index] = loomapi.FilePartialReason(reason)
	}
	return loomapi.FileSearchResponse{Results: results, LimitHit: value.LimitHit, PartialReasons: reasons}
}

func fileCheckoutErrors(values []sourcecontrol.FileCheckoutError) []loomapi.FileCheckoutError {
	result := make([]loomapi.FileCheckoutError, len(values))
	for index, value := range values {
		result[index] = loomapi.FileCheckoutError{
			Kind: loomapi.FileCheckoutErrorKind(value.Kind), Repo: value.Repo, Error: value.Error,
		}
		if value.Agent != "" {
			result[index].Agent = pointer(value.Agent)
		}
	}
	return result
}

func fileGitStatusResponse(value sourcecontrol.FileGitStatusResult) loomapi.FileGitStatusResponse {
	status := value.Status
	if status == nil {
		status = map[string]string{}
	}
	return loomapi.FileGitStatusResponse{
		Status: status, Partial: value.Partial, LimitHit: value.LimitHit,
		Errors: fileCheckoutErrors(value.Errors),
	}
}

func fileCheckoutsResponse(value *sourcecontrol.FileCheckoutsResult) loomapi.FileCheckoutsResponse {
	checkouts := make([]loomapi.FileCheckout, len(value.Checkouts))
	for index, checkout := range value.Checkouts {
		checkouts[index] = loomapi.FileCheckout{
			Kind: loomapi.FileCheckoutKind(checkout.Kind), Repo: checkout.Repo,
			Exists: checkout.Exists, ChangeCount: checkout.ChangeCount,
		}
		if checkout.Agent != "" {
			checkouts[index].Agent = pointer(checkout.Agent)
		}
		if checkout.Branch != "" {
			checkouts[index].Branch = pointer(checkout.Branch)
		}
		if checkout.StatusError {
			checkouts[index].StatusError = pointer(true)
		}
		if checkout.Error != "" {
			checkouts[index].Error = pointer(checkout.Error)
		}
		if checkout.Partial {
			checkouts[index].Partial = pointer(true)
		}
		if checkout.LimitHit {
			checkouts[index].LimitHit = pointer(true)
		}
	}
	return loomapi.FileCheckoutsResponse{
		Checkouts: checkouts, Partial: value.Partial, LimitHit: value.LimitHit,
		Errors: fileCheckoutErrors(value.Errors),
	}
}

func fileRepairCommand(value loomapi.FileCheckoutRepairRequest) sourcecontrol.FileCheckoutRepairRequest {
	command := sourcecontrol.FileCheckoutRepairRequest{Scope: string(value.Scope), Target: value.Target}
	if value.Repo != nil {
		command.Repo = *value.Repo
	}
	if value.Force != nil {
		command.Force = *value.Force
	}
	return command
}

func fileRepairResponse(value *sourcecontrol.RepairResult) loomapi.FileCheckoutRepairResponse {
	response := loomapi.FileCheckoutRepairResponse{
		Repaired: value.Repaired, Method: loomapi.FileCheckoutRepairResponseMethod(value.Method), Message: value.Message,
	}
	if value.RequiresForce {
		response.RequiresForce = pointer(true)
	}
	if value.BackupPath != "" {
		response.BackupPath = pointer(value.BackupPath)
	}
	return response
}

func fileDiffResponse(value *sourcecontrol.FileDiffResult) loomapi.FileDiffResponse {
	return loomapi.FileDiffResponse{Path: value.Path, Patch: value.Patch, Partial: value.Partial, LimitHit: value.LimitHit}
}

func fileBlameResponse(value *sourcecontrol.FileBlameResult) loomapi.FileBlameResponse {
	lines := make([]loomapi.FileBlameLine, len(value.Lines))
	for index, line := range value.Lines {
		lines[index] = loomapi.FileBlameLine{
			Line: line.Line, Lines: line.Lines, Sha: line.SHA,
			Author: line.Author, Time: line.Time, Summary: line.Summary,
		}
	}
	response := loomapi.FileBlameResponse{
		Path: value.Path, Skipped: value.Skipped, Lines: lines,
		Partial: value.Partial, LimitHit: value.LimitHit,
	}
	if value.Reason != "" {
		response.Reason = pointer(value.Reason)
	}
	if value.Message != "" {
		response.Message = pointer(value.Message)
	}
	return response
}

func fileHistoryResponse(value *sourcecontrol.FileHistoryResult) loomapi.FileHistoryResponse {
	entries := make([]loomapi.FileHistoryEntry, len(value.Entries))
	for index, entry := range value.Entries {
		entries[index] = loomapi.FileHistoryEntry{
			Kind: loomapi.FileHistoryEntryKind(entry.Kind), Sha: entry.SHA,
			Author: entry.Author, Time: entry.Time, Summary: entry.Summary,
		}
	}
	return loomapi.FileHistoryResponse{
		Path: value.Path, Entries: entries, Partial: value.Partial, LimitHit: value.LimitHit,
	}
}

func fileMutationResponse(value *sourcecontrol.FileMutationResult) loomapi.FileMutationResponse {
	return loomapi.FileMutationResponse{Success: value.Success, Version: value.Version}
}

func pointer[T any](value T) *T { return &value }

func valueOrZero[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
