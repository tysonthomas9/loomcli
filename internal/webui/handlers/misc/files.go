package misc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// IsBinaryContent checks if data is likely binary (non-UTF-8 or contains null bytes).
func IsBinaryContent(data []byte) bool {
	return !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0
}

// FileWriteError represents a categorized error from file write validation/execution.
type FileWriteError struct {
	Status  int
	Message string
}

// ValidateParentDir checks that the parent directory exists, is not a symlink, and is within the worktree.
func ValidateParentDir(fullPath, worktreeRoot string) *FileWriteError {
	parentDir := filepath.Dir(fullPath)
	parentFi, err := os.Lstat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &FileWriteError{http.StatusNotFound, "parent directory does not exist"}
		}
		return &FileWriteError{http.StatusInternalServerError, "failed to stat parent directory"}
	}
	if parentFi.Mode()&os.ModeSymlink != 0 {
		return &FileWriteError{http.StatusForbidden, "parent directory is a symlink"}
	}
	if !parentFi.IsDir() {
		return &FileWriteError{http.StatusBadRequest, "parent path is not a directory"}
	}
	if err := validatePathWithinDir(parentDir, worktreeRoot); err != nil {
		return &FileWriteError{http.StatusForbidden, "parent directory outside worktree"}
	}
	return nil
}

// ResolveWritePermissions determines the file permissions to use.
func ResolveWritePermissions(fullPath string) (os.FileMode, *FileWriteError) {
	existingFi, err := os.Lstat(fullPath)
	if err != nil {
		return 0644, nil // New file: default permissions
	}
	if existingFi.Mode()&os.ModeSymlink != 0 {
		return 0, &FileWriteError{http.StatusForbidden, "refusing to overwrite symlink"}
	}
	return existingFi.Mode().Perm(), nil
}

// AtomicWriteFile writes content to fullPath atomically via temp file + rename.
func AtomicWriteFile(fullPath, content string, perm os.FileMode) error {
	parentDir := filepath.Dir(fullPath)
	tmpFile, err := os.CreateTemp(parentDir, ".loom-write-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmpFile.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	success = true
	return nil
}

// HandleFileTree handles GET /api/agents/{name}/files/tree?path=
func HandleFileTree(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ListDirectoryScoped(r.Context(), wsID, service.ScopeAgent, agentName, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleFileRead handles GET /api/agents/{name}/files?path=
func HandleFileRead(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ReadFileScoped(r.Context(), wsID, service.ScopeAgent, agentName, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// scopeFromQuery parses the scope/target query params for the scoped file
// browser, defaulting to the workspace scope when scope is omitted.
func scopeFromQuery(r *http.Request) (service.FileScope, string) {
	scope := service.FileScope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = service.ScopeWorkspace
	}
	return scope, r.URL.Query().Get("target")
}

// HandleScopedFileTree handles GET /api/workspaces/{ws}/files/tree?scope=&target=&path=.
func HandleScopedFileTree(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ListDirectoryScoped(r.Context(), wsID, scope, target, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileRead handles GET /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileRead(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		rev := r.URL.Query().Get("rev")

		var result *service.FileReadResult
		var err error
		if strings.TrimSpace(rev) != "" {
			result, err = svc.ReadFileAtRevScoped(r.Context(), wsID, scope, target, reqPath, rev)
		} else {
			result, err = svc.ReadFileScoped(r.Context(), wsID, scope, target, reqPath)
		}
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileDiff handles GET /api/workspaces/{ws}/files/diff?scope=&target=&path=&from=&to=.
func HandleScopedFileDiff(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		result, err := svc.DiffFileScoped(r.Context(), wsID, scope, target, reqPath, from, to)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileHistory handles GET /api/workspaces/{ws}/files/history?scope=&target=&path=.
func HandleScopedFileHistory(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.HistoryFileScoped(r.Context(), wsID, scope, target, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileBlame handles GET /api/workspaces/{ws}/files/blame?scope=&target=&path=.
func HandleScopedFileBlame(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.BlameFileScoped(r.Context(), wsID, scope, target, reqPath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileIndex handles GET /api/workspaces/{ws}/files/index?scope=&target=.
func HandleScopedFileIndex(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)

		result, err := svc.IndexFilesScoped(r.Context(), wsID, scope, target)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedFileSearch handles POST /api/workspaces/{ws}/files/search?scope=&target=.
func HandleScopedFileSearch(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)

		var req service.FileSearchRequest
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

		result, err := svc.SearchFilesScoped(r.Context(), wsID, scope, target, req)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleScopedGitStatus handles GET /api/workspaces/{ws}/files/git-status?scope=&target=.
func HandleScopedGitStatus(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)

		result, err := svc.GitStatusScoped(r.Context(), wsID, scope, target)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// fileWriteRequest is the JSON body for PUT /api/agents/{name}/files?path=
type fileWriteRequest struct {
	Content string `json:"content"`
}

// HandleFileWrite handles PUT /api/agents/{name}/files?path= as a deprecated
// delegate to the scoped agent file endpoint.
func HandleFileWrite(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())
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

		if err := svc.WriteFileScoped(r.Context(), wsID, service.ScopeAgent, agentName, reqPath, req.Content); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileWrite handles PUT /api/workspaces/{ws}/files?scope=&target=&path=.
func HandleScopedFileWrite(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
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

		if err := svc.WriteFileScoped(r.Context(), wsID, scope, target, reqPath, req.Content); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileDelete handles DELETE /api/workspaces/{ws}/files?scope=&target=&path=&recursive=1.
func HandleScopedFileDelete(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")
		recursive := r.URL.Query().Get("recursive") == "1" || r.URL.Query().Get("recursive") == "true"

		if err := svc.DeletePathScoped(r.Context(), wsID, scope, target, reqPath, recursive); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMkdir handles POST /api/workspaces/{ws}/files/mkdir?scope=&target=&path=.
func HandleScopedFileMkdir(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		if err := svc.MkdirScoped(r.Context(), wsID, scope, target, reqPath); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleScopedFileMove handles PATCH /api/workspaces/{ws}/files/move?scope=&target=.
func HandleScopedFileMove(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)

		var req service.FileMoveRequest
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

		if err := svc.MovePathScoped(r.Context(), wsID, scope, target, req.From, req.To, req.Overwrite); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}
