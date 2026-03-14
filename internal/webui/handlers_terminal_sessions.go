package webui

import (
	"log"
	"net/http"
	"strings"
)

// terminalSessionInfo describes a terminal session visible to the frontend.
type terminalSessionInfo struct {
	Name    string `json:"name"`    // user-facing name, e.g. "talk-to-lead"
	Label   string `json:"label"`   // display label (same as name for now)
	Created int64  `json:"created"` // Unix timestamp, 0 if session not yet created
}

type terminalSessionsResponse struct {
	Success bool                  `json:"success"`
	Data    *terminalSessionsData `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

type terminalSessionsData struct {
	Sessions []terminalSessionInfo `json:"sessions"`
}

// ListActiveSessions returns sessions owned by this server instance.
// It filters tmux sessions by the manager's sessionPrefix and always
// includes "talk-to-lead" as the default session.
func (m *TerminalManager) ListActiveSessions() ([]terminalSessionInfo, error) {
	m.mu.RLock()
	sessionPrefix := m.sessionPrefix
	m.mu.RUnlock()

	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := sessionPrefix + "-"
	hasTalkToLead := false
	var result []terminalSessionInfo

	for _, s := range allSessions {
		var name string
		if sessionPrefix == "" {
			name = s.name
		} else if strings.HasPrefix(s.name, prefix) {
			name = strings.TrimPrefix(s.name, prefix)
		} else {
			continue
		}

		result = append(result, terminalSessionInfo{
			Name:    name,
			Label:   name,
			Created: s.created,
		})
		if name == "talk-to-lead" {
			hasTalkToLead = true
		}
	}

	if !hasTalkToLead {
		result = append([]terminalSessionInfo{{
			Name:    "talk-to-lead",
			Label:   "talk-to-lead",
			Created: 0,
		}}, result...)
	}

	return result, nil
}

func handleListTerminalSessions(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, terminalSessionsResponse{
				Success: false,
				Error:   "terminal manager not initialized",
			})
			return
		}

		sessions, err := manager.ListActiveSessions()
		if err != nil {
			log.Printf("Failed to list terminal sessions: %v", err)
			respondJSON(w, http.StatusInternalServerError, terminalSessionsResponse{
				Success: false,
				Error:   "failed to list terminal sessions",
			})
			return
		}

		respondJSON(w, http.StatusOK, terminalSessionsResponse{
			Success: true,
			Data: &terminalSessionsData{
				Sessions: sessions,
			},
		})
	}
}
