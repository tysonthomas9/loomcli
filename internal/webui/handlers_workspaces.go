package webui

import (
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// workspaceListItem represents a single workspace in the list response.
type workspaceListItem struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Path   string            `json:"path"`
	Active bool              `json:"active"`
	Pool   *daemon.PoolStats `json:"pool,omitempty"`
}

// handleListWorkspaces returns GET /api/workspaces — a list of all registered
// workspaces with basic status information.
func handleListWorkspaces(mp *daemon.MultiPool, configFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get registered workspace IDs from the MultiPool.
		ids := mp.WorkspaceIDs()

		// Build workspace metadata map from config if available.
		var wsMeta map[string]WorkspaceSummary
		if configFn != nil {
			if data, err := configFn(); err == nil && data != nil {
				wsMeta = make(map[string]WorkspaceSummary, len(data.Workspaces))
				for _, ws := range data.Workspaces {
					wsMeta[ws.Name] = ws
				}
			}
		}

		items := make([]workspaceListItem, 0, len(ids))
		for _, id := range ids {
			item := workspaceListItem{
				ID:   id,
				Name: id,
			}

			// Enrich with config metadata if available.
			if meta, ok := wsMeta[id]; ok {
				item.Path = meta.Path
				item.Active = meta.Active
			}

			// Include pool stats.
			if p := mp.PoolForWorkspace(id); p != nil {
				stats := p.Stats()
				item.Pool = &stats
			}

			items = append(items, item)
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"workspaces": items,
		})
	}
}

// handleGetWorkspace returns GET /api/workspaces/{ws} — details for a single
// workspace including its pool stats and config metadata.
func handleGetWorkspace(mp *daemon.MultiPool, configFn func() (*WorkspaceData, error), wsExistsFn func(string) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}

		// Validate workspace existence via the injected function.
		if !wsExistsFn(wsID) {
			respondError(w, http.StatusNotFound, "workspace not found: "+wsID)
			return
		}

		// Get pool for stats (may still be nil if pool was deregistered between check and here).
		p := mp.PoolForWorkspace(wsID)

		item := workspaceListItem{
			ID:   wsID,
			Name: wsID,
		}

		if p != nil {
			stats := p.Stats()
			item.Pool = &stats
		}

		// Enrich with config metadata if available.
		if configFn != nil {
			if data, err := configFn(); err == nil && data != nil {
				for _, ws := range data.Workspaces {
					if ws.Name == wsID {
						item.Path = ws.Path
						item.Active = ws.Active
						break
					}
				}
			}
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":   true,
			"workspace": item,
		})
	}
}
