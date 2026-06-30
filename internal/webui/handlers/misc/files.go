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

// deniedExtensions lists file extensions that must not be read or written.
var deniedExtensions = map[string]bool{
	".key": true,
	".pem": true,
	".p12": true,
	".pfx": true,
	".env": true,
	".gpg": true,
	".asc": true,
}

// deniedFilenames lists filenames (without path) that must not be read or written.
var deniedFilenames = map[string]bool{
	"id_rsa":          true,
	"id_ed25519":      true,
	"id_ecdsa":        true,
	"id_dsa":          true,
	".env":            true,
	".env.local":      true,
	".env.production": true,
	".netrc":          true,
}

// isDeniedPath checks if a path refers to a sensitive file by extension or filename.
func isDeniedPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if deniedExtensions[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return deniedFilenames[base]
}

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

		result, err := svc.ListDirectory(r.Context(), wsID, agentName, reqPath)
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

		result, err := svc.ReadFile(r.Context(), wsID, agentName, reqPath)
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

// HandleScopedFileTree handles GET /api/workspaces/{ws}/files/tree?scope=&target=&path=
// — one directory level of the read-only scope-rooted file browser.
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

// HandleScopedFileRead handles GET /api/workspaces/{ws}/files?scope=&target=&path=
// — reads a single file from the read-only scope-rooted file browser.
func HandleScopedFileRead(svc service.FileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		scope, target := scopeFromQuery(r)
		reqPath := r.URL.Query().Get("path")

		result, err := svc.ReadFileScoped(r.Context(), wsID, scope, target, reqPath)
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

// HandleFileWrite handles PUT /api/agents/{name}/files?path=
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

		if err := svc.WriteFile(r.Context(), wsID, agentName, reqPath, req.Content); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}
