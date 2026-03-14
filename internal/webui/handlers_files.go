package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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
// Both checks are case-insensitive to prevent bypass via case variation.
func isDeniedPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if deniedExtensions[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return deniedFilenames[base]
}

// isBinaryContent checks if data is likely binary (non-UTF-8 or contains null bytes).
func isBinaryContent(data []byte) bool {
	return !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0
}

// --- Response types ---

type fileTreeEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type fileTreeResult struct {
	Path    string          `json:"path"`
	Entries []fileTreeEntry `json:"entries"`
}

type fileReadResult struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Size    int64  `json:"size"`
	Binary  bool   `json:"binary"`
}

// --- File Tree ---

// handleFileTree handles GET /api/agents/{name}/files/tree?path=
// Lists one level of a directory within an agent's worktree.
func handleFileTree(ops FileOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		reqPath := r.URL.Query().Get("path")
		if reqPath == "" {
			reqPath = "."
		}

		fullPath := filepath.Join(wt.Path, filepath.Clean("/"+reqPath))

		if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
			respondError(w, http.StatusForbidden, "path outside worktree")
			return
		}

		// Lstat to reject symlinks before reading directory
		fi, err := os.Lstat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				respondError(w, http.StatusNotFound, "directory not found")
				return
			}
			respondError(w, http.StatusInternalServerError, "failed to stat path")
			return
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			respondError(w, http.StatusForbidden, "refusing to follow symlink")
			return
		}
		if !fi.IsDir() {
			respondError(w, http.StatusBadRequest, "path is not a directory")
			return
		}

		dirEntries, err := os.ReadDir(fullPath)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to read directory")
			return
		}

		// Sort: directories first, then alphabetically
		sort.Slice(dirEntries, func(i, j int) bool {
			iDir := dirEntries[i].IsDir()
			jDir := dirEntries[j].IsDir()
			if iDir != jDir {
				return iDir
			}
			return dirEntries[i].Name() < dirEntries[j].Name()
		})

		entries := make([]fileTreeEntry, 0, len(dirEntries))
		for _, de := range dirEntries {
			// Skip symlink entries to prevent information disclosure
			if de.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			entries = append(entries, fileTreeEntry{
				Name:    de.Name(),
				IsDir:   de.IsDir(),
				Size:    info.Size(),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
			})
		}

		// Compute relative path for response
		relPath, _ := filepath.Rel(wt.Path, fullPath)
		if relPath == "" {
			relPath = "."
		}

		respondJSON(w, http.StatusOK, fileTreeResult{
			Path:    relPath,
			Entries: entries,
		})
	}
}

// --- File Read ---

// handleFileRead handles GET /api/agents/{name}/files?path=
// Reads a file within an agent's worktree. Binary files return metadata only.
func handleFileRead(ops FileOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		reqPath := r.URL.Query().Get("path")
		if reqPath == "" {
			respondError(w, http.StatusBadRequest, "path parameter is required")
			return
		}

		if isDeniedPath(reqPath) {
			respondError(w, http.StatusForbidden, "access to this file type is denied")
			return
		}

		fullPath := filepath.Join(wt.Path, filepath.Clean("/"+reqPath))

		if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
			respondError(w, http.StatusForbidden, "path outside worktree")
			return
		}

		// Check denied path on resolved path (before opening)
		if isDeniedPath(fullPath) {
			respondError(w, http.StatusForbidden, "access to this file type is denied")
			return
		}

		// Lstat to reject symlinks and directories
		fi, err := os.Lstat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				respondError(w, http.StatusNotFound, "file not found")
				return
			}
			respondError(w, http.StatusInternalServerError, "failed to stat file")
			return
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			respondError(w, http.StatusForbidden, "refusing to follow symlink")
			return
		}
		if fi.IsDir() {
			respondError(w, http.StatusBadRequest, "path is a directory, not a file")
			return
		}

		// Check size before reading
		if fi.Size() > maxRequestBody {
			respondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large: %d bytes (max %d)", fi.Size(), maxRequestBody))
			return
		}

		// Open with O_NOFOLLOW on Unix
		f, err := openLogFileSecure(fullPath, wt.Path)
		if err != nil {
			if strings.Contains(err.Error(), "symlink") {
				respondError(w, http.StatusForbidden, "refusing to follow symlink")
				return
			}
			respondError(w, http.StatusInternalServerError, "failed to open file")
			return
		}
		defer f.Close()

		data, err := io.ReadAll(io.LimitReader(f, maxRequestBody+1))
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to read file")
			return
		}

		if isBinaryContent(data) {
			respondJSON(w, http.StatusOK, fileReadResult{
				Path:   reqPath,
				Size:   fi.Size(),
				Binary: true,
			})
			return
		}

		respondJSON(w, http.StatusOK, fileReadResult{
			Path:    reqPath,
			Content: string(data),
			Size:    fi.Size(),
			Binary:  false,
		})
	}
}

// --- File Write ---

type fileWriteRequest struct {
	Content string `json:"content"`
}

// fileWriteError represents a categorized error from file write validation/execution.
type fileWriteError struct {
	Status  int
	Message string
}

// validateParentDir checks that the parent directory exists, is not a symlink, and is within the worktree.
func validateParentDir(fullPath, worktreeRoot string) *fileWriteError {
	parentDir := filepath.Dir(fullPath)
	parentFi, err := os.Lstat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &fileWriteError{http.StatusNotFound, "parent directory does not exist"}
		}
		return &fileWriteError{http.StatusInternalServerError, "failed to stat parent directory"}
	}
	if parentFi.Mode()&os.ModeSymlink != 0 {
		return &fileWriteError{http.StatusForbidden, "parent directory is a symlink"}
	}
	if !parentFi.IsDir() {
		return &fileWriteError{http.StatusBadRequest, "parent path is not a directory"}
	}
	if err := validatePathWithinDir(parentDir, worktreeRoot); err != nil {
		return &fileWriteError{http.StatusForbidden, "parent directory outside worktree"}
	}
	return nil
}

// resolveWritePermissions determines the file permissions to use. Returns an error if
// the target is a symlink.
func resolveWritePermissions(fullPath string) (os.FileMode, *fileWriteError) {
	existingFi, err := os.Lstat(fullPath)
	if err != nil {
		return 0644, nil // New file: default permissions
	}
	if existingFi.Mode()&os.ModeSymlink != 0 {
		return 0, &fileWriteError{http.StatusForbidden, "refusing to overwrite symlink"}
	}
	return existingFi.Mode().Perm(), nil
}

// atomicWriteFile writes content to fullPath atomically via temp file + rename.
func atomicWriteFile(fullPath, content string, perm os.FileMode) error {
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

// handleFileWrite handles PUT /api/agents/{name}/files?path=
// Writes content to a file within an agent's worktree using atomic temp+rename.
func handleFileWrite(ops FileOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		reqPath := r.URL.Query().Get("path")
		if reqPath == "" {
			respondError(w, http.StatusBadRequest, "path parameter is required")
			return
		}

		if isDeniedPath(reqPath) {
			respondError(w, http.StatusForbidden, "access to this file type is denied")
			return
		}

		// Parse request body with size limit
		var req fileWriteRequest
		if r.Body == nil {
			respondError(w, http.StatusBadRequest, "request body is required")
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		fullPath := filepath.Join(wt.Path, filepath.Clean("/"+reqPath))

		if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
			respondError(w, http.StatusForbidden, "path outside worktree")
			return
		}
		if isDeniedPath(fullPath) {
			respondError(w, http.StatusForbidden, "access to this file type is denied")
			return
		}

		if writeErr := validateParentDir(fullPath, wt.Path); writeErr != nil {
			respondError(w, writeErr.Status, writeErr.Message)
			return
		}

		perm, writeErr := resolveWritePermissions(fullPath)
		if writeErr != nil {
			respondError(w, writeErr.Status, writeErr.Message)
			return
		}

		if err := atomicWriteFile(fullPath, req.Content, perm); err != nil {
			respondError(w, http.StatusInternalServerError, "failed to save file")
			return
		}

		respondJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}
