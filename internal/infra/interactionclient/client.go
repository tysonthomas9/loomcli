package interactionclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const (
	sessionTokenHeader               = "X-Loom-Session-Token" //nolint:gosec // HTTP header name, not credential material.
	sessionAgentHeader               = "X-Loom-Session-Agent-ID"
	sessionTerminalHeader            = "X-Loom-Session-Terminal-ID"
	sessionNodeHeader                = "X-Loom-Session-Node-ID"
	sessionLeaseHeader               = "X-Loom-Session-Lease-ID"
	sessionFenceHeader               = "X-Loom-Session-Fencing-Token"
	transcriptMetadataHeader         = "X-Loom-Transcript-Metadata"
	maxResponseBytes                 = 1 << 20
	maxTranscriptBytes               = (64 << 20) - (1 << 20)
	maxTranscriptMetadataHeaderBytes = 64 << 10
	requestTimeout                   = 15 * time.Second
)

// SessionProof is the credential-free identity portion of one exact
// SessionEnvelope. WorkspaceKey and SessionID are also carried in the request
// path so the server can reject a body/path mismatch before issuing authority.
type SessionProof struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
}

// Client is a least-privilege child-side Interaction transport. The raw lease
// credential is retained only in this process's memory and copied only into
// the request header. It is never encoded in a body, URL, response, or error.
type Client struct {
	baseURL *url.URL
	proof   SessionProof
	http    *http.Client

	mu     sync.RWMutex
	token  []byte
	closed bool
}

// NewFromEnv reads one exact SessionEnvelope plus the server-owned Interaction
// API URL. If none of those variables are present it returns (nil, false, nil),
// which is the explicit standalone-lead mode. A partial or malformed envelope
// fails closed. The raw token variable is unset on every registered path.
func NewFromEnv() (*Client, bool, error) {
	return newFromEnvironment(
		os.LookupEnv,
		os.Unsetenv,
		&http.Client{Timeout: requestTimeout},
	)
}

//nolint:funlen // Keep environment validation, credential stripping, proof construction, and client setup in one fail-closed bootstrap.
func newFromEnvironment(
	lookup func(string) (string, bool),
	unset func(string) error,
	httpClient *http.Client,
) (*Client, bool, error) {
	names := []string{
		interaction.EnvSessionWorkspace,
		interaction.EnvSessionID,
		interaction.EnvSessionAgentID,
		interaction.EnvSessionTerminalID,
		interaction.EnvSessionNodeID,
		interaction.EnvSessionLeaseID,
		interaction.EnvSessionFence,
		interaction.EnvSessionToken,
		interaction.EnvInteractionAPIURL,
	}
	values := make(map[string]string, len(names))
	present := false
	for _, name := range names {
		value, ok := lookup(name)
		if ok {
			present = true
		}
		values[name] = strings.TrimSpace(value)
	}
	if !present {
		return nil, false, nil
	}

	rawToken := values[interaction.EnvSessionToken]
	// Remove the raw token from the inherited process environment immediately.
	// The parsed client owns the only retained copy.
	if err := unset(interaction.EnvSessionToken); err != nil {
		return nil, true, fmt.Errorf("clear session credential environment: %w", err)
	}
	required := []string{
		interaction.EnvSessionWorkspace,
		interaction.EnvSessionID,
		interaction.EnvSessionAgentID,
		interaction.EnvSessionTerminalID,
		interaction.EnvSessionNodeID,
		interaction.EnvSessionLeaseID,
		interaction.EnvSessionFence,
		interaction.EnvSessionToken,
		interaction.EnvInteractionAPIURL,
	}
	for _, name := range required {
		if values[name] == "" {
			return nil, true, fmt.Errorf("incomplete session envelope: %s is required", name)
		}
	}
	if !validLeaseToken(rawToken) {
		return nil, true, errors.New("invalid session credential")
	}
	fence, err := strconv.ParseInt(values[interaction.EnvSessionFence], 10, 64)
	if err != nil || fence <= 0 {
		return nil, true, fmt.Errorf("invalid session fencing token")
	}
	baseURL, err := parseBaseURL(values[interaction.EnvInteractionAPIURL])
	if err != nil {
		return nil, true, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL: baseURL,
		proof: SessionProof{
			WorkspaceKey: values[interaction.EnvSessionWorkspace],
			SessionID:    values[interaction.EnvSessionID],
			AgentID:      values[interaction.EnvSessionAgentID],
			TerminalID:   values[interaction.EnvSessionTerminalID],
			NodeID:       values[interaction.EnvSessionNodeID],
			LeaseID:      values[interaction.EnvSessionLeaseID],
			FencingToken: fence,
		},
		http:  httpClient,
		token: []byte(rawToken),
	}, true, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return nil, fmt.Errorf("invalid Interaction API URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid Interaction API URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func validLeaseToken(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (client *Client) Proof() SessionProof {
	if client == nil {
		return SessionProof{}
	}
	return client.proof
}

func (client *Client) HeartbeatSession(
	ctx context.Context,
	command interaction.HeartbeatSessionCommand,
) error {
	body := client.proofBody()
	body["phase"] = strings.TrimSpace(command.Phase)
	if command.LeaseTTL > 0 {
		body["lease_ttl_seconds"] = int64(command.LeaseTTL / time.Second)
	}
	return client.do(ctx, http.MethodPost, client.sessionPath()+"/heartbeat", body, nil)
}

func (client *Client) PatchSessionRuntimeContext(
	ctx context.Context,
	command interaction.PatchSessionCommand,
) error {
	body := client.proofBody()
	if command.Phase != nil {
		phase := strings.TrimSpace(*command.Phase)
		body["phase"] = phase
	}
	if len(command.MetadataUpserts) > 0 {
		body["metadata_upserts"] = cloneMetadata(command.MetadataUpserts)
	}
	if len(command.MetadataRemovals) > 0 {
		body["metadata_removals"] = append([]string(nil), command.MetadataRemovals...)
	}
	if command.TranscriptArtifactID != nil {
		body["transcript_artifact_id"] = strings.TrimSpace(*command.TranscriptArtifactID)
	}
	return client.do(ctx, http.MethodPatch, client.sessionPath(), body, nil)
}

func (client *Client) PublishTranscript(
	ctx context.Context,
	command interaction.PublishTranscriptCommand,
) error {
	if len(command.Content) == 0 || len(command.Content) > maxTranscriptBytes {
		return fmt.Errorf("canonical transcript must contain 1..%d bytes", maxTranscriptBytes)
	}
	metadata, err := json.Marshal(command.Metadata)
	if err != nil {
		return fmt.Errorf("encode transcript metadata: %w", err)
	}
	if len(metadata) > maxTranscriptMetadataHeaderBytes {
		return errors.New("transcript metadata exceeded limit")
	}
	return client.doTranscript(ctx, command.Content, string(metadata))
}

func (client *Client) FinishSession(ctx context.Context, command interaction.FinishSessionCommand) error {
	body := client.proofBody()
	body["status"] = strings.TrimSpace(string(command.Status))
	body["summary"] = strings.TrimSpace(command.Summary)
	body["error_class"] = strings.TrimSpace(command.ErrorClass)
	if command.ExitCode != nil {
		body["exit_code"] = *command.ExitCode
	}
	if artifactID := strings.TrimSpace(command.TranscriptArtifactID); artifactID != "" {
		body["transcript_artifact_id"] = artifactID
	}
	return client.do(ctx, http.MethodPost, client.sessionPath()+"/finish", body, nil)
}

func (client *Client) ClaimNextInbox(
	ctx context.Context,
	command interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	body := client.proofBody()
	if command.LeaseTTL > 0 {
		body["lease_ttl_seconds"] = int64(command.LeaseTTL / time.Second)
	}
	var raw json.RawMessage
	if err := client.do(
		ctx,
		http.MethodPost,
		client.sessionPath()+"/inbox/claim-next",
		body,
		&raw,
	); err != nil {
		return nil, err
	}
	return decodeInboxMessage(raw)
}

func (client *Client) CompleteInbox(
	ctx context.Context,
	command interaction.CompleteInboxCommand,
) error {
	messageID := strings.TrimSpace(command.MessageID)
	if messageID == "" {
		return errors.New("inbox message ID is required")
	}
	body := client.proofBody()
	body["attempt"] = command.Attempt
	body["status"] = strings.TrimSpace(string(command.Status))
	body["delivered_thread_id"] = strings.TrimSpace(command.DeliveredThreadID)
	body["error_class"] = strings.TrimSpace(command.ErrorClass)
	path := client.sessionPath() + "/inbox/" + url.PathEscape(messageID) + "/complete"
	return client.do(ctx, http.MethodPost, path, body, nil)
}

func (client *Client) workspacePath() string {
	return "/api/workspaces/" + url.PathEscape(client.proof.WorkspaceKey)
}

func (client *Client) sessionPath() string {
	return client.workspacePath() + "/interaction/sessions/" + url.PathEscape(client.proof.SessionID)
}

func (client *Client) proofBody() map[string]any {
	return map[string]any{
		"agent_id":      client.proof.AgentID,
		"terminal_id":   client.proof.TerminalID,
		"node_id":       client.proof.NodeID,
		"lease_id":      client.proof.LeaseID,
		"fencing_token": client.proof.FencingToken,
	}
}

func (client *Client) do(
	ctx context.Context,
	method string,
	path string,
	body map[string]any,
	out *json.RawMessage,
) error {
	if client == nil || client.baseURL == nil || client.http == nil {
		return errors.New("interaction session client is unavailable")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode Interaction request: %w", err)
	}
	requestURL := strings.TrimRight(client.baseURL.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build Interaction request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	token, err := client.tokenCopy()
	if err != nil {
		return err
	}
	req.Header.Set(sessionTokenHeader, string(token))
	clear(token)
	defer req.Header.Del(sessionTokenHeader)
	response, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("interaction request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Interaction response: %w", err)
	}
	if len(content) > maxResponseBytes {
		return errors.New("interaction response exceeded limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusNotFound {
			return domain.ErrNotFound
		}
		return fmt.Errorf("interaction request failed with status %d", response.StatusCode)
	}
	if out != nil {
		*out = append((*out)[:0], content...)
	}
	return nil
}

func (client *Client) doTranscript(ctx context.Context, content []byte, metadata string) error {
	if client == nil || client.baseURL == nil || client.http == nil {
		return errors.New("interaction session client is unavailable")
	}
	requestURL := strings.TrimRight(client.baseURL.String(), "/") + client.sessionPath() + "/transcript"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("build Interaction transcript request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set(sessionAgentHeader, client.proof.AgentID)
	req.Header.Set(sessionTerminalHeader, client.proof.TerminalID)
	req.Header.Set(sessionNodeHeader, client.proof.NodeID)
	req.Header.Set(sessionLeaseHeader, client.proof.LeaseID)
	req.Header.Set(sessionFenceHeader, strconv.FormatInt(client.proof.FencingToken, 10))
	req.Header.Set(transcriptMetadataHeader, metadata)
	token, err := client.tokenCopy()
	if err != nil {
		return err
	}
	req.Header.Set(sessionTokenHeader, string(token))
	clear(token)
	defer req.Header.Del(sessionTokenHeader)
	response, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("interaction transcript request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Interaction transcript response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return errors.New("interaction transcript response exceeded limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusNotFound {
			return domain.ErrNotFound
		}
		return fmt.Errorf("interaction transcript request failed with status %d", response.StatusCode)
	}
	return nil
}

func (client *Client) tokenCopy() ([]byte, error) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.closed || len(client.token) == 0 {
		return nil, errors.New("interaction session client is closed")
	}
	return append([]byte(nil), client.token...), nil
}

// Close destroys the retained raw session credential. It is safe to call more
// than once and must run after the final owner-fenced request.
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil
	}
	clear(client.token)
	client.token = nil
	client.closed = true
	return nil
}

//nolint:funlen // Keep strict envelope decoding, identity validation, and payload normalization in one untrusted-message boundary.
func decodeInboxMessage(raw json.RawMessage) (*interaction.InboxMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, domain.ErrNotFound
	}
	var wrapped struct {
		Message json.RawMessage `json:"message"`
	}
	payload := raw
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Message) > 0 {
		payload = wrapped.Message
	}
	var direct interaction.InboxMessage
	if err := json.Unmarshal(payload, &direct); err != nil {
		return nil, fmt.Errorf("decode Interaction inbox response: %w", err)
	}
	if strings.TrimSpace(direct.MessageID) != "" {
		return &direct, nil
	}
	var wire struct {
		WorkspaceKey      string                  `json:"workspace_key"`
		MessageID         string                  `json:"message_id"`
		Cursor            int64                   `json:"cursor"`
		TargetAgentID     string                  `json:"target_agent_id"`
		SessionID         string                  `json:"session_id"`
		Body              string                  `json:"body"`
		Status            interaction.InboxStatus `json:"status"`
		SourceKind        string                  `json:"source_kind"`
		SourceRef         string                  `json:"source_ref"`
		DriverRunID       string                  `json:"driver_run_id"`
		TaskRunID         string                  `json:"task_run_id"`
		TriggerEventID    string                  `json:"trigger_event_id"`
		TriggerDeliveryID string                  `json:"trigger_delivery_id"`
		DedupeKey         string                  `json:"dedupe_key"`
		Attempt           int                     `json:"attempt"`
		ClaimedBy         string                  `json:"claimed_by"`
		ClaimExpiresAt    *time.Time              `json:"claim_expires_at"`
		ErrorClass        string                  `json:"error_class"`
		DeliveredThreadID string                  `json:"delivered_thread_id"`
		DeliveredAt       *time.Time              `json:"delivered_at"`
		CreatedAt         time.Time               `json:"created_at"`
		UpdatedAt         time.Time               `json:"updated_at"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, fmt.Errorf("decode Interaction inbox response: %w", err)
	}
	if strings.TrimSpace(wire.MessageID) == "" {
		return nil, errors.New("interaction inbox response omitted message identity")
	}
	return &interaction.InboxMessage{
		WorkspaceKey:      wire.WorkspaceKey,
		MessageID:         wire.MessageID,
		Cursor:            wire.Cursor,
		TargetAgentID:     wire.TargetAgentID,
		SessionID:         wire.SessionID,
		Body:              wire.Body,
		Status:            wire.Status,
		SourceKind:        wire.SourceKind,
		SourceRef:         wire.SourceRef,
		DriverRunID:       wire.DriverRunID,
		TaskRunID:         wire.TaskRunID,
		TriggerEventID:    wire.TriggerEventID,
		TriggerDeliveryID: wire.TriggerDeliveryID,
		DedupeKey:         wire.DedupeKey,
		Attempt:           wire.Attempt,
		ClaimedBy:         wire.ClaimedBy,
		ClaimExpiresAt:    wire.ClaimExpiresAt,
		ErrorClass:        wire.ErrorClass,
		DeliveredThreadID: wire.DeliveredThreadID,
		DeliveredAt:       wire.DeliveredAt,
		CreatedAt:         wire.CreatedAt,
		UpdatedAt:         wire.UpdatedAt,
	}, nil
}

func cloneMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
