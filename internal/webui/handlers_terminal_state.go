package webui

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/redis/go-redis/v9"
)

const terminalUIStateKey = "terminal:ui-state"

// handleGetTerminalState returns the persisted terminal UI state.
// GET /api/workspaces/{ws}/terminal/state
func handleGetTerminalState(client *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vals, err := client.HGetAll(r.Context(), terminalUIStateKey).Result()
		if err != nil {
			log.Printf("Warning: failed to get terminal state: %v", err)
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"active_tab": "",
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": vals["active_tab"],
		})
	}
}

// handlePatchTerminalState updates the persisted terminal UI state.
// PATCH /api/workspaces/{ws}/terminal/state
func handlePatchTerminalState(client *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ActiveTab string `json:"active_tab"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		fields := map[string]interface{}{
			"active_tab": req.ActiveTab,
		}

		if err := client.HSet(r.Context(), terminalUIStateKey, fields).Err(); err != nil {
			log.Printf("Warning: failed to set terminal state: %v", err)
			respondError(w, http.StatusInternalServerError, "failed to save terminal state")
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": req.ActiveTab,
		})
	}
}
