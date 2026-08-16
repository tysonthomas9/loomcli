package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type nodeStore struct{ client *Client }

var _ execution.NodeStore = (*nodeStore)(nil)

func (s *nodeStore) Create(ctx context.Context, in execution.NodeCreate) (*execution.WorkerNode, error) {
	body := struct {
		NodeID          string                         `json:"node_id"`
		OwnerActor      string                         `json:"owner_actor,omitempty"`
		RuntimeProvider execution.RuntimeProvider      `json:"runtime_provider,omitempty"`
		Labels          []string                       `json:"labels,omitempty"`
		Capabilities    []string                       `json:"capabilities,omitempty"`
		ToolInventory   []string                       `json:"tool_inventory,omitempty"`
		Version         string                         `json:"version,omitempty"`
		Capacity        int                            `json:"capacity,omitempty"`
		DrainState      execution.WorkerNodeDrainState `json:"drain_state,omitempty"`
		TTLSeconds      int                            `json:"ttl_seconds,omitempty"`
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
	var out execution.WorkerNode
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/nodes", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) Get(ctx context.Context, ws, nodeID string) (*execution.WorkerNode, error) {
	var out execution.WorkerNode
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/nodes/"+pathEscape(nodeID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) List(ctx context.Context, ws string) ([]*execution.WorkerNode, error) {
	var resp struct {
		Nodes []*execution.WorkerNode `json:"nodes"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/nodes", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Nodes == nil {
		resp.Nodes = []*execution.WorkerNode{}
	}
	return resp.Nodes, nil
}

func (s *nodeStore) Heartbeat(ctx context.Context, ws, nodeID string, ttl time.Duration) (*execution.WorkerNode, error) {
	path := "/api/v1/" + pathEscape(ws) + "/nodes/" + pathEscape(nodeID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out execution.WorkerNode
	if err := s.client.do(ctx, "POST", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *nodeStore) Update(ctx context.Context, ws, nodeID string, patch execution.NodeUpdate) (*execution.WorkerNode, error) {
	var out execution.WorkerNode
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/nodes/"+pathEscape(nodeID), nodeUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type agentSessionStore struct{ client *Client }

var _ interaction.AgentSessionStore = (*agentSessionStore)(nil)

func (s *agentSessionStore) Create(ctx context.Context, in interaction.AgentSessionCreate) (*interaction.SessionRecord, error) {
	body := struct {
		SessionID       string                          `json:"session_id"`
		AgentID         string                          `json:"agent_id"`
		NodeID          string                          `json:"node_id,omitempty"`
		Kind            interaction.SessionRecordKind   `json:"kind,omitempty"`
		TaskID          string                          `json:"task_id,omitempty"`
		TerminalID      string                          `json:"terminal_id,omitempty"`
		ParentSessionID string                          `json:"parent_session_id,omitempty"`
		Status          interaction.SessionRecordStatus `json:"status,omitempty"`
		Phase           string                          `json:"phase,omitempty"`
		Attempt         int                             `json:"attempt,omitempty"`
		Metadata        map[string]string               `json:"metadata,omitempty"`
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
	var out interaction.SessionRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-sessions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) Get(ctx context.Context, ws, sessionID string) (*interaction.SessionRecord, error) {
	var out interaction.SessionRecord
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) List(ctx context.Context, ws string, filter interaction.AgentSessionFilter) ([]*interaction.SessionRecord, error) {
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
		AgentSessions []*interaction.SessionRecord `json:"agent_sessions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentSessions == nil {
		resp.AgentSessions = []*interaction.SessionRecord{}
	}
	if clientSideKind != "" || clientSideParent != "" {
		resp.AgentSessions = filterAgentSessionsClientSide(resp.AgentSessions, clientSideKind, clientSideParent, clientSideLimit)
	}
	return resp.AgentSessions, nil
}

// Client-side filter for Kind / ParentSessionID; see List comment above.
func filterAgentSessionsClientSide(sessions []*interaction.SessionRecord, kind interaction.SessionRecordKind, parent string, limit int) []*interaction.SessionRecord {
	filtered := make([]*interaction.SessionRecord, 0, len(sessions))
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

func (s *agentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*interaction.SessionRecord, error) {
	var out interaction.SessionRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID)+"/heartbeat", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) Update(ctx context.Context, ws, sessionID string, patch interaction.AgentSessionUpdate) (*interaction.SessionRecord, error) {
	var out interaction.SessionRecord
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

// workerStore renews/removes fleet-db worker registrations over HTTP.
type workerStore struct{ client *Client }

var _ execution.WorkerStore = (*workerStore)(nil)

// Heartbeat renews the worker registration lease via the fleet-db worker
// heartbeat endpoint. The response body (HeartbeatResult) is not consumed.
func (s *workerStore) Heartbeat(ctx context.Context, ws, workerID string) error {
	return s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/workers/"+pathEscape(workerID)+"/heartbeat", nil, nil)
}

// Deregister removes the worker registration (and releases any held issue
// lock) via the fleet-db worker DELETE endpoint. Idempotent server-side.
func (s *workerStore) Deregister(ctx context.Context, ws, workerID string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/workers/"+pathEscape(workerID), nil, nil)
}

type terminalSessionStore struct{ client *Client }

var _ interaction.TerminalSessionStore = (*terminalSessionStore)(nil)

func (s *terminalSessionStore) Create(ctx context.Context, in interaction.TerminalSessionCreate) (*interaction.TerminalRecord, error) {
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
	var out interaction.TerminalRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/terminal-sessions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *terminalSessionStore) Get(ctx context.Context, ws, terminalID string) (*interaction.TerminalRecord, error) {
	var out interaction.TerminalRecord
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/terminal-sessions/"+pathEscape(terminalID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *terminalSessionStore) List(ctx context.Context, ws string, filter interaction.TerminalSessionFilter) ([]*interaction.TerminalRecord, error) {
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
		TerminalSessions []*interaction.TerminalRecord `json:"terminal_sessions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TerminalSessions == nil {
		resp.TerminalSessions = []*interaction.TerminalRecord{}
	}
	return resp.TerminalSessions, nil
}

func (s *terminalSessionStore) Update(ctx context.Context, ws, terminalID string, patch interaction.TerminalSessionUpdate) (*interaction.TerminalRecord, error) {
	var out interaction.TerminalRecord
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/terminal-sessions/"+pathEscape(terminalID), terminalSessionUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func nodeUpdateBody(patch execution.NodeUpdate) map[string]any {
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

func agentSessionUpdateBody(patch interaction.AgentSessionUpdate) map[string]any {
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

func terminalSessionUpdateBody(patch interaction.TerminalSessionUpdate) map[string]any {
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

type agentLeaseStore struct{ client *Client }

var _ interaction.AgentLeaseStore = (*agentLeaseStore)(nil)

func (s *agentLeaseStore) Create(ctx context.Context, in interaction.AgentLeaseCreate) (*interaction.LeaseRecord, error) {
	body := map[string]any{"lease_id": in.LeaseID, "agent_id": in.AgentID, "node_id": in.NodeID, "ttl_seconds": ttlSeconds(in.TTL)}
	var response struct {
		Lease interaction.LeaseRecord `json:"lease"`
		Token string                  `json:"token"`
	}
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-sessions/"+pathEscape(in.SessionID)+"/leases", body, &response); err != nil {
		return nil, err
	}
	if err := validateAgentLeaseEnvelope(response.Lease, response.Token, in); err != nil {
		return nil, err
	}
	response.Lease.Token = response.Token
	return &response.Lease, nil
}

func (s *agentLeaseStore) Get(ctx context.Context, ws, leaseID string) (*interaction.LeaseRecord, error) {
	var out interaction.LeaseRecord
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-leases/"+pathEscape(leaseID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentLeaseStore) List(ctx context.Context, ws string, filter interaction.AgentLeaseFilter) ([]*interaction.LeaseRecord, error) {
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
		AgentLeases []*interaction.LeaseRecord `json:"agent_leases"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentLeases == nil {
		resp.AgentLeases = []*interaction.LeaseRecord{}
	}
	return resp.AgentLeases, nil
}

func (s *agentLeaseStore) Heartbeat(ctx context.Context, ws, leaseID, token string, ttl time.Duration) (*interaction.LeaseRecord, error) {
	path := "/api/v1/" + pathEscape(ws) + "/agent-leases/" + pathEscape(leaseID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out interaction.LeaseRecord
	if err := s.client.doWithHeaders(ctx, "POST", path, nil, &out, map[string]string{"X-Agent-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentLeaseStore) Release(ctx context.Context, ws, leaseID, token string) (*interaction.LeaseRecord, error) {
	var out interaction.LeaseRecord
	if err := s.client.doWithHeaders(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-leases/"+pathEscape(leaseID)+"/release", nil, &out, map[string]string{"X-Agent-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func validateAgentLeaseEnvelope(lease interaction.LeaseRecord, token string, in interaction.AgentLeaseCreate) error {
	switch {
	case token == "":
		return errors.New("fleetdb: agent lease create response omitted one-time token")
	case lease.WorkspaceKey != in.WorkspaceKey:
		return fmt.Errorf("fleetdb: agent lease create response workspace %q does not match %q", lease.WorkspaceKey, in.WorkspaceKey)
	case lease.LeaseID != in.LeaseID:
		return fmt.Errorf("fleetdb: agent lease create response lease %q does not match %q", lease.LeaseID, in.LeaseID)
	case lease.SessionID != in.SessionID:
		return fmt.Errorf("fleetdb: agent lease create response session %q does not match %q", lease.SessionID, in.SessionID)
	case lease.AgentID != in.AgentID:
		return fmt.Errorf("fleetdb: agent lease create response agent %q does not match %q", lease.AgentID, in.AgentID)
	case lease.NodeID != in.NodeID:
		return fmt.Errorf("fleetdb: agent lease create response node %q does not match %q", lease.NodeID, in.NodeID)
	case lease.FencingToken <= 0:
		return fmt.Errorf("fleetdb: agent lease create response has invalid fencing token %d", lease.FencingToken)
	}
	return nil
}

type agentInboxMessageStore struct{ client *Client }

var _ interaction.AgentInboxMessageStore = (*agentInboxMessageStore)(nil)

func (s *agentInboxMessageStore) Create(ctx context.Context, in interaction.AgentInboxMessageCreate) (*interaction.InboxRecord, error) {
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
	var out interaction.InboxRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-inbox-messages", body, &out); err != nil {
		return nil, err
	}
	if out.Status == "" {
		out.Status = interaction.InboxRecordQueued
	}
	return &out, nil
}

func (s *agentInboxMessageStore) Get(ctx context.Context, ws, inboxMessageID string) (*interaction.InboxRecord, error) {
	var out interaction.InboxRecord
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-inbox-messages/"+pathEscape(inboxMessageID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentInboxMessageStore) List(ctx context.Context, ws string, filter interaction.AgentInboxMessageFilter) ([]*interaction.InboxRecord, error) {
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
		AgentInboxMessages []*interaction.InboxRecord `json:"agent_inbox_messages"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentInboxMessages == nil {
		resp.AgentInboxMessages = []*interaction.InboxRecord{}
	}
	return resp.AgentInboxMessages, nil
}

func (s *agentInboxMessageStore) ClaimNext(ctx context.Context, in interaction.AgentInboxMessageClaim) (*interaction.InboxRecord, error) {
	body := map[string]any{
		"target_agent_id": in.TargetAgentID,
		"session_id":      in.SessionID,
		"claimed_by":      in.ClaimedBy,
	}
	if in.LeaseTTL > 0 {
		body["lease_ttl_ms"] = in.LeaseTTL.Milliseconds()
	}
	var out interaction.InboxRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-inbox-messages/claim-next", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentInboxMessageStore) Complete(ctx context.Context, ws, inboxMessageID string, update interaction.AgentInboxMessageComplete) (*interaction.InboxRecord, error) {
	var out interaction.InboxRecord
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-inbox-messages/"+pathEscape(inboxMessageID)+"/complete", update, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
