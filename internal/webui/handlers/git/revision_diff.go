package git

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/gitread"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

func revisionError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	var coded *loomgit.Error
	switch {
	case errors.As(err, &coded):
		code = coded.Code()
		switch coded.Kind {
		case loomgit.RepoSelectionRequired:
			status = http.StatusBadRequest
		case loomgit.WorkspaceUnsupported:
			status = http.StatusNotFound
		case loomgit.BaseRefUnresolvable, loomgit.LineageUnresolved:
			status = http.StatusConflict
		}
	case gitread.IsNotFound(err):
		status, code = http.StatusNotFound, "not_found"
	}
	handler.WriteJSON(w, status, map[string]any{"success": false, "error": code})
}

// HandleRevisionDiff serves recorded revision refs from a selected workspace repo.
func HandleRevisionDiff(interdiff bool) http.HandlerFunc {
	return handleRevisionDiff(interdiff, func() (*gitread.Reader, func() error, error) {
		return gitread.OpenLocal(storeadapter.ResolveRepoPath)
	})
}

func handleRevisionDiff(interdiff bool, open func() (*gitread.Reader, func() error, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		workspace := middleware.WorkspaceFromContext(req.Context())
		if workspace == "" {
			workspace = req.PathValue("ws")
		}
		number, err := strconv.Atoi(req.PathValue("r"))
		if err != nil || number < 1 {
			handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "lineage_unresolved"})
			return
		}
		against := 0
		if interdiff {
			against, err = strconv.Atoi(req.URL.Query().Get("against"))
			if err != nil || against < 1 {
				handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "lineage_unresolved"})
				return
			}
		}
		reader, closeStore, err := open()
		if err != nil {
			revisionError(w, err)
			return
		}
		defer func() { _ = closeStore() }()
		var result gitread.Diff
		if interdiff {
			result, err = reader.Interdiff(req.Context(), workspace, req.PathValue("change"), req.URL.Query().Get("repo"), number, against)
		} else {
			result, err = reader.Diff(req.Context(), workspace, req.PathValue("change"), req.URL.Query().Get("repo"), number)
		}
		if err != nil {
			revisionError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
	}
}

// openRevisionReader opens serve's read-only revision view; tests replace it.
var openRevisionReader = func() (*gitread.Reader, func() error, error) {
	return gitread.OpenLocal(storeadapter.ResolveRepoPath)
}

// HandleTaskDiff serves one task's diff: its newest revision against the
// layer below it in the lead's stack, the same as its PR.
func HandleTaskDiff() http.HandlerFunc {
	return handleTaskDiff(func() (*gitread.Reader, func() error, error) { return openRevisionReader() })
}

func handleTaskDiff(open func() (*gitread.Reader, func() error, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		workspace := middleware.WorkspaceFromContext(req.Context())
		if workspace == "" {
			workspace = req.PathValue("ws")
		}
		reader, closeStore, err := open()
		if err != nil {
			revisionError(w, err)
			return
		}
		defer func() { _ = closeStore() }()
		result, err := reader.TaskDiff(req.Context(), workspace, req.PathValue("id"), req.URL.Query().Get("lead"))
		if err != nil {
			revisionError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
	}
}
