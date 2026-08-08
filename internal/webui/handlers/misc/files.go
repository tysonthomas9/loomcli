package misc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// IsBinaryContent checks if data is likely binary (non-UTF-8 or contains null bytes).
func IsBinaryContent(data []byte) bool {
	return filecoord.IsBinaryContent(data)
}

// scopeFromQuery parses the scope/target query params for the scoped file
// browser, defaulting to the workspace scope when scope is omitted.
func scopeFromQuery(r *http.Request) (filecoord.FileScope, string, string) {
	scope := filecoord.FileScope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = filecoord.ScopeWorkspace
	}
	return scope, r.URL.Query().Get("target"), r.URL.Query().Get("repo")
}

func repoFromQueryAndBody(queryRepo, bodyRepo string) (string, error) {
	queryRepo = strings.TrimSpace(queryRepo)
	bodyRepo = strings.TrimSpace(bodyRepo)
	if queryRepo != "" && bodyRepo != "" && queryRepo != bodyRepo {
		return "", apperrors.ErrValidation("repo query and request body values differ")
	}
	if bodyRepo != "" {
		return bodyRepo, nil
	}
	return queryRepo, nil
}

func parseStrongETag(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, ",") || strings.HasPrefix(value, "W/") || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", apperrors.ErrValidation("precondition must contain one strong quoted ETag")
	}
	value = value[1 : len(value)-1]
	if value == "" || strings.ContainsAny(value, "\r\n\"") {
		return "", apperrors.ErrValidation("invalid ETag precondition")
	}
	return value, nil
}

func quotedETag(version string) string {
	return `"` + version + `"`
}

func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	defer r.Body.Close()
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(dst)
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if strings.Contains(err.Error(), "http: request body too large") {
		handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	handler.RespondError(w, http.StatusBadRequest, "invalid request body")
	return false
}

// HandleFileCapabilities returns the permissions already established by file middleware.
func HandleFileCapabilities() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capabilities, ok := filecoord.FileCapabilitiesFromContext(r.Context())
		if !ok {
			handler.RespondError(w, http.StatusForbidden, "forbidden")
			return
		}
		handler.WriteJSON(w, http.StatusOK, capabilities)
	}
}

// HandleScopedFileTree handles GET /api/workspaces/{ws}/files/tree?scope=&target=&path=.
func HandleScopedFileTree(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ListDirectoryScoped(r.Context(), wsID, scope, target, repo, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileStat handles GET /api/workspaces/{ws}/files/stat?scope=&target=&path=.
func HandleScopedFileStat(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		result, err := svc.StatPathScoped(r.Context(), wsID, scope, target, repo, r.URL.Query().Get("path"))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileRead handles GET /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileRead(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		rev := r.URL.Query().Get("rev")

		var result *filecoord.FileReadResult
		var err error
		if strings.TrimSpace(rev) != "" {
			result, err = svc.ReadFileAtRevScoped(r.Context(), wsID, scope, target, repo, reqPath, rev)
		} else {
			result, err = svc.ReadFileScoped(r.Context(), wsID, scope, target, repo, reqPath)
		}
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if strings.TrimSpace(rev) == "" && result.Version != "" {
			w.Header().Set("ETag", quotedETag(result.Version))
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileDiff handles GET /api/workspaces/{ws}/files/diff?scope=&target=&path=&from=&to=.
func HandleScopedFileDiff(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		result, err := svc.DiffFileScoped(r.Context(), wsID, scope, target, repo, reqPath, from, to)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileHistory handles GET /api/workspaces/{ws}/files/history?scope=&target=&path=.
func HandleScopedFileHistory(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.HistoryFileScoped(r.Context(), wsID, scope, target, repo, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileBlame handles GET /api/workspaces/{ws}/files/blame?scope=&target=&path=.
func HandleScopedFileBlame(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.BlameFileScoped(r.Context(), wsID, scope, target, repo, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileIndex handles GET /api/workspaces/{ws}/files/index?scope=&target=.
func HandleScopedFileIndex(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)

		result, err := svc.IndexFilesScoped(r.Context(), wsID, scope, target, repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileSearch handles POST /api/workspaces/{ws}/files/search?scope=&target=.
func HandleScopedFileSearch(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)

		var req filecoord.FileSearchRequest
		if r.Body == nil {
			handler.RespondError(w, http.StatusBadRequest, "request body is required")
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(&req); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, req.Repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		result, err := svc.SearchFilesScoped(r.Context(), wsID, scope, target, repo, req)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedGitStatus handles GET /api/workspaces/{ws}/files/git-status?scope=&target=.
func HandleScopedGitStatus(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)

		result, err := svc.GitStatusScoped(r.Context(), wsID, scope, target, repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleFileCheckouts handles GET /api/workspaces/{ws}/files/checkouts.
func HandleFileCheckouts(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.ListFileCheckouts(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleFileCheckoutRepair handles POST /api/workspaces/{ws}/files/checkouts/repair.
func HandleFileCheckoutRepair(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if r.Body == nil {
			handler.RespondError(w, http.StatusBadRequest, "request body is required")
			return
		}
		defer r.Body.Close()

		var req filecoord.FileCheckoutRepairRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(&req); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		result, err := svc.RepairCheckout(r.Context(), wsID, req)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// fileWriteRequest is the JSON body for the scoped workspace file write endpoint.
type fileWriteRequest struct {
	Content string `json:"content"`
	Repo    string `json:"repo,omitempty"`
}

type fileRepoRequest struct {
	Repo string `json:"repo,omitempty"`
}

// HandleScopedFileWrite handles PUT /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileWrite(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		var req fileWriteRequest
		if r.Body == nil {
			handler.RespondError(w, http.StatusBadRequest, "request body is required")
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(&req); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, req.Repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		ifMatch, err := parseStrongETag(r.Header.Get("If-Match"))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match"))
		if ifNoneMatch != "" && ifNoneMatch != "*" {
			handler.HandleServiceError(w, apperrors.ErrValidation("If-None-Match only supports *"))
			return
		}
		result, err := svc.WriteFileConditionalScoped(r.Context(), wsID, scope, target, repo, reqPath, req.Content, filecoord.FileWritePreconditions{IfMatch: ifMatch, IfNoneMatch: ifNoneMatch == "*"})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileDelete handles DELETE /api/workspaces/{ws}/files?scope=&target=&path=&recursive=1.
func HandleScopedFileDelete(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		recursive := r.URL.Query().Get("recursive") == "1" || r.URL.Query().Get("recursive") == "true"

		version, err := parseStrongETag(r.Header.Get("If-Match"))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if err := svc.DeletePathVersionedScoped(r.Context(), wsID, scope, target, repo, reqPath, recursive, version); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMkdir handles POST /api/workspaces/{ws}/files/mkdir?scope=&target=&path=.
func HandleScopedFileMkdir(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		var req fileRepoRequest
		if !decodeOptionalJSONBody(w, r, &req) {
			return
		}
		repo, err := repoFromQueryAndBody(queryRepo, req.Repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		if err := svc.MkdirScoped(r.Context(), wsID, scope, target, repo, reqPath); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMove handles PATCH /api/workspaces/{ws}/files/move?scope=&target=.
func HandleScopedFileMove(svc filecoord.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)

		var req filecoord.FileMoveRequest
		if r.Body == nil {
			handler.RespondError(w, http.StatusBadRequest, "request body is required")
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(&req); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, req.Repo)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		result, err := svc.MovePathVersionedScoped(r.Context(), wsID, scope, target, repo, req.From, req.To, req.Overwrite, req.SourceVersion, req.DestinationVersion)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, result)
	}
}
