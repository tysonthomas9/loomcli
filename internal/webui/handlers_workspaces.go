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

		// Build workspace metadata maps from config if available.
		// Two maps allow enrichment whether MultiPool is keyed by name (pre-T2) or UUID (post-T2).
		var wsMetaByName map[string]WorkspaceSummary
		var wsMetaByID map[string]WorkspaceSummary
		if configFn != nil {
			if data, err := configFn(); err == nil && data != nil {
				wsMetaByName = make(map[string]WorkspaceSummary, len(data.Workspaces))
				wsMetaByID = make(map[string]WorkspaceSummary, len(data.Workspaces))
				for _, ws := range data.Workspaces {
					wsMetaByName[ws.Name] = ws
					if ws.ID != "" {
						wsMetaByID[ws.ID] = ws
					}
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
			// Try UUID-keyed lookup first (post-T2), then fall back to name-keyed (pre-T2).
			if meta, ok := wsMetaByID[id]; ok {
				item.Name = meta.Name
				item.ID = meta.ID
				item.Path = meta.Path
				item.Active = meta.Active
			} else if meta, ok := wsMetaByName[id]; ok {
				if meta.ID != "" {
					item.ID = meta.ID
				}
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
		// Match by ID (UUID) or Name to handle both pre-T2 and post-T2 pool keys.
		if configFn != nil {
			if data, err := configFn(); err == nil && data != nil {
				for _, ws := range data.Workspaces {
					if ws.ID == wsID || ws.Name == wsID {
						item.Name = ws.Name
						if ws.ID != "" {
							item.ID = ws.ID
						}
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
