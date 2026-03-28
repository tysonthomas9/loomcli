package webui

import (
	"encoding/json"
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// GetActiveSourceRepos returns deduplicated source repos across connected
// clients that have a repo filter. Returns nil when no client has a filter.
func (h *SSEHub) GetActiveSourceRepos() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[string]struct{})
	for c := range h.clients {
		for _, r := range c.sourceRepos {
			seen[r] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	return out
}

// emitPerRepoRefreshes checks which watched repos have changes and emits
// per-repo refresh events. Falls back to a global refresh when no clients
// have repo filters or when per-repo Count RPCs fail.
func (s *DaemonSubscriber) emitPerRepoRefreshes(client *rpc.Client, now time.Time, lastPollTime time.Time, totalCount int64) {
	ts := now.UTC().Format(time.RFC3339)
	globalRefresh := &MutationPayload{
		Type: rpc.MutationRefresh, Timestamp: ts, WorkspaceID: s.workspaceID,
	}

	activeRepos := s.hub.GetActiveSourceRepos()
	if len(activeRepos) == 0 {
		s.hub.Broadcast(globalRefresh)
		log.Printf("External DB change detected, broadcast global refresh to %d SSE clients", s.hub.ClientCount())
		return
	}

	updatedAfter := lastPollTime.UTC().Format(time.RFC3339)
	anyPerRepo := false
	for _, repo := range activeRepos {
		resp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter, SourceRepos: []string{repo}})
		if err != nil {
			log.Printf("External poll: per-repo count error for %q: %v, falling back to global refresh", repo, err)
			s.hub.Broadcast(globalRefresh)
			return
		}
		if !resp.Success {
			log.Printf("External poll: per-repo count not success for %q, falling back to global refresh", repo)
			s.hub.Broadcast(globalRefresh)
			return
		}
		var countResult struct {
			Count int64 `json:"count"`
		}
		if err := json.Unmarshal(resp.Data, &countResult); err != nil {
			log.Printf("External poll: per-repo count parse error for %q: %v, falling back to global refresh", repo, err)
			s.hub.Broadcast(globalRefresh)
			return
		}
		if countResult.Count > 0 {
			s.hub.Broadcast(&MutationPayload{
				Type: rpc.MutationRefresh, SourceRepo: repo, Timestamp: ts, WorkspaceID: s.workspaceID,
			})
			anyPerRepo = true
			log.Printf("External DB change detected in repo %q, broadcast per-repo refresh", repo)
		}
	}

	// If count changed but no per-repo updates found (unwatched repo), emit global refresh.
	s.mu.RLock()
	lastKnown := s.lastKnownCount
	s.mu.RUnlock()
	if !anyPerRepo && totalCount != lastKnown {
		s.hub.Broadcast(globalRefresh)
		log.Printf("External DB change in unwatched repo, broadcast global refresh to %d SSE clients", s.hub.ClientCount())
	}
}
