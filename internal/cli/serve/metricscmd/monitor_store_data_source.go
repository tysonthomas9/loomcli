package metricscmd

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// MonitorStoreDataSource resolves store-backed monitor metadata for a
// workspace. Status and agents endpoints share it so adjacent poll requests
// do not each re-read workspace metadata and agent assignments.
type MonitorStoreDataSource struct {
	st      MonitorProjectionSources
	agents  MonitorAgentDirectory
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]*monitorStoreCacheEntry
}

// MonitorProjectionSources is the read-only owner-record seam used to build
// the immutable workspace topology projection.
type MonitorProjectionSources interface {
	Workspaces() workspacemodule.WorkspaceStore
	Repos() workspacemodule.RepoStore
	AgentSessions() interaction.AgentSessionStore
	AgentInboxMessages() interaction.AgentInboxMessageStore
}

// MonitorAgentDirectory is the canonical identity and Role read surface used
// by monitor endpoints. Interaction session evidence is joined by AgentID.
type MonitorAgentDirectory interface {
	agentsmodule.IdentityQueries
	agentsmodule.RoleQueries
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
func NewMonitorStoreDataSource(st MonitorProjectionSources) *MonitorStoreDataSource {
	return NewMonitorStoreDataSourceWithTTL(st, defaultWorkspaceMonitorCacheTTL)
}

// NewMonitorStoreDataSourceWithTTL returns a store data source with a
// configurable TTL for tests.
func NewMonitorStoreDataSourceWithTTL(st MonitorProjectionSources, ttl time.Duration) *MonitorStoreDataSource {
	if ttl <= 0 {
		ttl = defaultWorkspaceMonitorCacheTTL
	}
	return &MonitorStoreDataSource{
		st:      st,
		ttl:     ttl,
		entries: make(map[string]*monitorStoreCacheEntry),
	}
}

// SetAgentDirectory completes startup composition before the HTTP listener is
// exposed and drops any pre-composition empty-roster cache entry.
func (s *MonitorStoreDataSource) SetAgentDirectory(directory MonitorAgentDirectory) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.agents = directory
	s.entries = make(map[string]*monitorStoreCacheEntry)
	s.mu.Unlock()
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

	s.mu.Lock()
	directory := s.agents
	s.mu.Unlock()
	data := collectMonitorStoreData(ctx, s.st, directory, workspaceHint)
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

func collectMonitorStoreData(
	ctx context.Context,
	st MonitorProjectionSources,
	directory MonitorAgentDirectory,
	workspaceHint string,
) monitorStoreData {
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

	if directory == nil {
		slog.Warn("monitor: canonical Agents directory is unavailable", "workspace", wsKey)
		return data
	}
	identities, err := directory.ListAgents(ctx, wsKey, agentsmodule.AgentFilter{})
	if err != nil {
		slog.Warn("monitor: list canonical Agents failed", "workspace", wsKey, "err", err)
		return data
	}
	roles, err := directory.ListRoles(ctx, wsKey)
	if err != nil {
		slog.Warn("monitor: list canonical Roles failed", "workspace", wsKey, "err", err)
		return data
	}

	workspaceData := monitorWorkspaceDataForAgents(ctx, st, wsKey, wsName)
	rolesByName := monitorCanonicalRolesByName(roles, wsKey)
	latestSessions := latestAgentSessionsForMonitor(ctx, st, wsKey)
	interactiveByAgent := latestInteractiveSessionsForMonitor(ctx, st, wsKey)
	inboxByAgent := agentInboxSummariesForMonitor(ctx, st, wsKey)
	data.Agents = monitorAgentStatuses(identities, rolesByName, workspaceData, latestSessions, interactiveByAgent, inboxByAgent, wsName)
	return data
}

func monitorAgentStatuses(
	identities []*agentsmodule.Agent,
	rolesByName map[string]*agentsmodule.Role,
	workspaceData *operationalview.Workspace,
	latestSessions map[string]*interaction.SessionRecord,
	interactiveByAgent map[string]*interaction.SessionRecord,
	inboxByAgent map[string]agentInboxSummary,
	wsName string,
) []monitor.AgentStatus {
	statuses := []monitor.AgentStatus{}
	for _, identity := range identities {
		status, ok := monitorAgentStatus(identity, rolesByName, workspaceData, latestSessions,
			interactiveByAgent, inboxByAgent, wsName)
		if ok {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

type monitorAgentSessionState struct {
	taskID, sessionID, liveStatus, activePhase, lastErrorClass string
}

func monitorAgentStatus(
	identity *agentsmodule.Agent,
	rolesByName map[string]*agentsmodule.Role,
	workspaceData *operationalview.Workspace,
	latestSessions map[string]*interaction.SessionRecord,
	interactiveByAgent map[string]*interaction.SessionRecord,
	inboxByAgent map[string]agentInboxSummary,
	wsName string,
) (monitor.AgentStatus, bool) {
	if identity == nil {
		return monitor.AgentStatus{}, false
	}
	runtime, roleKind, ok := monitorAgentRuntime(identity, rolesByName)
	if !ok {
		return monitor.AgentStatus{}, false
	}
	placement := operationalview.Agent{
		Name: identity.AgentID, Kind: roleKind, RoleName: identity.Behavior.RoleName,
		Backend: runtime.Backend, Repos: runtime.Repos,
		RepoGroups: runtime.RepoGroups, CrossRepo: runtime.CrossRepo,
	}
	session := monitorAgentSession(latestSessions[identity.AgentID])
	orchestrationID := ""
	if interactive := interactiveByAgent[identity.AgentID]; interactive != nil {
		orchestrationID = interactive.SessionID
	}
	inbox := inboxByAgent[identity.AgentID]
	return monitor.AgentStatus{
		Name: identity.AgentID, Branch: monitorBranchFromAgent(workspaceData, placement),
		Status: monitorStatusFromDesiredState(identity.DesiredState), Role: identity.Behavior.RoleName,
		RoleKind: roleKind, Repo: monitorRepoFromAgent(placement), Workspace: wsName,
		InboxQueuedCount: inbox.QueuedCount, InboxFailedCount: inbox.FailedCount,
		InboxLatestMessage: inbox.LatestMessage, OrchestratorSessionID: orchestrationID,
		TaskID: session.taskID, SessionID: session.sessionID, DesiredState: string(identity.DesiredState),
		LiveStatus: session.liveStatus, ActiveTaskID: session.taskID, ActivePhase: session.activePhase,
		LastErrorClass: session.lastErrorClass,
	}, true
}

func monitorAgentRuntime(
	identity *agentsmodule.Agent,
	rolesByName map[string]*agentsmodule.Role,
) (agentsmodule.RuntimeMetadata, string, bool) {
	runtime, err := agentsmodule.ParseRuntimeMetadata(identity.Metadata)
	if err != nil {
		slog.Warn("monitor: reject malformed canonical Agent metadata", "agent", identity.AgentID, "err", err)
		return agentsmodule.RuntimeMetadata{}, "", false
	}
	roleKind := runtime.RoleKind
	if roleKind == "" && rolesByName[identity.Behavior.RoleName] != nil {
		roleKind = strings.TrimSpace(rolesByName[identity.Behavior.RoleName].Kind)
	}
	return runtime, roleKind, true
}

func monitorAgentSession(session *interaction.SessionRecord) monitorAgentSessionState {
	if session == nil {
		return monitorAgentSessionState{}
	}
	state := monitorAgentSessionState{
		taskID: session.TaskID, sessionID: session.SessionID,
		activePhase: session.Phase, lastErrorClass: session.ErrorClass,
	}
	if monitorSessionActive(session.Status) && session.FinishedAt == nil {
		state.liveStatus = "working"
	}
	return state
}

func monitorCanonicalRolesByName(roles []*agentsmodule.Role, workspace string) map[string]*agentsmodule.Role {
	out := make(map[string]*agentsmodule.Role, len(roles))
	for _, role := range roles {
		if role == nil || role.WorkspaceKey != workspace || role.Name == "" {
			continue
		}
		out[role.Name] = role
	}
	return out
}

func monitorStatusFromDesiredState(state agentsmodule.DesiredState) string {
	if state == agentsmodule.DesiredRunning {
		return "ready"
	}
	return "stopped"
}

func agentInboxSummariesForMonitor(ctx context.Context, st MonitorProjectionSources, wsKey string) map[string]agentInboxSummary {
	out := make(map[string]agentInboxSummary)
	if st == nil || st.AgentInboxMessages() == nil || wsKey == "" {
		return out
	}
	for _, status := range []interaction.InboxRecordStatus{interaction.InboxRecordQueued, interaction.InboxRecordFailed} {
		items, err := st.AgentInboxMessages().List(ctx, wsKey, interaction.AgentInboxMessageFilter{Status: status, Limit: 10000})
		if err != nil {
			slog.Warn("monitor: list agent inbox messages failed", "workspace", wsKey, "status", status, "err", err)
			continue
		}
		for _, item := range items {
			if item == nil || item.TargetAgentID == "" {
				continue
			}
			summary := out[item.TargetAgentID]
			if status == interaction.InboxRecordQueued {
				summary.QueuedCount++
			} else if status == interaction.InboxRecordFailed {
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

func workspaceNames(workspaces []*workspacemodule.Workspace) []string {
	names := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws != nil {
			names = append(names, ws.Name)
		}
	}
	return names
}

func resolveMonitorWorkspaceFromList(ctx context.Context, st MonitorProjectionSources, workspaceHint string, workspaces []*workspacemodule.Workspace) (key string, name string, ok bool) {
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

func findMonitorWorkspace(workspaces []*workspacemodule.Workspace, hint string, allowName bool) (key string, name string, ok bool) {
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

func monitorWorkspaceDataForAgents(ctx context.Context, st MonitorProjectionSources, wsKey string, wsName string) *operationalview.Workspace {
	if st == nil {
		return nil
	}
	repos, err := st.Repos().List(ctx, wsKey)
	if err != nil {
		slog.Warn("monitor: list store repos failed", "workspace", wsKey, "err", err)
		return nil
	}

	out := make([]operationalview.Repository, 0, len(repos))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		out = append(out, operationalview.Repository{
			Name:   repo.Name,
			Groups: repo.Groups,
		})
	}

	return &operationalview.Workspace{
		ID:    wsKey,
		Name:  wsName,
		Path:  storeadapter.ResolveWorkspacePath(wsKey),
		Repos: out,
	}
}
