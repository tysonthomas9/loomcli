package fleetdb

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type nodeStore struct{ client *Client }

var _ store.NodeStore = (*nodeStore)(nil)

func (s *nodeStore) Create(ctx context.Context, in store.NodeCreate) (*domain.Node, error) {
	body := struct {
		NodeID          string                 `json:"node_id"`
		OwnerActor      string                 `json:"owner_actor,omitempty"`
		RuntimeProvider domain.RuntimeProvider `json:"runtime_provider,omitempty"`
		Labels          []string               `json:"labels,omitempty"`
		Capabilities    []string               `json:"capabilities,omitempty"`
		ToolInventory   []string               `json:"tool_inventory,omitempty"`
		Version         string                 `json:"version,omitempty"`
		Capacity        int                    `json:"capacity,omitempty"`
		DrainState      domain.NodeDrainState  `json:"drain_state,omitempty"`
		TTLSeconds      int                    `json:"ttl_seconds,omitempty"`
	}{
		NodeID:          in.NodeID,
		OwnerActor:      in.OwnerActor,
		RuntimeProvider: in.RuntimeProvider,
		Labels:          in.Labels,
		Capabilities:    in.Capabilities,
		ToolInventory:   in.ToolInventory,
		Version:         in.Version,
		Capacity:        in.Capacity,
		DrainState:      in.DrainState,
		TTLSeconds:      ttlSeconds(in.TTL),
	}
	var out domain.Node
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/nodes", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) Get(ctx context.Context, ws, nodeID string) (*domain.Node, error) {
	var out domain.Node
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/nodes/"+pathEscape(nodeID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) List(ctx context.Context, ws string) ([]*domain.Node, error) {
	var resp struct {
		Nodes []*domain.Node `json:"nodes"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/nodes", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Nodes == nil {
		resp.Nodes = []*domain.Node{}
	}
	return resp.Nodes, nil
}

func (s *nodeStore) Heartbeat(ctx context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
	path := "/api/v1/" + pathEscape(ws) + "/nodes/" + pathEscape(nodeID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out domain.Node
	if err := s.client.do(ctx, "POST", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) Update(ctx context.Context, ws, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	var out domain.Node
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/nodes/"+pathEscape(nodeID), nodeUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type agentSessionStore struct{ client *Client }

var _ store.AgentSessionStore = (*agentSessionStore)(nil)

func (s *agentSessionStore) Create(ctx context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	body := struct {
		SessionID       string                    `json:"session_id"`
		AgentID         string                    `json:"agent_id"`
		NodeID          string                    `json:"node_id,omitempty"`
		Kind            domain.AgentSessionKind   `json:"kind,omitempty"`
		TaskID          string                    `json:"task_id,omitempty"`
		TerminalID      string                    `json:"terminal_id,omitempty"`
		ParentSessionID string                    `json:"parent_session_id,omitempty"`
		Status          domain.AgentSessionStatus `json:"status,omitempty"`
		Phase           string                    `json:"phase,omitempty"`
		Attempt         int                       `json:"attempt,omitempty"`
		Metadata        map[string]string         `json:"metadata,omitempty"`
	}{
		SessionID:       in.SessionID,
		AgentID:         in.AgentID,
		NodeID:          in.NodeID,
		Kind:            in.Kind,
		TaskID:          in.TaskID,
		TerminalID:      in.TerminalID,
		ParentSessionID: in.ParentSessionID,
		Status:          in.Status,
		Phase:           in.Phase,
		Attempt:         in.Attempt,
		Metadata:        in.Metadata,
	}
	var out domain.AgentSession
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-sessions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) Get(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	var out domain.AgentSession
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) List(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	q := url.Values{}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.TaskID != "" {
		q.Set("task_id", filter.TaskID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	// fleet-db's listAgentSessions doesn't yet accept kind / parent_session_id
	// as query params (see fleet-db/api/openapi.yaml :: listAgentSessions),
	// so we ask for the broader set and filter client-side below. When fleet-db
	// adds those params, append them here and drop the post-filter pass.
	clientSideKind := filter.Kind
	clientSideParent := filter.ParentSessionID
	// Limit must be applied *after* the client-side filter, otherwise we
	// could return fewer than the requested count when the server-side
	// page contains many non-matching kinds/parents.
	clientSideLimit := filter.Limit
	if clientSideKind == "" && clientSideParent == "" && filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-sessions", q)
	var resp struct {
		AgentSessions []*domain.AgentSession `json:"agent_sessions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentSessions == nil {
		resp.AgentSessions = []*domain.AgentSession{}
	}
	if clientSideKind != "" || clientSideParent != "" {
		resp.AgentSessions = filterAgentSessionsClientSide(resp.AgentSessions, clientSideKind, clientSideParent, clientSideLimit)
	}
	return resp.AgentSessions, nil
}

// Client-side filter for Kind / ParentSessionID; see List comment above.
func filterAgentSessionsClientSide(sessions []*domain.AgentSession, kind domain.AgentSessionKind, parent string, limit int) []*domain.AgentSession {
	filtered := make([]*domain.AgentSession, 0, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		if kind != "" && sess.Kind != kind {
			continue
		}
		if parent != "" && sess.ParentSessionID != parent {
			continue
		}
		filtered = append(filtered, sess)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func (s *agentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	var out domain.AgentSession
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID)+"/heartbeat", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) Update(ctx context.Context, ws, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	var out domain.AgentSession
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID), agentSessionUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func ttlSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	return int(ttl.Round(time.Second) / time.Second)
}

type terminalSessionStore struct{ client *Client }

var _ store.TerminalSessionStore = (*terminalSessionStore)(nil)

func (s *terminalSessionStore) Create(ctx context.Context, in store.TerminalSessionCreate) (*domain.TerminalSession, error) {
	body := map[string]any{
		"terminal_id":      in.TerminalID,
		"agent_id":         in.AgentID,
		"session_id":       in.SessionID,
		"node_id":          in.NodeID,
		"task_id":          in.TaskID,
		"title":            in.Title,
		"kind":             in.Kind,
		"status":           in.Status,
		"pty_provider":     in.PTYProvider,
		"stream_ref":       in.StreamRef,
		"transcript_ref":   in.TranscriptRef,
		"attached_clients": in.AttachedClients,
		"metadata":         in.Metadata,
	}
	var out domain.TerminalSession
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/terminal-sessions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *terminalSessionStore) Get(ctx context.Context, ws, terminalID string) (*domain.TerminalSession, error) {
	var out domain.TerminalSession
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/terminal-sessions/"+pathEscape(terminalID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *terminalSessionStore) List(ctx context.Context, ws string, filter store.TerminalSessionFilter) ([]*domain.TerminalSession, error) {
	q := url.Values{}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.SessionID != "" {
		q.Set("session_id", filter.SessionID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.TaskID != "" {
		q.Set("task_id", filter.TaskID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/terminal-sessions", q)
	var resp struct {
		TerminalSessions []*domain.TerminalSession `json:"terminal_sessions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TerminalSessions == nil {
		resp.TerminalSessions = []*domain.TerminalSession{}
	}
	return resp.TerminalSessions, nil
}

func (s *terminalSessionStore) Update(ctx context.Context, ws, terminalID string, patch store.TerminalSessionUpdate) (*domain.TerminalSession, error) {
	var out domain.TerminalSession
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/terminal-sessions/"+pathEscape(terminalID), terminalSessionUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type artifactStore struct{ client *Client }

var _ store.ArtifactStore = (*artifactStore)(nil)

func (s *artifactStore) Create(ctx context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	body := map[string]any{
		"artifact_id":      in.ArtifactID,
		"agent_id":         in.AgentID,
		"session_id":       in.SessionID,
		"terminal_id":      in.TerminalID,
		"task_id":          in.TaskID,
		"owner_type":       in.OwnerType,
		"owner_id":         in.OwnerID,
		"type":             in.Type,
		"uri":              in.URI,
		"summary":          in.Summary,
		"mime_type":        in.MIMEType,
		"size_bytes":       in.SizeBytes,
		"checksum":         in.Checksum,
		"content_hash":     in.ContentHash,
		"visibility":       in.Visibility,
		"redaction_status": in.RedactionStatus,
		"durable_status":   in.DurableStatus,
		"metadata":         in.Metadata,
	}
	var out domain.Artifact
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/artifacts", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactStore) Get(ctx context.Context, ws, artifactID string) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactStore) List(ctx context.Context, ws string, filter store.ArtifactFilter) ([]*domain.Artifact, error) {
	q := url.Values{}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.SessionID != "" {
		q.Set("session_id", filter.SessionID)
	}
	if filter.TerminalID != "" {
		q.Set("terminal_id", filter.TerminalID)
	}
	if filter.TaskID != "" {
		q.Set("task_id", filter.TaskID)
	}
	if filter.OwnerType != "" {
		q.Set("owner_type", filter.OwnerType)
	}
	if filter.OwnerID != "" {
		q.Set("owner_id", filter.OwnerID)
	}
	if filter.Type != "" {
		q.Set("type", filter.Type)
	}
	if filter.Status != "" {
		q.Set("durable_status", filter.Status)
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/artifacts", q)
	var resp struct {
		Artifacts []*domain.Artifact `json:"artifacts"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Artifacts == nil {
		resp.Artifacts = []*domain.Artifact{}
	}
	return resp.Artifacts, nil
}

func (s *artifactStore) UploadContent(ctx context.Context, ws, artifactID string, upload store.ArtifactContentUpload) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := s.client.doRaw(ctx, "PUT", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID)+"/content", upload.Body, upload.MIMEType, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactStore) Finalize(ctx context.Context, ws, artifactID string, finalize store.ArtifactFinalize) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID)+"/finalize", artifactFinalizeBody(finalize), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactStore) Update(ctx context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID), artifactUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func nodeUpdateBody(patch store.NodeUpdate) map[string]any {
	body := map[string]any{}
	if patch.OwnerActor != nil {
		body["owner_actor"] = *patch.OwnerActor
	}
	if patch.RuntimeProvider != nil {
		body["runtime_provider"] = *patch.RuntimeProvider
	}
	if patch.Labels != nil {
		body["labels"] = *patch.Labels
	}
	if patch.Capabilities != nil {
		body["capabilities"] = *patch.Capabilities
	}
	if patch.ToolInventory != nil {
		body["tool_inventory"] = *patch.ToolInventory
	}
	if patch.Version != nil {
		body["version"] = *patch.Version
	}
	if patch.Capacity != nil {
		body["capacity"] = *patch.Capacity
	}
	if patch.DrainState != nil {
		body["drain_state"] = *patch.DrainState
	}
	if patch.ExpiresAt != nil {
		body["expires_at"] = *patch.ExpiresAt
	}
	return body
}

func agentSessionUpdateBody(patch store.AgentSessionUpdate) map[string]any {
	body := map[string]any{}
	if patch.NodeID != nil {
		body["node_id"] = *patch.NodeID
	}
	if patch.TaskID != nil {
		body["task_id"] = *patch.TaskID
	}
	if patch.Status != nil {
		body["status"] = *patch.Status
	}
	if patch.Phase != nil {
		body["phase"] = *patch.Phase
	}
	if patch.LastHeartbeat != nil {
		body["last_heartbeat"] = *patch.LastHeartbeat
	}
	if patch.FinishedAt != nil {
		body["finished_at"] = *patch.FinishedAt
	}
	if patch.Summary != nil {
		body["summary"] = *patch.Summary
	}
	if patch.ErrorClass != nil {
		body["error_class"] = *patch.ErrorClass
	}
	if patch.ExitCode != nil {
		body["exit_code"] = *patch.ExitCode
	}
	if patch.Metadata != nil {
		body["metadata"] = *patch.Metadata
	}
	return body
}

func terminalSessionUpdateBody(patch store.TerminalSessionUpdate) map[string]any {
	body := map[string]any{}
	if patch.AgentID != nil {
		body["agent_id"] = *patch.AgentID
	}
	if patch.SessionID != nil {
		body["session_id"] = *patch.SessionID
	}
	if patch.NodeID != nil {
		body["node_id"] = *patch.NodeID
	}
	if patch.TaskID != nil {
		body["task_id"] = *patch.TaskID
	}
	if patch.Title != nil {
		body["title"] = *patch.Title
	}
	if patch.Kind != nil {
		body["kind"] = *patch.Kind
	}
	if patch.Status != nil {
		body["status"] = *patch.Status
	}
	if patch.PTYProvider != nil {
		body["pty_provider"] = *patch.PTYProvider
	}
	if patch.StreamRef != nil {
		body["stream_ref"] = *patch.StreamRef
	}
	if patch.TranscriptRef != nil {
		body["transcript_ref"] = *patch.TranscriptRef
	}
	if patch.AttachedClients != nil {
		body["attached_clients"] = *patch.AttachedClients
	}
	if patch.LastSeenAt != nil {
		body["last_seen_at"] = *patch.LastSeenAt
	}
	if patch.EndedAt != nil {
		body["ended_at"] = *patch.EndedAt
	}
	if patch.Metadata != nil {
		body["metadata"] = *patch.Metadata
	}
	return body
}

func artifactUpdateBody(patch store.ArtifactUpdate) map[string]any {
	body := map[string]any{}
	if patch.AgentID != nil {
		body["agent_id"] = *patch.AgentID
	}
	if patch.SessionID != nil {
		body["session_id"] = *patch.SessionID
	}
	if patch.TerminalID != nil {
		body["terminal_id"] = *patch.TerminalID
	}
	if patch.TaskID != nil {
		body["task_id"] = *patch.TaskID
	}
	if patch.OwnerType != nil {
		body["owner_type"] = *patch.OwnerType
	}
	if patch.OwnerID != nil {
		body["owner_id"] = *patch.OwnerID
	}
	if patch.Type != nil {
		body["type"] = *patch.Type
	}
	if patch.URI != nil {
		body["uri"] = *patch.URI
	}
	if patch.Summary != nil {
		body["summary"] = *patch.Summary
	}
	if patch.MIMEType != nil {
		body["mime_type"] = *patch.MIMEType
	}
	if patch.SizeBytes != nil {
		body["size_bytes"] = *patch.SizeBytes
	}
	if patch.Checksum != nil {
		body["checksum"] = *patch.Checksum
	}
	if patch.ContentHash != nil {
		body["content_hash"] = *patch.ContentHash
	}
	if patch.Visibility != nil {
		body["visibility"] = *patch.Visibility
	}
	if patch.RedactionStatus != nil {
		body["redaction_status"] = *patch.RedactionStatus
	}
	if patch.DurableStatus != nil {
		body["durable_status"] = *patch.DurableStatus
	}
	if patch.Metadata != nil {
		body["metadata"] = *patch.Metadata
	}
	if patch.FinalizedAt != nil {
		body["finalized_at"] = patch.FinalizedAt.Format(time.RFC3339Nano)
	}
	return body
}

func artifactFinalizeBody(finalize store.ArtifactFinalize) map[string]any {
	body := map[string]any{}
	if finalize.URI != nil {
		body["uri"] = *finalize.URI
	}
	if finalize.Summary != nil {
		body["summary"] = *finalize.Summary
	}
	if finalize.MIMEType != nil {
		body["mime_type"] = *finalize.MIMEType
	}
	if finalize.SizeBytes != nil {
		body["size_bytes"] = *finalize.SizeBytes
	}
	if finalize.Checksum != nil {
		body["checksum"] = *finalize.Checksum
	}
	if finalize.ContentHash != nil {
		body["content_hash"] = *finalize.ContentHash
	}
	if finalize.Visibility != nil {
		body["visibility"] = *finalize.Visibility
	}
	if finalize.RedactionStatus != nil {
		body["redaction_status"] = *finalize.RedactionStatus
	}
	if finalize.Metadata != nil {
		body["metadata"] = *finalize.Metadata
	}
	return body
}

type agentLeaseStore struct{ client *Client }

var _ store.AgentLeaseStore = (*agentLeaseStore)(nil)

func (s *agentLeaseStore) Create(ctx context.Context, in store.AgentLeaseCreate) (*domain.AgentLease, error) {
	body := map[string]any{"lease_id": in.LeaseID, "agent_id": in.AgentID, "node_id": in.NodeID, "ttl_seconds": ttlSeconds(in.TTL)}
	var out domain.AgentLease
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-sessions/"+pathEscape(in.SessionID)+"/leases", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentLeaseStore) Get(ctx context.Context, ws, leaseID string) (*domain.AgentLease, error) {
	var out domain.AgentLease
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-leases/"+pathEscape(leaseID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentLeaseStore) List(ctx context.Context, ws string, filter store.AgentLeaseFilter) ([]*domain.AgentLease, error) {
	q := url.Values{}
	if filter.SessionID != "" {
		q.Set("session_id", filter.SessionID)
	}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-leases", q)
	var resp struct {
		AgentLeases []*domain.AgentLease `json:"agent_leases"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentLeases == nil {
		resp.AgentLeases = []*domain.AgentLease{}
	}
	return resp.AgentLeases, nil
}

func (s *agentLeaseStore) Heartbeat(ctx context.Context, ws, leaseID, token string, ttl time.Duration) (*domain.AgentLease, error) {
	path := "/api/v1/" + pathEscape(ws) + "/agent-leases/" + pathEscape(leaseID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out domain.AgentLease
	if err := s.client.doWithHeaders(ctx, "POST", path, nil, &out, map[string]string{"X-Agent-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentLeaseStore) Release(ctx context.Context, ws, leaseID, token string) (*domain.AgentLease, error) {
	var out domain.AgentLease
	if err := s.client.doWithHeaders(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-leases/"+pathEscape(leaseID)+"/release", nil, &out, map[string]string{"X-Agent-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

type agentOwnershipLeaseStore struct{ client *Client }

var _ store.AgentOwnershipLeaseStore = (*agentOwnershipLeaseStore)(nil)

func (s *agentOwnershipLeaseStore) Acquire(ctx context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	body := map[string]any{
		"lease_id":         in.LeaseID,
		"owner_id":         in.OwnerID,
		"runtime_provider": in.RuntimeProvider,
		"node_id":          in.NodeID,
		"ttl_seconds":      ttlSeconds(in.TTL),
	}
	var out domain.AgentOwnershipLease
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-ownership-leases/"+pathEscape(in.AgentID)+"/acquire", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) Get(ctx context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	var out domain.AgentOwnershipLease
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-ownership-leases/"+pathEscape(agentID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) List(ctx context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	q := url.Values{}
	if filter.OwnerID != "" {
		q.Set("owner_id", filter.OwnerID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.RuntimeProvider != "" {
		q.Set("runtime_provider", string(filter.RuntimeProvider))
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-ownership-leases", q)
	var resp struct {
		AgentOwnershipLeases []*domain.AgentOwnershipLease `json:"agent_ownership_leases"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentOwnershipLeases == nil {
		resp.AgentOwnershipLeases = []*domain.AgentOwnershipLease{}
	}
	return resp.AgentOwnershipLeases, nil
}

func (s *agentOwnershipLeaseStore) Heartbeat(ctx context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	path := "/api/v1/" + pathEscape(ws) + "/agent-ownership-leases/" + pathEscape(agentID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out domain.AgentOwnershipLease
	if err := s.client.doWithHeaders(ctx, "POST", path, nil, &out, map[string]string{"X-Agent-Ownership-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) Release(ctx context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	var out domain.AgentOwnershipLease
	if err := s.client.doWithHeaders(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-ownership-leases/"+pathEscape(agentID)+"/release", nil, &out, map[string]string{"X-Agent-Ownership-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

type agentCommandStore struct{ client *Client }

var _ store.AgentCommandStore = (*agentCommandStore)(nil)

func (s *agentCommandStore) Create(ctx context.Context, in store.AgentCommandCreate) (*domain.AgentCommand, error) {
	body := map[string]any{"command_id": in.CommandID, "target_agent_id": in.TargetAgentID, "target_node_id": in.TargetNodeID, "session_id": in.SessionID, "type": in.Type, "payload": in.Payload}
	var out domain.AgentCommand
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-commands", body, &out); err != nil {
		return nil, err
	}
	if out.Status == "" {
		out.Status = domain.AgentCommandQueued
	}
	return &out, nil
}

func (s *agentCommandStore) Get(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	var out domain.AgentCommand
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-commands/"+pathEscape(commandID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentCommandStore) List(ctx context.Context, ws string, filter store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	q := url.Values{}
	if filter.TargetAgentID != "" {
		q.Set("target_agent_id", filter.TargetAgentID)
	}
	if filter.TargetNodeID != "" {
		q.Set("target_node_id", filter.TargetNodeID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.AfterCursor > 0 {
		q.Set("after_cursor", strconv.FormatInt(filter.AfterCursor, 10))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-commands", q)
	var resp struct {
		AgentCommands []*domain.AgentCommand `json:"agent_commands"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentCommands == nil {
		resp.AgentCommands = []*domain.AgentCommand{}
	}
	return resp.AgentCommands, nil
}

func (s *agentCommandStore) Ack(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	var out domain.AgentCommand
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-commands/"+pathEscape(commandID)+"/ack", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentCommandStore) Complete(ctx context.Context, ws, commandID string, update store.AgentCommandComplete) (*domain.AgentCommand, error) {
	var out domain.AgentCommand
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-commands/"+pathEscape(commandID)+"/complete", update, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type agentInboxMessageStore struct{ client *Client }

var _ store.AgentInboxMessageStore = (*agentInboxMessageStore)(nil)

func (s *agentInboxMessageStore) Create(ctx context.Context, in store.AgentInboxMessageCreate) (*domain.AgentInboxMessage, error) {
	body := map[string]any{
		"inbox_message_id":    in.InboxMessageID,
		"target_agent_id":     in.TargetAgentID,
		"session_id":          in.SessionID,
		"body":                in.Body,
		"source_kind":         in.SourceKind,
		"source_ref":          in.SourceRef,
		"driver_run_id":       in.DriverRunID,
		"task_run_id":         in.TaskRunID,
		"trigger_event_id":    in.TriggerEventID,
		"trigger_delivery_id": in.TriggerDeliveryID,
		"dedupe_key":          in.DedupeKey,
	}
	var out domain.AgentInboxMessage
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-inbox-messages", body, &out); err != nil {
		return nil, err
	}
	if out.Status == "" {
		out.Status = domain.AgentInboxMessageQueued
	}
	return &out, nil
}

func (s *agentInboxMessageStore) Get(ctx context.Context, ws, inboxMessageID string) (*domain.AgentInboxMessage, error) {
	var out domain.AgentInboxMessage
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-inbox-messages/"+pathEscape(inboxMessageID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentInboxMessageStore) List(ctx context.Context, ws string, filter store.AgentInboxMessageFilter) ([]*domain.AgentInboxMessage, error) {
	q := url.Values{}
	if filter.TargetAgentID != "" {
		q.Set("target_agent_id", filter.TargetAgentID)
	}
	if filter.SessionID != "" {
		q.Set("session_id", filter.SessionID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.SourceKind != "" {
		q.Set("source_kind", filter.SourceKind)
	}
	if filter.SourceRef != "" {
		q.Set("source_ref", filter.SourceRef)
	}
	if filter.DriverRunID != "" {
		q.Set("driver_run_id", filter.DriverRunID)
	}
	if filter.TaskRunID != "" {
		q.Set("task_run_id", filter.TaskRunID)
	}
	if filter.AfterCursor > 0 {
		q.Set("after_cursor", strconv.FormatInt(filter.AfterCursor, 10))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-inbox-messages", q)
	var resp struct {
		AgentInboxMessages []*domain.AgentInboxMessage `json:"agent_inbox_messages"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentInboxMessages == nil {
		resp.AgentInboxMessages = []*domain.AgentInboxMessage{}
	}
	return resp.AgentInboxMessages, nil
}

func (s *agentInboxMessageStore) ClaimNext(ctx context.Context, in store.AgentInboxMessageClaim) (*domain.AgentInboxMessage, error) {
	body := map[string]any{
		"target_agent_id": in.TargetAgentID,
		"session_id":      in.SessionID,
		"claimed_by":      in.ClaimedBy,
	}
	if in.LeaseTTL > 0 {
		body["lease_ttl_ms"] = in.LeaseTTL.Milliseconds()
	}
	var out domain.AgentInboxMessage
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-inbox-messages/claim-next", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentInboxMessageStore) Complete(ctx context.Context, ws, inboxMessageID string, update store.AgentInboxMessageComplete) (*domain.AgentInboxMessage, error) {
	var out domain.AgentInboxMessage
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-inbox-messages/"+pathEscape(inboxMessageID)+"/complete", update, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
