package webui

import (
	"encoding/json"
	"errors"
	"net/http"
)

// workspaceOrderRequest is the JSON body for PUT /api/workspaces/order.
type workspaceOrderRequest struct {
	Order []string `json:"order"`
}

// handleWorkspaceReorder returns a handler that persists a custom workspace display order.
// It accepts both workspace names and UUIDs, resolves UUIDs to names, filters unknown entries,
// deduplicates, and saves the order.
func handleWorkspaceReorder(workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req workspaceOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		cfg, err := loadLoomConfig()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to load config"})
			return
		}
		if cfg == nil {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: "no config found"})
			return
		}

		// Validate: resolve UUIDs to names and filter unknown entries.
		// Deduplicate to prevent double-entries when both UUID and name appear.
		validOrder := make([]string, 0, len(req.Order))
		seen := make(map[string]bool, len(req.Order))
		for _, entry := range req.Order {
			var name string
			if _, ok := cfg.Workspaces[entry]; ok {
				// Direct name match
				name = entry
			} else if resolved, _, found := resolveWorkspaceNameByID(cfg, entry); found {
				// UUID resolved to workspace name
				name = resolved
			}
			if name != "" && !seen[name] {
				validOrder = append(validOrder, name)
				seen[name] = true
			}
		}
		cfg.WorkspaceOrder = validOrder

		if err := saveLoomConfig(cfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to save config"})
			return
		}

		// Return refreshed workspace data
		if workspaceConfigFn != nil {
			data, err := workspaceConfigFn()
			if err == nil && data != nil {
				normalizeWorkspaceData(data)
			}
			respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
			return
		}

		respondJSON(w, http.StatusOK, workspaceResponse{Success: true})
	}
}
