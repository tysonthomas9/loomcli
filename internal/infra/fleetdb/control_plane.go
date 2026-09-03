package fleetdb

import (
	"context"
	"fmt"
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
		TaskRunID       string                    `json:"task_run_id,omitempty"`
		InvocationKey   string                    `json:"invocation_key,omitempty"`
		TerminalID      string                    `json:"terminal_id,omitempty"`
		ParentSessionID string                    `json:"parent_session_id,omitempty"`
		Status          domain.AgentSessionStatus `json:"status,omitempty"`
		Phase           string                    `json:"phase,omitempty"`
		Attempt         int                       `json:"attempt,omitempty"`
		Tags            []string                  `json:"tags,omitempty"`
		StartedAt       time.Time                 `json:"started_at,omitempty"`
		Metadata        map[string]string         `json:"metadata,omitempty"`
	}{
		SessionID:       in.SessionID,
		AgentID:         in.AgentID,
		NodeID:          in.NodeID,
		Kind:            in.Kind,
		TaskID:          in.TaskID,
		TaskRunID:       in.TaskRunID,
		InvocationKey:   in.InvocationKey,
		TerminalID:      in.TerminalID,
		ParentSessionID: in.ParentSessionID,
		Status:          in.Status,
		Phase:           in.Phase,
		Attempt:         in.Attempt,
		Tags:            in.Tags,
		StartedAt:       in.StartedAt,
		Metadata:        in.Metadata,
	}
	var out domain.AgentSession
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-sessions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentSessionStore) Open(ctx context.Context, run store.SessionRunContext, descriptor store.SessionDescriptor) (store.SessionRef, error) {
	if err := store.ValidateSessionDescriptor(descriptor); err != nil {
		return store.SessionRef{}, err
	}
	if run.WorkspaceKey == "" || run.TaskRunID == "" || run.Attempt <= 0 {
		return store.SessionRef{}, fmt.Errorf("session run context is incomplete: %w", domain.ErrInvalid)
	}
	body := struct {
		TaskRunID       string                  `json:"task_run_id"`
		Attempt         int                     `json:"attempt"`
		FencingToken    int64                   `json:"fencing_token,omitempty"`
		DriverRunID     string                  `json:"driver_run_id,omitempty"`
		DriverStepID    string                  `json:"driver_step_id,omitempty"`
		InvocationKey   string                  `json:"invocation_key"`
		Backend         string                  `json:"backend"`
		Model           string                  `json:"model"`
		ParentSessionID string                  `json:"parent_session_id,omitempty"`
		Kind            domain.AgentSessionKind `json:"kind,omitempty"`
		Tags            []string                `json:"tags,omitempty"`
		Metadata        map[string]string       `json:"metadata,omitempty"`
	}{TaskRunID: run.TaskRunID, Attempt: run.Attempt, FencingToken: run.FencingToken, DriverRunID: run.DriverRunID, DriverStepID: run.DriverStepID, InvocationKey: descriptor.InvocationKey, Backend: descriptor.Backend, Model: descriptor.Model, ParentSessionID: descriptor.ParentSessionID, Kind: descriptor.Kind, Tags: descriptor.Tags, Metadata: descriptor.Metadata}
	var session domain.AgentSession
	path := "/api/v1/" + pathEscape(run.WorkspaceKey) + "/agent-sessions/open"
	if err := s.client.do(ctx, "POST", path, body, &session); err != nil {
		return store.SessionRef{}, err
	}
	return store.SessionRef{WorkspaceKey: session.WorkspaceKey, SessionID: session.SessionID, Attempt: session.Attempt}, nil
}

func (s *agentSessionStore) Get(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	var out domain.AgentSession
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-sessions/"+pathEscape(sessionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func agentSessionListQuery(filter store.AgentSessionFilter) url.Values {
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
	if filter.TaskRunID != "" {
		q.Set("task_run_id", filter.TaskRunID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Attempt != nil {
		q.Set("attempt", strconv.Itoa(*filter.Attempt))
	}
	if filter.NonTerminal {
		q.Set("non_terminal", "true")
	}
	if filter.Kind != "" {
		q.Set("kind", string(filter.Kind))
	}
	if filter.ParentSessionID != "" {
		q.Set("parent_session_id", filter.ParentSessionID)
	}
	if filter.Since != nil {
		q.Set("since", filter.Since.UTC().Format(time.RFC3339))
	}
	if filter.Until != nil {
		q.Set("until", filter.Until.UTC().Format(time.RFC3339))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	return q
}

func (s *agentSessionStore) List(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-sessions", agentSessionListQuery(filter))
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

func (s *agentSessionStore) ListPage(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, int, error) {
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-sessions", agentSessionListQuery(filter))
	var resp struct {
		AgentSessions []*domain.AgentSession `json:"agent_sessions"`
		Total         *int                   `json:"total"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Total == nil {
		return nil, 0, fmt.Errorf("fleet-db must be upgraded: agent-sessions list response is missing total for server-side session time filtering: %w", store.ErrServerCapability)
	}
	if resp.AgentSessions == nil {
		resp.AgentSessions = []*domain.AgentSession{}
	}
	return resp.AgentSessions, *resp.Total, nil
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

func (s *agentSessionStore) Finalize(ctx context.Context, ref store.SessionRef, outcome store.SessionOutcome) (*domain.AgentSession, error) {
	if err := store.ValidateSessionOutcome(outcome); err != nil {
		return nil, err
	}
	body := struct {
		Status                domain.AgentSessionStatus `json:"status"`
		ExitCode              *int                      `json:"exit_code,omitempty"`
		Summary               string                    `json:"summary,omitempty"`
		ErrorClass            string                    `json:"error_class,omitempty"`
		TranscriptRef         string                    `json:"transcript_ref,omitempty"`
		DriverRunnerSessionID string                    `json:"driver_runner_session_id,omitempty"`
		Usage                 struct {
			Tokens  *int64   `json:"tokens,omitempty"`
			CostUSD *float64 `json:"cost_usd,omitempty"`
		} `json:"usage,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}{Status: outcome.Status, ExitCode: outcome.ExitCode, Summary: outcome.Summary, ErrorClass: outcome.ErrorClass, TranscriptRef: outcome.TranscriptRef, DriverRunnerSessionID: outcome.DriverRunnerSessionID, Metadata: outcome.Metadata}
	body.Usage.Tokens = outcome.Usage.Tokens
	body.Usage.CostUSD = outcome.Usage.CostUSD
	var session domain.AgentSession
	path := "/api/v1/" + pathEscape(ref.WorkspaceKey) + "/agent-sessions/" + pathEscape(ref.SessionID) + "/finalize"
	if err := s.client.do(ctx, "POST", path, body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func ttlSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	return int(ttl.Round(time.Second) / time.Second)
}

// workerStore renews/removes fleet-db worker registrations over HTTP.
type workerStore struct{ client *Client }

var _ store.WorkerStore = (*workerStore)(nil)

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
var _ store.ArtifactContentReader = (*artifactStore)(nil)

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

func (s *artifactStore) ReadContent(ctx context.Context, ws, artifactID string) ([]byte, error) {
	return s.client.doBytes(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/artifacts/"+pathEscape(artifactID)+"/content")
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
	bodyPtr(body, "agent_id", patch.AgentID)
	bodyPtr(body, "session_id", patch.SessionID)
	bodyPtr(body, "terminal_id", patch.TerminalID)
	bodyPtr(body, "task_id", patch.TaskID)
	bodyPtr(body, "owner_type", patch.OwnerType)
	bodyPtr(body, "owner_id", patch.OwnerID)
	bodyPtr(body, "type", patch.Type)
	bodyPtr(body, "uri", patch.URI)
	bodyPtr(body, "summary", patch.Summary)
	bodyPtr(body, "mime_type", patch.MIMEType)
	bodyPtr(body, "size_bytes", patch.SizeBytes)
	bodyPtr(body, "checksum", patch.Checksum)
	bodyPtr(body, "content_hash", patch.ContentHash)
	bodyPtr(body, "visibility", patch.Visibility)
	bodyPtr(body, "redaction_status", patch.RedactionStatus)
	bodyPtr(body, "durable_status", patch.DurableStatus)
	bodyPtr(body, "metadata", patch.Metadata)
	bodyTimeRFC3339NanoPtr(body, "finalized_at", patch.FinalizedAt)
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
