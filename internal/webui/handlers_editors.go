package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/editor"
)

// EditorInfo is the JSON representation of an editor for the API response.
type EditorInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	IconName    string `json:"icon_name"`
	Detected    bool   `json:"detected"`
}

// EditorsListResponse is the response envelope for GET /api/editors.
type EditorsListResponse struct {
	Success bool             `json:"success"`
	Data    *EditorsListData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// EditorsListData is the payload for the editors list response.
type EditorsListData struct {
	Editors []EditorInfo `json:"editors"`
}

// EditorOpenResponse is the response envelope for POST /api/editors/open.
type EditorOpenResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// EditorOpenRequest is the JSON body for POST /api/editors/open.
type EditorOpenRequest struct {
	EditorID string `json:"editor_id"`
	Path     string `json:"path"`
}

// editorDetectFunc probes installed editors and returns the detected set.
type editorDetectFunc func() []editor.DetectedEditor

// editorLaunchFunc launches an editor with the given targets.
type editorLaunchFunc func(editor.DetectedEditor, []string) error

// editorCache holds cached detection results with TTL.
type editorCache struct {
	mu       sync.Mutex
	detect   editorDetectFunc
	editors  []editor.DetectedEditor
	cachedAt time.Time
	ttl      time.Duration
}

const editorCacheTTL = 30 * time.Second

func newEditorCache(ttl time.Duration, detect editorDetectFunc) *editorCache {
	return &editorCache{ttl: ttl, detect: detect}
}

func newDefaultEditorCache() *editorCache {
	return newEditorCache(editorCacheTTL, editor.DetectedEditors)
}

func (c *editorCache) get() []editor.DetectedEditor {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.cachedAt) > c.ttl {
		c.editors = c.detect()
		c.cachedAt = time.Now()
	}
	return c.editors
}

// handleListEditors returns a handler for GET /api/editors.
// It returns all editors from the registry with detection status.
func handleListEditors(cache *editorCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detected := cache.get()

		// Build a set of detected editor IDs for fast lookup.
		detectedSet := make(map[string]bool, len(detected))
		for _, de := range detected {
			detectedSet[de.ID] = true
		}

		infos := make([]EditorInfo, 0, len(editor.Registry))
		for _, e := range editor.Registry {
			infos = append(infos, EditorInfo{
				ID:          e.ID,
				DisplayName: e.DisplayName,
				IconName:    e.IconName,
				Detected:    detectedSet[e.ID],
			})
		}

		respondJSON(w, http.StatusOK, EditorsListResponse{
			Success: true,
			Data:    &EditorsListData{Editors: infos},
		})
	}
}

// validEditorID matches editor IDs (lowercase alphanumeric + hyphens).
var validEditorID = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateEditorPath checks that the path is absolute, contains no ".." components,
// and exists on disk. Returns the cleaned path and an error message with HTTP status.
func validateEditorPath(rawPath string) (string, int, string) {
	if rawPath == "" {
		return "", http.StatusBadRequest, "path is required"
	}
	if !filepath.IsAbs(rawPath) {
		return "", http.StatusBadRequest, "path must be absolute"
	}
	// Reject ".." components in raw path before cleaning (Clean resolves them away).
	for _, part := range strings.Split(filepath.ToSlash(rawPath), "/") {
		if part == ".." {
			return "", http.StatusUnprocessableEntity, "path must not contain '..' components"
		}
	}
	cleanPath := filepath.Clean(rawPath)
	if _, err := os.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			return "", http.StatusNotFound, "path does not exist"
		}
		return "", http.StatusInternalServerError, "failed to verify path"
	}
	return cleanPath, 0, ""
}

// findDetectedEditor looks up an editor by ID in the detected set.
// Returns the editor if found, or an HTTP status and message if not.
func findDetectedEditor(editorID string, detected []editor.DetectedEditor) (*editor.DetectedEditor, int, string) {
	for i := range detected {
		if detected[i].ID == editorID {
			return &detected[i], 0, ""
		}
	}
	// Distinguish "unknown editor" (404) from "not detected" (422).
	for _, e := range editor.Registry {
		if e.ID == editorID {
			return nil, http.StatusUnprocessableEntity, "editor not detected on this system: " + editorID
		}
	}
	return nil, http.StatusNotFound, "unknown editor: " + editorID
}

// handleOpenEditor returns a handler for POST /api/editors/open.
// It validates the request, looks up the editor, and launches it.
func handleOpenEditor(cache *editorCache, launch editorLaunchFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req EditorOpenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			respondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if req.EditorID == "" {
			respondError(w, http.StatusBadRequest, "editor_id is required")
			return
		}
		if !validEditorID.MatchString(req.EditorID) {
			respondError(w, http.StatusBadRequest, "invalid editor_id format")
			return
		}

		cleanPath, status, msg := validateEditorPath(req.Path)
		if msg != "" {
			respondError(w, status, msg)
			return
		}

		found, status, msg := findDetectedEditor(req.EditorID, cache.get())
		if msg != "" {
			respondError(w, status, msg)
			return
		}

		if err := launch(*found, []string{cleanPath}); err != nil {
			respondError(w, http.StatusInternalServerError, "failed to launch editor: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, EditorOpenResponse{Success: true})
	}
}
