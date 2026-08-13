package git

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// DiffBrowse is the consumer-defined portion of Source Control Browse needed
// by these HTTP routes.
type DiffBrowse interface {
	DiffStat(context.Context, sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error)
	DiffCommits(context.Context, sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error)
	DiffFiles(context.Context, sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error)
	DiffFilePatch(context.Context, sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error)
}

func respondDiffError(w http.ResponseWriter, status int, msg string) {
	handler.RespondError(w, status, msg)
}

// HandleDiffCommits handles GET /api/agents/{name}/diff/commits?limit=N
func HandleDiffCommits(browse DiffBrowse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		limitPtr, err := parseIntParam(r.URL.Query(), "limit")
		if err != nil {
			respondDiffError(w, http.StatusBadRequest, err.Error())
			return
		}
		limit := 0
		if limitPtr != nil {
			limit = *limitPtr
		}

		from := r.URL.Query().Get("from")

		commits, svcErr := browse.DiffCommits(r.Context(), sourcecontrol.DiffCommitsQuery{
			WorkspaceKey: wsID,
			AgentID:      agentName,
			From:         from,
			Limit:        limit,
		})
		if svcErr != nil {
			handler.HandleSourceControlError(w, svcErr)
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.GitDiffCommitsResponse{
			Success: true,
			Data:    loomapi.GitDiffCommitsData{Commits: diffCommitDTOs(commits)},
		})
	}
}

// HandleDiffFiles handles GET /api/agents/{name}/diff/files?to=HEAD&from=X
func HandleDiffFiles(browse DiffBrowse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		files, err := browse.DiffFiles(r.Context(), sourcecontrol.DiffFilesQuery{
			WorkspaceKey: wsID,
			AgentID:      agentName,
			From:         from,
			To:           to,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.GitDiffFilesResponse{
			Success: true,
			Data:    loomapi.GitDiffFilesData{Files: diffFileDTOs(files)},
		})
	}
}

// HandleDiffFile handles GET /api/agents/{name}/diff/file?path=X&to=HEAD&from=Y
func HandleDiffFile(browse DiffBrowse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		q := r.URL.Query()
		filePath := q.Get("path")
		from := q.Get("from")
		to := q.Get("to")

		result, err := browse.DiffFilePatch(r.Context(), sourcecontrol.DiffFilePatchQuery{
			WorkspaceKey: wsID,
			AgentID:      agentName,
			From:         from,
			To:           to,
			Path:         filePath,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.GitDiffFileResponse{
			Success: true,
			Data:    diffFilePatchDTOFrom(result),
		})
	}
}

func diffCommitDTOs(commits []sourcecontrol.DiffCommit) []loomapi.GitDiffCommit {
	result := make([]loomapi.GitDiffCommit, len(commits))
	for index, commit := range commits {
		result[index] = loomapi.GitDiffCommit{
			Hash: commit.Hash, ShortHash: commit.ShortHash, Subject: commit.Subject,
			Author: commit.Author, Email: commit.Email, Date: commit.Date,
		}
	}
	return result
}

func diffFileDTOs(files []sourcecontrol.DiffFile) []loomapi.GitDiffFile {
	result := make([]loomapi.GitDiffFile, len(files))
	for index, file := range files {
		result[index] = loomapi.GitDiffFile{
			Path: file.Path, Status: file.Status,
			Additions: file.Additions, Deletions: file.Deletions,
		}
		if file.OldPath != "" {
			result[index].OldPath = gitPointer(file.OldPath)
		}
	}
	return result
}

func diffFilePatchDTOFrom(patch *sourcecontrol.DiffFilePatch) loomapi.GitDiffFilePatch {
	return loomapi.GitDiffFilePatch{
		Patch: patch.Patch, IsBinary: patch.IsBinary, IsTooLarge: patch.IsTooLarge,
		Additions: patch.Additions, Deletions: patch.Deletions,
	}
}
