package metricscmd

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// MonitorStoreDataSource resolves store-backed monitor metadata for a
// workspace. Status and agents endpoints share it so adjacent poll requests
// do not each re-read workspace metadata and agent assignments.
type MonitorStoreDataSource struct {
	st      store.Store
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]*monitorStoreCacheEntry
}

type monitorStoreData struct {
	Workspace WorkspaceInfo
	Agents    []monitor.AgentStatus
}

type agentInboxSummary struct {
	QueuedCount   int
	FailedCount   int
	LatestMessage string
	LatestCursor  int64
}

type monitorStoreCacheEntry struct {
	mu       sync.Mutex
	cachedAt time.Time
	data     monitorStoreData
}

// NewMonitorStoreDataSource returns a cache for store-backed monitor data.
func NewMonitorStoreDataSource(st store.Store) *MonitorStoreDataSource {
	return NewMonitorStoreDataSourceWithTTL(st, defaultWorkspaceMonitorCacheTTL)
}

// NewMonitorStoreDataSourceWithTTL returns a store data source with a
// configurable TTL for tests.
func NewMonitorStoreDataSourceWithTTL(st store.Store, ttl time.Duration) *MonitorStoreDataSource {
	if ttl <= 0 {
		ttl = defaultWorkspaceMonitorCacheTTL
	}
	return &MonitorStoreDataSource{
		st:      st,
		ttl:     ttl,
		entries: make(map[string]*monitorStoreCacheEntry),
	}
}

// Resolve returns cached store-backed monitor metadata for workspaceHint.
func (s *MonitorStoreDataSource) Resolve(ctx context.Context, workspaceHint string) monitorStoreData {
	if s == nil || s.st == nil {
		return emptyMonitorStoreData()
	}

	entry := s.cacheEntry(monitorStoreCacheKey(workspaceHint))
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	if !entry.cachedAt.IsZero() && now.Sub(entry.cachedAt) < s.ttl {
		return entry.data
	}

	data := collectMonitorStoreData(ctx, s.st, workspaceHint)
	entry.data = data
	entry.cachedAt = now
	return data
}

func (s *MonitorStoreDataSource) cacheEntry(cacheKey string) *monitorStoreCacheEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.entries[cacheKey]
	if entry != nil {
		return entry
	}
	if len(s.entries) >= maxWorkspaceMonitorCollectors {
		return &monitorStoreCacheEntry{}
	}
	entry = &monitorStoreCacheEntry{}
	s.entries[cacheKey] = entry
	return entry
}

func monitorStoreCacheKey(workspaceHint string) string {
	if workspaceHint != "" {
		return workspaceHint
	}
	return "__active__"
}

func emptyMonitorStoreData() monitorStoreData {
	return monitorStoreData{
		Workspace: WorkspaceInfo{Mode: "workspace"},
		Agents:    []monitor.AgentStatus{},
	}
}

func collectMonitorStoreData(ctx context.Context, st store.Store, workspaceHint string) monitorStoreData {
	data := emptyMonitorStoreData()
	if st == nil {
		return data
	}

	workspaces, err := st.Workspaces().List(ctx)
	if err == nil {
		data.Workspace.Workspaces = workspaceNames(workspaces)
	}

	wsKey, wsName, ok := resolveMonitorWorkspaceFromList(ctx, st, workspaceHint, workspaces)
	if !ok {
		return data
	}
	data.Workspace.Name = wsName

	assignments, err := st.Agents().List(ctx, wsKey)
	if err != nil {
		slog.Warn("monitor: list store agents failed", "workspace", wsKey, "err", err)
		return data
	}

	workspaceData := monitorWorkspaceDataForAgents(ctx, st, wsKey, wsName)
	latestSessions := latestAgentSessionsForMonitor(ctx, st, wsKey)
	orchestrationByAgent := latestOrchestrationSessionsForMonitor(ctx, st, wsKey)
	inboxByAgent := agentInboxSummariesForMonitor(ctx, st, wsKey)
	data.Agents = monitorAgentStatuses(assignments, workspaceData, latestSessions, orchestrationByAgent, inboxByAgent, wsName)
	return data
}

func monitorAgentStatuses(
	assignments []*domain.Agent,
	workspaceData *ops.WorkspaceData,
	latestSessions map[string]*domain.AgentSession,
	orchestrationByAgent map[string]*domain.AgentSession,
	inboxByAgent map[string]agentInboxSummary,
	wsName string,
) []monitor.AgentStatus {
	agents := []monitor.AgentStatus{}
	for _, assignment := range assignments {
		if assignment == nil {
			continue
		}
		var taskID, sessionID string
		if session := latestSessions[assignment.Name]; session != nil {
			taskID = session.TaskID
			sessionID = session.SessionID
		}
		var orchID string
		if sess := orchestrationByAgent[assignment.Name]; sess != nil {
			orchID = sess.SessionID
		}
		inboxSummary := inboxByAgent[assignment.Name]
		agents = append(agents, monitor.AgentStatus{
			Name:                  assignment.Name,
			Branch:                monitorBranchFromAgent(workspaceData, assignment),
			Status:                monitorStatusFromAgentState(assignment.State),
			Role:                  assignment.RoleName,
			Repo:                  monitorRepoFromAgent(assignment),
			Workspace:             wsName,
			DaemonManaged:         assignment.Auto,
			Parent:                assignment.Parent,
			DeliveryState:         monitorLeadDeliveryState(assignment, orchestrationByAgent[assignment.Name]),
			InboxQueuedCount:      inboxSummary.QueuedCount,
			InboxFailedCount:      inboxSummary.FailedCount,
			InboxLatestMessage:    inboxSummary.LatestMessage,
			OrchestratorSessionID: orchID,
			TaskID:                taskID,
			SessionID:             sessionID,
			Mode:                  string(assignment.Mode),
			DesiredState:          string(assignment.DesiredState),
		})
	}
	return agents
}

func agentInboxSummariesForMonitor(ctx context.Context, st store.Store, wsKey string) map[string]agentInboxSummary {
	out := make(map[string]agentInboxSummary)
	if st == nil || st.AgentInboxMessages() == nil || wsKey == "" {
		return out
	}
	for _, status := range []domain.AgentInboxMessageStatus{domain.AgentInboxMessageQueued, domain.AgentInboxMessageFailed} {
		items, err := st.AgentInboxMessages().List(ctx, wsKey, store.AgentInboxMessageFilter{Status: status, Limit: 10000})
		if err != nil {
			slog.Warn("monitor: list agent inbox messages failed", "workspace", wsKey, "status", status, "err", err)
			continue
		}
		for _, item := range items {
			if item == nil || item.TargetAgentID == "" {
				continue
			}
			summary := out[item.TargetAgentID]
			if status == domain.AgentInboxMessageQueued {
				summary.QueuedCount++
			} else if status == domain.AgentInboxMessageFailed {
				summary.FailedCount++
			}
			if item.Cursor >= summary.LatestCursor {
				summary.LatestCursor = item.Cursor
				summary.LatestMessage = item.Body
			}
			out[item.TargetAgentID] = summary
		}
	}
	return out
}

func monitorLeadDeliveryState(agent *domain.Agent, session *domain.AgentSession) string {
	if agent == nil || !monitorIsLeadRole(agent.RoleName) || strings.TrimSpace(agent.Parent) == "" {
		return ""
	}
	version := monitorLeadAssignmentVersion(agent)
	if version == "" {
		return "pending"
	}
	if monitorSessionMetadataVersionMatches(session, "lead_assignment_acknowledged_version", version) {
		return "acknowledged"
	}
	if monitorSessionMetadataVersionMatches(session, "lead_assignment_delivered_version", version) {
		return "delivered"
	}
	return "pending"
}

func monitorIsLeadRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}

func monitorLeadAssignmentVersion(agent *domain.Agent) string {
	if agent == nil {
		return ""
	}
	if !agent.UpdatedAt.IsZero() {
		return agent.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.TrimSpace(agent.Parent)
}

func monitorSessionMetadataVersionMatches(session *domain.AgentSession, key, version string) bool {
	if session == nil || session.Metadata == nil || version == "" {
		return false
	}
	return strings.TrimSpace(session.Metadata[key]) == version
}

func workspaceNames(workspaces []*domain.Workspace) []string {
	names := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws != nil {
			names = append(names, ws.Name)
		}
	}
	return names
}

func resolveMonitorWorkspaceFromList(ctx context.Context, st store.Store, workspaceHint string, workspaces []*domain.Workspace) (key string, name string, ok bool) {
	if workspaceHint != "" {
		if key, name, ok := findMonitorWorkspace(workspaces, workspaceHint, true); ok {
			return key, name, true
		}
		return resolveMonitorWorkspace(ctx, st, workspaceHint)
	}

	envWorkspace := os.Getenv(bootstrap.EnvWorkspace)
	if envWorkspace == "" {
		return "", "", false
	}
	if key, name, ok := findMonitorWorkspace(workspaces, envWorkspace, false); ok {
		return key, name, true
	}
	return resolveMonitorWorkspace(ctx, st, workspaceHint)
}

func findMonitorWorkspace(workspaces []*domain.Workspace, hint string, allowName bool) (key string, name string, ok bool) {
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		if ws.Key == hint || (allowName && ws.Name == hint) {
			return ws.Key, ws.Name, true
		}
	}
	return "", "", false
}

func monitorWorkspaceDataForAgents(ctx context.Context, st store.Store, wsKey string, wsName string) *ops.WorkspaceData {
	if st == nil {
		return nil
	}
	repos, err := st.Repos().List(ctx, wsKey)
	if err != nil {
		slog.Warn("monitor: list store repos failed", "workspace", wsKey, "err", err)
		return nil
	}

	out := make([]ops.WorkspaceRepo, 0, len(repos))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		out = append(out, ops.WorkspaceRepo{
			Name:   repo.Name,
			Groups: repo.Groups,
		})
	}

	return &ops.WorkspaceData{
		ID:    wsKey,
		Name:  wsName,
		Path:  storeadapter.ResolveWorkspacePath(wsKey),
		Repos: out,
	}
}
