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
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/nodes/"+pathEscape(nodeID), patch, &out); err != nil {
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
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/agent-sessions"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		AgentSessions []*domain.AgentSession `json:"agent_sessions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentSessions == nil {
		resp.AgentSessions = []*domain.AgentSession{}
	}
	return resp.AgentSessions, nil
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
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID), patch, &out); err != nil {
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
	path := "/api/v1/" + pathEscape(ws) + "/terminal-sessions"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
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
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/terminal-sessions/"+pathEscape(terminalID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type artifactStore struct{ client *Client }

var _ store.ArtifactStore = (*artifactStore)(nil)

func (s *artifactStore) Create(ctx context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	body := map[string]any{
		"artifact_id": in.ArtifactID,
		"agent_id":    in.AgentID,
		"session_id":  in.SessionID,
		"terminal_id": in.TerminalID,
		"task_id":     in.TaskID,
		"type":        in.Type,
		"uri":         in.URI,
		"summary":     in.Summary,
		"mime_type":   in.MIMEType,
		"size_bytes":  in.SizeBytes,
		"checksum":    in.Checksum,
		"metadata":    in.Metadata,
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
	if filter.Type != "" {
		q.Set("type", filter.Type)
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/artifacts"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
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

func (s *artifactStore) Update(ctx context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	path := "/api/v1/" + pathEscape(ws) + "/agent-leases"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
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
