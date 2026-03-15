package webui

import (
	"encoding/json"
	"errors"
	"net/http"
)

// workspaceOrderRequest is the JSON body for PUT /api/workspace/order.
type workspaceOrderRequest struct {
	Order []string `json:"order"`
}

// handleWorkspaceReorder returns a handler that persists a custom workspace display order.
// It validates that names exist in the config, filters out unknown names, and saves the order.
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

		// Validate: only keep names that exist in cfg.Workspaces
		validOrder := make([]string, 0, len(req.Order))
		for _, name := range req.Order {
			if _, ok := cfg.Workspaces[name]; ok {
				validOrder = append(validOrder, name)
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
