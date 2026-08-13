package misc

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// IsBinaryContent checks if data is likely binary (non-UTF-8 or contains null bytes).
func IsBinaryContent(data []byte) bool {
	return sourcecontrol.IsBinaryContent(data)
}

// scopeFromQuery parses the scope/target query params for the scoped file
// browser, defaulting to the workspace scope when scope is omitted.
func scopeFromQuery(r *http.Request) (sourcecontrol.FileScope, string, string) {
	scope := sourcecontrol.FileScope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = sourcecontrol.ScopeWorkspace
	}
	return scope, r.URL.Query().Get("target"), r.URL.Query().Get("repo")
}

func fileLocation(wsID string, scope sourcecontrol.FileScope, target, repo string) sourcecontrol.FileLocation {
	return sourcecontrol.FileLocation{WorkspaceKey: wsID, Scope: scope, Target: target, Repository: repo}
}

func fileAccessGrant(r *http.Request) sourcecontrol.AccessGrant {
	grant, _ := middleware.FileAccessGrantFromContext(r.Context())
	return grant
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

func optionalFileRepo(repo *string) string {
	if repo == nil {
		return ""
	}
	return *repo
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
	return decodeJSONBody(w, r, dst, true)
}

func decodeRequiredJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONBody(w, r, dst, false)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, optional bool) bool {
	if r.Body == nil {
		if optional {
			return true
		}
		handler.RespondError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	defer r.Body.Close()
	err := handler.DecodeOneJSON(w, r, dst, handler.JSONDecodeOptions{DisallowUnknownFields: true})
	if err == nil {
		return true
	}
	if optional && errors.Is(err, io.EOF) {
		return true
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	handler.RespondError(w, http.StatusBadRequest, "invalid request body")
	return false
}

// HandleFileCapabilities returns the permissions already established by file middleware.
func HandleFileCapabilities() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		grant, ok := middleware.FileAccessGrantFromContext(r.Context())
		if !ok {
			handler.RespondError(w, http.StatusForbidden, "forbidden")
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileCapabilitiesResponse(grant.Capabilities()))
	}
}

// HandleScopedFileTree handles GET /api/workspaces/{ws}/files/tree?scope=&target=&path=.
func HandleScopedFileTree(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ListDirectory(r.Context(), sourcecontrol.PathQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileTreeResponse(result))
	}
}

// HandleScopedFileStat handles GET /api/workspaces/{ws}/files/stat?scope=&target=&path=.
func HandleScopedFileStat(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		result, err := svc.StatPath(r.Context(), sourcecontrol.PathQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: r.URL.Query().Get("path"),
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, fileStatResponse(result))
	}
}

// HandleScopedFileRead handles GET /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileRead(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		rev := r.URL.Query().Get("rev")

		var result *sourcecontrol.FileReadResult
		var err error
		if strings.TrimSpace(rev) != "" {
			result, err = svc.ReadFileAtRevision(r.Context(), sourcecontrol.RevisionQuery{
				Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath, Revision: rev,
			})
		} else {
			result, err = svc.ReadFile(r.Context(), sourcecontrol.PathQuery{
				Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath,
			})
		}
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		if strings.TrimSpace(rev) == "" && result.Version != "" {
			w.Header().Set("ETag", quotedETag(result.Version))
		}
		handler.WriteJSON(w, http.StatusOK, fileReadResponse(result))
	}
}

// HandleScopedFileDiff handles GET /api/workspaces/{ws}/files/diff?scope=&target=&path=&from=&to=.
func HandleScopedFileDiff(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		result, err := svc.DiffPath(r.Context(), sourcecontrol.PathDiffQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
			Path: reqPath, From: from, To: to,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileDiffResponse(result))
	}
}

// HandleScopedFileHistory handles GET /api/workspaces/{ws}/files/history?scope=&target=&path=.
func HandleScopedFileHistory(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.PathHistory(r.Context(), sourcecontrol.PathQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileHistoryResponse(result))
	}
}

// HandleScopedFileBlame handles GET /api/workspaces/{ws}/files/blame?scope=&target=&path=.
func HandleScopedFileBlame(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.BlamePath(r.Context(), sourcecontrol.PathQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileBlameResponse(result))
	}
}

// HandleScopedFileIndex handles GET /api/workspaces/{ws}/files/index?scope=&target=.
func HandleScopedFileIndex(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)

		result, err := svc.IndexFiles(r.Context(), sourcecontrol.LocationQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileIndexResponse(result))
	}
}

// HandleScopedFileSearch handles POST /api/workspaces/{ws}/files/search?scope=&target=.
func HandleScopedFileSearch(svc sourcecontrol.Browse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)

		var req loomapi.FileSearchRequest
		if !decodeRequiredJSONBody(w, r, &req) {
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, optionalFileRepo(req.Repo))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		result, err := svc.SearchFiles(r.Context(), sourcecontrol.SearchQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Search: fileSearchCommand(req),
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileSearchResponse(result))
	}
}

// HandleScopedGitStatus handles GET /api/workspaces/{ws}/files/git-status?scope=&target=.
func HandleScopedGitStatus(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, repo := scopeFromQuery(r)

		result, err := svc.Status(r.Context(), sourcecontrol.LocationQuery{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileGitStatusResponse(result))
	}
}

// HandleFileCheckouts handles GET /api/workspaces/{ws}/files/checkouts.
func HandleFileCheckouts(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.ListCheckouts(r.Context(), sourcecontrol.WorkspaceQuery{
			Grant: fileAccessGrant(r), WorkspaceKey: wsID,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileCheckoutsResponse(result))
	}
}

// HandleFileCheckoutRepair handles POST /api/workspaces/{ws}/files/checkouts/repair.
func HandleFileCheckoutRepair(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.FileCheckoutRepairRequest
		if !decodeRequiredJSONBody(w, r, &req) {
			return
		}

		repair := fileRepairCommand(req)
		result, err := svc.Repair(r.Context(), sourcecontrol.RepairCommand{
			Grant:    fileAccessGrant(r),
			Location: fileLocation(wsID, sourcecontrol.FileScope(repair.Scope), repair.Target, repair.Repo),
			Force:    repair.Force,
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, fileRepairResponse(result))
	}
}

// HandleScopedFileWrite handles PUT /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileWrite(svc sourcecontrol.Mutate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		var req loomapi.FileWriteRequest
		if !decodeRequiredJSONBody(w, r, &req) {
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, optionalFileRepo(req.Repo))
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
		result, err := svc.WriteFile(r.Context(), sourcecontrol.WriteCommand{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
			Path: reqPath, Content: req.Content, ExpectedVersion: ifMatch, CreateOnly: ifNoneMatch == "*",
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, fileMutationResponse(result))
	}
}

// HandleScopedFileDelete handles DELETE /api/workspaces/{ws}/files?scope=&target=&path=&recursive=1.
func HandleScopedFileDelete(svc sourcecontrol.Mutate) http.HandlerFunc {
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
		if err := svc.DeletePath(r.Context(), sourcecontrol.DeleteCommand{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
			Path: reqPath, Recursive: recursive, ExpectedVersion: version,
		}); err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMkdir handles POST /api/workspaces/{ws}/files/mkdir?scope=&target=&path=.
func HandleScopedFileMkdir(svc sourcecontrol.Mutate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		var req loomapi.FileRepoQualifierRequest
		if !decodeOptionalJSONBody(w, r, &req) {
			return
		}
		repo, err := repoFromQueryAndBody(queryRepo, optionalFileRepo(req.Repo))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		if err := svc.CreateDirectory(r.Context(), sourcecontrol.CreateDirectoryCommand{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo), Path: reqPath,
		}); err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMove handles PATCH /api/workspaces/{ws}/files/move?scope=&target=.
func HandleScopedFileMove(svc sourcecontrol.Mutate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target, queryRepo := scopeFromQuery(r)

		var req loomapi.FileMoveRequest
		if !decodeRequiredJSONBody(w, r, &req) {
			return
		}

		repo, err := repoFromQueryAndBody(queryRepo, optionalFileRepo(req.Repo))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		result, err := svc.MovePath(r.Context(), sourcecontrol.MoveCommand{
			Grant: fileAccessGrant(r), Location: fileLocation(wsID, scope, target, repo),
			From: req.From, To: req.To, Overwrite: valueOrZero(req.Overwrite),
			ExpectedSourceVersion:      valueOrZero(req.SourceVersion),
			ExpectedDestinationVersion: valueOrZero(req.DestinationVersion),
		})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}
		w.Header().Set("ETag", quotedETag(result.Version))
		handler.WriteJSON(w, http.StatusOK, fileMutationResponse(result))
	}
}
