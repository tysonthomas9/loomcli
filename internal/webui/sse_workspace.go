package webui

// ClientCountForWorkspace returns the number of SSE clients for a workspace.
func (h *SSEHub) ClientCountForWorkspace(wsID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		if c.workspaceID == wsID {
			n++
		}
	}
	return n
}
