package subscription

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// emitPerRepoRefreshes checks which watched repos have changes and emits
// per-repo refresh events. Falls back to a global refresh when no clients
// have repo filters or when per-repo Count RPCs fail.
//
// Returns clientHealthy: false only when a Count RPC hit a transport error
// (resp == nil && err != nil). In that case the loop stops, a global refresh
// is broadcast, and the caller must Discard the connection. Success=false
// and parse errors on fully-received bodies keep clientHealthy = true
// (ref: loomcli-67meg).
func (s *DaemonSubscriber) emitPerRepoRefreshes(client *rpc.Client, now time.Time, lastPollTime time.Time, totalCount int64) bool {
	ts := now.UTC().Format(time.RFC3339)
	globalRefresh := &realtime.MutationPayload{
		Type: rpc.MutationRefresh, Timestamp: ts, WorkspaceID: s.workspaceID,
	}

	activeRepos := s.hub.GetActiveSourceRepos()
	if len(activeRepos) == 0 {
		s.hub.Broadcast(globalRefresh)
		slog.Info("external DB change detected, broadcast global refresh", "clients", s.hub.ClientCount())
		return true
	}

	updatedAfter := lastPollTime.UTC().Format(time.RFC3339)
	anyPerRepo := false
	for _, repo := range activeRepos {
		changed, healthy, fellBack := s.checkRepoChanged(client, repo, updatedAfter, globalRefresh)
		if !healthy {
			return false
		}
		if fellBack {
			return true
		}
		if changed {
			s.hub.Broadcast(&realtime.MutationPayload{
				Type: rpc.MutationRefresh, SourceRepo: repo, Timestamp: ts, WorkspaceID: s.workspaceID,
			})
			anyPerRepo = true
			slog.Info("external DB change detected, broadcast per-repo refresh", "repo", repo)
		}
	}

	// If count changed but no per-repo updates found (unwatched repo), emit global refresh.
	s.mu.RLock()
	lastKnown := s.lastKnownCount
	s.mu.RUnlock()
	if !anyPerRepo && totalCount != lastKnown {
		s.hub.Broadcast(globalRefresh)
		slog.Info("external DB change in unwatched repo, broadcast global refresh", "clients", s.hub.ClientCount())
	}
	return true
}

// checkRepoChanged runs a per-repo Count(UpdatedAfter) RPC and classifies the result.
// Returns (changed, clientHealthy, fellBackToGlobal):
//   - changed: true iff the repo reported a positive count of updated issues.
//   - clientHealthy: false on transport error (resp == nil && err != nil).
//   - fellBackToGlobal: true iff a logical failure (Success=false or parse error)
//     caused globalRefresh to be broadcast; the caller should stop iterating.
//
// Transport errors also broadcast globalRefresh before returning.
func (s *DaemonSubscriber) checkRepoChanged(client *rpc.Client, repo, updatedAfter string, globalRefresh *realtime.MutationPayload) (changed, clientHealthy, fellBackToGlobal bool) {
	resp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter, SourceRepos: []string{repo}})
	if err != nil {
		if resp == nil {
			slog.Error("external poll: per-repo count transport error, falling back to global refresh", "repo", repo, "err", err)
			s.hub.Broadcast(globalRefresh)
			return false, false, true
		}
		slog.Warn("external poll: per-repo count not success, falling back to global refresh", "repo", repo, "err", err)
		s.hub.Broadcast(globalRefresh)
		return false, true, true
	}
	if !resp.Success {
		slog.Warn("external poll: per-repo count not success, falling back to global refresh", "repo", repo)
		s.hub.Broadcast(globalRefresh)
		return false, true, true
	}
	var countResult struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &countResult); err != nil {
		slog.Error("external poll: per-repo count parse error, falling back to global refresh", "repo", repo, "err", err)
		s.hub.Broadcast(globalRefresh)
		return false, true, true
	}
	return countResult.Count > 0, true, false
}
