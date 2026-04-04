package webui

import (
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// emitPerRepoRefreshes checks which watched repos have changes and emits
// per-repo refresh events. Falls back to a global refresh when no clients
// have repo filters or when per-repo Count RPCs fail.
func (s *DaemonSubscriber) emitPerRepoRefreshes(client *rpc.Client, now time.Time, lastPollTime time.Time, totalCount int64) {
	ts := now.UTC().Format(time.RFC3339)
	globalRefresh := &realtime.MutationPayload{
		Type: rpc.MutationRefresh, Timestamp: ts, WorkspaceID: s.workspaceID,
	}

	activeRepos := s.hub.GetActiveSourceRepos()
	if len(activeRepos) == 0 {
		s.hub.Broadcast(globalRefresh)
		logger.Info("external DB change detected, broadcast global refresh", "clients", s.hub.ClientCount())
		return
	}

	updatedAfter := lastPollTime.UTC().Format(time.RFC3339)
	anyPerRepo := false
	for _, repo := range activeRepos {
		resp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter, SourceRepos: []string{repo}})
		if err != nil {
			logger.Error("external poll: per-repo count error, falling back to global refresh", "repo", repo, "err", err)
			s.hub.Broadcast(globalRefresh)
			return
		}
		if !resp.Success {
			logger.Warn("external poll: per-repo count not success, falling back to global refresh", "repo", repo)
			s.hub.Broadcast(globalRefresh)
			return
		}
		var countResult struct {
			Count int64 `json:"count"`
		}
		if err := json.Unmarshal(resp.Data, &countResult); err != nil {
			logger.Error("external poll: per-repo count parse error, falling back to global refresh", "repo", repo, "err", err)
			s.hub.Broadcast(globalRefresh)
			return
		}
		if countResult.Count > 0 {
			s.hub.Broadcast(&realtime.MutationPayload{
				Type: rpc.MutationRefresh, SourceRepo: repo, Timestamp: ts, WorkspaceID: s.workspaceID,
			})
			anyPerRepo = true
			logger.Info("external DB change detected, broadcast per-repo refresh", "repo", repo)
		}
	}

	// If count changed but no per-repo updates found (unwatched repo), emit global refresh.
	s.mu.RLock()
	lastKnown := s.lastKnownCount
	s.mu.RUnlock()
	if !anyPerRepo && totalCount != lastKnown {
		s.hub.Broadcast(globalRefresh)
		logger.Info("external DB change in unwatched repo, broadcast global refresh", "clients", s.hub.ClientCount())
	}
}
