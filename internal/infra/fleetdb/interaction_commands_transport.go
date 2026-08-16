package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// InteractionMutationTransport contains only the compound Phase 5
// Interaction commands. Owned commands validate the complete AgentSession
// lease generation in the same FleetDB transaction as the mutation.
type InteractionMutationTransport interface {
	StartInteractionSession(context.Context, InteractionSessionStartInput) (*InteractionSessionStartResult, error)
	RecoverInteractionSessionStart(context.Context, InteractionSessionStartRecoveryInput) (*InteractionSessionStartResult, error)
	GetInteractionSession(context.Context, string, string) (*interaction.SessionRecord, error)
	ListInteractionSessions(context.Context, InteractionSessionQuery) ([]*interaction.SessionRecord, error)
	PatchInteractionSession(context.Context, InteractionSessionPatchInput) (*InteractionSessionMutationResult, error)
	HeartbeatInteractionSession(context.Context, InteractionSessionHeartbeatInput) (*InteractionSessionMutationResult, error)
	FinishInteractionSession(context.Context, InteractionSessionFinishInput) (*InteractionSessionMutationResult, error)
	ForceInterruptInteractionSession(context.Context, InteractionSessionForceInterruptInput) (*InteractionSessionForceInterruptResult, error)
	ListRecoverableInteractionSessions(context.Context, string) ([]*interaction.SessionRecord, error)
	InterruptInteractionSessionIfLeaseMissing(context.Context, string, string) (*InteractionSessionInterruptResult, error)

	CreateInteractionTerminal(context.Context, InteractionTerminalCreateInput) (*interaction.TerminalRecord, error)
	GetInteractionTerminal(context.Context, string, string) (*interaction.TerminalRecord, error)
	UpdateInteractionTerminal(context.Context, InteractionTerminalUpdateInput) (*interaction.TerminalRecord, error)

	EnqueueInteractionInbox(context.Context, InteractionInboxEnqueueInput) (*interaction.InboxRecord, error)
	ClaimInteractionInbox(context.Context, InteractionInboxClaimInput) (*interaction.InboxRecord, error)
	CompleteInteractionInbox(context.Context, InteractionInboxCompleteInput) (*interaction.InboxRecord, error)

	ListInteractionActivity(context.Context, string, string, int) ([]InteractionActivity, error)
}

type InteractionSessionQuery struct {
	WorkspaceKey string
	AgentID      string
	WorkItemID   string
	Limit        int
}

type InteractionSessionStartInput struct {
	WorkspaceKey    string
	SessionID       string
	AgentID         string
	NodeID          string
	Kind            string
	TaskID          string
	TerminalID      string
	ParentSessionID string
	Phase           string
	Attempt         int
	LeaseID         string
	LeaseTTL        time.Duration
	Metadata        map[string]string
}

type InteractionSessionStartResult struct {
	Session *interaction.SessionRecord `json:"session"`
	Lease   *interaction.LeaseRecord   `json:"lease"`
	Token   string                     `json:"token"`
}

type InteractionSessionStartRecoveryInput struct {
	Original                  InteractionSessionStartInput
	ExpectedLeaseID           string
	ExpectedLeaseFencingToken int64
	ReplacementLeaseID        string
	ReplacementLeaseTTL       time.Duration
}

type InteractionSessionHeartbeatInput struct {
	Proof    InteractionSessionAuthorityProof
	Phase    string
	LeaseTTL time.Duration
}

type InteractionSessionPatchInput struct {
	Proof                InteractionSessionAuthorityProof
	Phase                *string
	MetadataUpserts      map[string]string
	MetadataRemovals     []string
	TranscriptArtifactID *string
}

type InteractionSessionFinishInput struct {
	Proof                InteractionSessionAuthorityProof
	Status               string
	Summary              string
	ErrorClass           string
	ExitCode             *int
	TranscriptArtifactID string
}

type InteractionSessionMutationResult struct {
	Session  *interaction.SessionRecord  `json:"session"`
	Terminal *interaction.TerminalRecord `json:"terminal,omitempty"`
	Lease    *interaction.LeaseRecord    `json:"lease"`
}

type InteractionSessionInterruptResult struct {
	Session *interaction.SessionRecord `json:"session"`
	Changed bool                       `json:"changed"`
}

type InteractionSessionForceInterruptInput struct {
	WorkspaceKey              string
	SessionID                 string
	AgentID                   string
	TerminalID                string
	ExpectedLeaseID           string
	ExpectedLeaseFencingToken int64
	StreamRef                 string
	TerminalTab               string
	Reason                    string
}

type InteractionSessionForceInterruptResult struct {
	Session  *interaction.SessionRecord  `json:"session"`
	Terminal *interaction.TerminalRecord `json:"terminal"`
	Lease    *interaction.LeaseRecord    `json:"lease"`
	Changed  bool                        `json:"changed"`
}

type InteractionTerminalCreateInput struct {
	Proof       InteractionSessionAuthorityProof
	TerminalID  string
	TaskID      string
	Title       string
	Kind        string
	PTYProvider string
	StreamRef   string
	Metadata    map[string]string
}

type InteractionTerminalUpdateInput struct {
	Proof                InteractionSessionAuthorityProof
	TerminalID           string
	Status               string
	StreamRef            *string
	TranscriptArtifactID *string
	AttachedClients      *int
}

type InteractionInboxEnqueueInput struct {
	WorkspaceKey      string
	MessageID         string
	TargetAgentID     string
	SessionID         string
	Body              string
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
	DedupeKey         string
}

type InteractionInboxClaimInput struct {
	Proof    InteractionSessionAuthorityProof
	LeaseTTL time.Duration
}

type InteractionInboxCompleteInput struct {
	Proof             InteractionSessionAuthorityProof
	InboxMessageID    string
	Attempt           int
	Status            string
	DeliveredThreadID string
	ErrorClass        string
}

type InteractionActivity struct {
	WorkspaceKey string     `json:"workspace_key"`
	AgentID      string     `json:"agent_id"`
	Kind         string     `json:"kind"`
	SourceID     string     `json:"source_id"`
	TaskID       string     `json:"task_id,omitempty"`
	Status       string     `json:"status"`
	Summary      string     `json:"summary,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

var _ InteractionMutationTransport = (*interactionStore)(nil)

func (store *interactionStore) StartInteractionSession(
	ctx context.Context,
	input InteractionSessionStartInput,
) (*InteractionSessionStartResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionStartInput(input); err != nil {
		return nil, err
	}
	request := struct {
		SessionID       string            `json:"session_id"`
		AgentID         string            `json:"agent_id"`
		NodeID          string            `json:"node_id"`
		Kind            string            `json:"kind"`
		TaskID          string            `json:"task_id,omitempty"`
		TerminalID      string            `json:"terminal_id,omitempty"`
		ParentSessionID string            `json:"parent_session_id,omitempty"`
		Phase           string            `json:"phase,omitempty"`
		Attempt         int               `json:"attempt,omitempty"`
		LeaseID         string            `json:"lease_id"`
		LeaseTTLSeconds int               `json:"lease_ttl_seconds"`
		Metadata        map[string]string `json:"metadata,omitempty"`
	}{
		SessionID: input.SessionID, AgentID: input.AgentID, NodeID: input.NodeID,
		Kind: input.Kind, TaskID: input.TaskID, TerminalID: input.TerminalID,
		ParentSessionID: input.ParentSessionID, Phase: input.Phase, Attempt: input.Attempt,
		LeaseID: input.LeaseID, LeaseTTLSeconds: interactionTTLSeconds(input.LeaseTTL),
		Metadata: cloneInteractionMetadata(input.Metadata),
	}
	var response InteractionSessionStartResult
	err := store.client.Do(
		ctx,
		http.MethodPost,
		interactionBasePath(input.WorkspaceKey)+"/sessions",
		request,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("start interaction session", err)
	}
	if err := validateInteractionStartResult(&response); err != nil {
		response.Token = ""
		return nil, err
	}
	return &response, nil
}

//nolint:funlen // Keep recovery authority, exact-generation fencing, durable transition, and response validation in one transaction.
func (store *interactionStore) RecoverInteractionSessionStart(
	ctx context.Context,
	input InteractionSessionStartRecoveryInput,
) (*InteractionSessionStartResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionStartInput(input.Original); err != nil {
		return nil, err
	}
	for _, value := range []string{
		input.ExpectedLeaseID,
		input.ReplacementLeaseID,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return nil, ErrInteractionInvalid
		}
	}
	if input.ExpectedLeaseFencingToken <= 0 ||
		input.ReplacementLeaseID == input.ExpectedLeaseID ||
		input.ReplacementLeaseTTL <= 0 {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		AgentID                    string            `json:"agent_id"`
		NodeID                     string            `json:"node_id"`
		Kind                       string            `json:"kind"`
		TaskID                     string            `json:"task_id,omitempty"`
		TerminalID                 string            `json:"terminal_id,omitempty"`
		ParentSessionID            string            `json:"parent_session_id,omitempty"`
		Phase                      string            `json:"phase,omitempty"`
		Attempt                    int               `json:"attempt,omitempty"`
		Metadata                   map[string]string `json:"metadata,omitempty"`
		ExpectedLeaseID            string            `json:"expected_lease_id"`
		ExpectedLeaseFencingToken  int64             `json:"expected_lease_fencing_token"`
		ReplacementLeaseID         string            `json:"replacement_lease_id"`
		ReplacementLeaseTTLSeconds int               `json:"replacement_lease_ttl_seconds"`
	}{
		AgentID: input.Original.AgentID, NodeID: input.Original.NodeID,
		Kind: input.Original.Kind, TaskID: input.Original.TaskID,
		TerminalID:      input.Original.TerminalID,
		ParentSessionID: input.Original.ParentSessionID,
		Phase:           input.Original.Phase, Attempt: input.Original.Attempt,
		Metadata:                   cloneInteractionMetadata(input.Original.Metadata),
		ExpectedLeaseID:            input.ExpectedLeaseID,
		ExpectedLeaseFencingToken:  input.ExpectedLeaseFencingToken,
		ReplacementLeaseID:         input.ReplacementLeaseID,
		ReplacementLeaseTTLSeconds: interactionTTLSeconds(input.ReplacementLeaseTTL),
	}
	var response InteractionSessionStartResult
	err := store.client.Do(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Original.WorkspaceKey)+"/sessions/"+
			pathEscape(input.Original.SessionID)+"/recover-start",
		request,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError(
			"recover interaction session start",
			err,
		)
	}
	if err := validateInteractionStartResult(&response); err != nil {
		response.Token = ""
		return nil, err
	}
	return &response, nil
}

func (store *interactionStore) GetInteractionSession(
	ctx context.Context,
	workspace,
	sessionID string,
) (*interaction.SessionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionCoordinates(workspace, sessionID); err != nil {
		return nil, err
	}
	var response interaction.SessionRecord
	err := store.client.Do(
		ctx,
		http.MethodGet,
		interactionBasePath(workspace)+"/sessions/"+pathEscape(sessionID),
		nil,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("get interaction session", err)
	}
	return &response, nil
}

func (store *interactionStore) ListInteractionSessions(
	ctx context.Context,
	query InteractionSessionQuery,
) ([]*interaction.SessionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.WorkItemID = strings.TrimSpace(query.WorkItemID)
	if query.WorkspaceKey == "" || query.Limit < 1 || query.Limit > 100 {
		return nil, ErrInteractionInvalid
	}
	values, err := store.client.ListAgentSessions(ctx, query.WorkspaceKey, interaction.AgentSessionFilter{
		AgentID: query.AgentID,
		TaskID:  query.WorkItemID,
		Limit:   query.Limit,
	})
	if err != nil {
		return nil, mapInteractionAuthorityError("list interaction sessions", err)
	}
	return values, nil
}

func (store *interactionStore) HeartbeatInteractionSession(
	ctx context.Context,
	input InteractionSessionHeartbeatInput,
) (*InteractionSessionMutationResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if input.LeaseTTL <= 0 {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		AgentID         string `json:"agent_id"`
		NodeID          string `json:"node_id"`
		LeaseID         string `json:"lease_id"`
		FencingToken    int64  `json:"fencing_token"`
		Phase           string `json:"phase,omitempty"`
		LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
	}{
		AgentID: input.Proof.AgentID, NodeID: input.Proof.NodeID,
		LeaseID: input.Proof.LeaseID, FencingToken: input.Proof.FencingToken,
		Phase: input.Phase, LeaseTTLSeconds: interactionTTLSeconds(input.LeaseTTL),
	}
	var response InteractionSessionMutationResult
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Proof.WorkspaceKey)+"/sessions/"+
			pathEscape(input.Proof.SessionID)+"/heartbeat",
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("heartbeat interaction session", err)
	}
	return &response, nil
}

func (store *interactionStore) PatchInteractionSession(
	ctx context.Context,
	input InteractionSessionPatchInput,
) (*InteractionSessionMutationResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	request := map[string]any{
		"agent_id": input.Proof.AgentID, "node_id": input.Proof.NodeID,
		"lease_id": input.Proof.LeaseID, "fencing_token": input.Proof.FencingToken,
	}
	bodyPtr(request, "phase", input.Phase)
	if len(input.MetadataUpserts) > 0 {
		request["metadata_upserts"] = cloneInteractionMetadata(input.MetadataUpserts)
	}
	if len(input.MetadataRemovals) > 0 {
		request["metadata_removals"] = append([]string(nil), input.MetadataRemovals...)
	}
	bodyPtr(request, "transcript_artifact_id", input.TranscriptArtifactID)
	if len(request) == 4 {
		return nil, ErrInteractionInvalid
	}
	var response InteractionSessionMutationResult
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPatch,
		interactionBasePath(input.Proof.WorkspaceKey)+"/sessions/"+
			pathEscape(input.Proof.SessionID),
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("patch interaction session", err)
	}
	return &response, nil
}

func (store *interactionStore) FinishInteractionSession(
	ctx context.Context,
	input InteractionSessionFinishInput,
) (*InteractionSessionMutationResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Status) == "" {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		AgentID            string `json:"agent_id"`
		NodeID             string `json:"node_id"`
		LeaseID            string `json:"lease_id"`
		FencingToken       int64  `json:"fencing_token"`
		Status             string `json:"status"`
		Summary            string `json:"summary,omitempty"`
		ErrorClass         string `json:"error_class,omitempty"`
		ExitCode           *int   `json:"exit_code,omitempty"`
		TranscriptArtifact string `json:"transcript_artifact_id,omitempty"`
	}{
		AgentID: input.Proof.AgentID, NodeID: input.Proof.NodeID,
		LeaseID: input.Proof.LeaseID, FencingToken: input.Proof.FencingToken,
		Status: input.Status, Summary: input.Summary,
		ErrorClass: input.ErrorClass, ExitCode: input.ExitCode,
		TranscriptArtifact: input.TranscriptArtifactID,
	}
	var response InteractionSessionMutationResult
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Proof.WorkspaceKey)+"/sessions/"+
			pathEscape(input.Proof.SessionID)+"/finish",
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("finish interaction session", err)
	}
	return &response, nil
}

//nolint:funlen // Keep forced-interrupt authority, lease fencing, durable transition, and response validation in one transaction.
func (store *interactionStore) ForceInterruptInteractionSession(
	ctx context.Context,
	input InteractionSessionForceInterruptInput,
) (*InteractionSessionForceInterruptResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionCoordinates(input.WorkspaceKey, input.SessionID); err != nil {
		return nil, err
	}
	for _, value := range []string{
		input.AgentID,
		input.TerminalID,
		input.ExpectedLeaseID,
		input.StreamRef,
		input.TerminalTab,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return nil, ErrInteractionInvalid
		}
	}
	if input.Reason != strings.TrimSpace(input.Reason) {
		return nil, ErrInteractionInvalid
	}
	if input.ExpectedLeaseFencingToken <= 0 {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		AgentID                   string `json:"agent_id"`
		TerminalID                string `json:"terminal_id"`
		ExpectedLeaseID           string `json:"expected_lease_id"`
		ExpectedLeaseFencingToken int64  `json:"expected_lease_fencing_token"`
		StreamRef                 string `json:"stream_ref"`
		TerminalTab               string `json:"terminal_tab"`
		Reason                    string `json:"reason,omitempty"`
	}{
		AgentID: input.AgentID, TerminalID: input.TerminalID,
		ExpectedLeaseID:           input.ExpectedLeaseID,
		ExpectedLeaseFencingToken: input.ExpectedLeaseFencingToken,
		StreamRef:                 input.StreamRef, TerminalTab: input.TerminalTab,
		Reason: input.Reason,
	}
	var response InteractionSessionForceInterruptResult
	err := store.client.Do(
		ctx,
		http.MethodPost,
		interactionBasePath(input.WorkspaceKey)+"/sessions/"+
			pathEscape(input.SessionID)+"/force-interrupt",
		request,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError(
			"force interrupt interaction session",
			err,
		)
	}
	return &response, nil
}

func (store *interactionStore) ListRecoverableInteractionSessions(
	ctx context.Context,
	workspace string,
) ([]*interaction.SessionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if strings.TrimSpace(workspace) == "" || workspace != strings.TrimSpace(workspace) {
		return nil, ErrInteractionInvalid
	}
	var response struct {
		Sessions []*interaction.SessionRecord `json:"agent_sessions"`
		Count    int                          `json:"count"`
	}
	err := store.client.Do(
		ctx,
		http.MethodGet,
		interactionBasePath(workspace)+"/sessions/recoverable",
		nil,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("list recoverable interaction sessions", err)
	}
	if response.Sessions == nil {
		response.Sessions = []*interaction.SessionRecord{}
	}
	return response.Sessions, nil
}

func (store *interactionStore) InterruptInteractionSessionIfLeaseMissing(
	ctx context.Context,
	workspace,
	sessionID string,
) (*InteractionSessionInterruptResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionCoordinates(workspace, sessionID); err != nil {
		return nil, err
	}
	var response InteractionSessionInterruptResult
	err := store.client.Do(
		ctx,
		http.MethodPost,
		interactionBasePath(workspace)+"/sessions/"+pathEscape(sessionID)+
			"/interrupt-if-lease-missing",
		nil,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("interrupt interaction session if lease missing", err)
	}
	return &response, nil
}

func (store *interactionStore) CreateInteractionTerminal(
	ctx context.Context,
	input InteractionTerminalCreateInput,
) (*interaction.TerminalRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.TerminalID) == "" || input.TerminalID != strings.TrimSpace(input.TerminalID) {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		TerminalID   string            `json:"terminal_id"`
		SessionID    string            `json:"session_id"`
		AgentID      string            `json:"agent_id"`
		NodeID       string            `json:"node_id"`
		LeaseID      string            `json:"lease_id"`
		FencingToken int64             `json:"fencing_token"`
		TaskID       string            `json:"task_id,omitempty"`
		Title        string            `json:"title,omitempty"`
		Kind         string            `json:"kind,omitempty"`
		PTYProvider  string            `json:"pty_provider,omitempty"`
		StreamRef    string            `json:"stream_ref,omitempty"`
		Metadata     map[string]string `json:"metadata,omitempty"`
	}{
		TerminalID: input.TerminalID, SessionID: input.Proof.SessionID,
		AgentID: input.Proof.AgentID, NodeID: input.Proof.NodeID,
		LeaseID: input.Proof.LeaseID, FencingToken: input.Proof.FencingToken,
		TaskID: input.TaskID, Title: input.Title, Kind: input.Kind,
		PTYProvider: input.PTYProvider, StreamRef: input.StreamRef,
		Metadata: cloneInteractionMetadata(input.Metadata),
	}
	var response interaction.TerminalRecord
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Proof.WorkspaceKey)+"/terminals",
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("create interaction terminal", err)
	}
	return &response, nil
}

func (store *interactionStore) GetInteractionTerminal(
	ctx context.Context,
	workspace,
	terminalID string,
) (*interaction.TerminalRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionCoordinates(workspace, terminalID); err != nil {
		return nil, err
	}
	var response interaction.TerminalRecord
	err := store.client.Do(
		ctx,
		http.MethodGet,
		interactionBasePath(workspace)+"/terminals/"+pathEscape(terminalID),
		nil,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("get interaction terminal", err)
	}
	return &response, nil
}

func (store *interactionStore) UpdateInteractionTerminal(
	ctx context.Context,
	input InteractionTerminalUpdateInput,
) (*interaction.TerminalRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.TerminalID) == "" || input.TerminalID != strings.TrimSpace(input.TerminalID) ||
		strings.TrimSpace(input.Status) == "" {
		return nil, ErrInteractionInvalid
	}
	request := map[string]any{
		"session_id": input.Proof.SessionID, "agent_id": input.Proof.AgentID,
		"node_id": input.Proof.NodeID, "lease_id": input.Proof.LeaseID,
		"fencing_token": input.Proof.FencingToken, "status": input.Status,
	}
	bodyPtr(request, "stream_ref", input.StreamRef)
	bodyPtr(request, "transcript_artifact_id", input.TranscriptArtifactID)
	bodyPtr(request, "attached_clients", input.AttachedClients)
	var response interaction.TerminalRecord
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPatch,
		interactionBasePath(input.Proof.WorkspaceKey)+"/terminals/"+pathEscape(input.TerminalID),
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("update interaction terminal", err)
	}
	return &response, nil
}

func (store *interactionStore) EnqueueInteractionInbox(
	ctx context.Context,
	input InteractionInboxEnqueueInput,
) (*interaction.InboxRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if strings.TrimSpace(input.WorkspaceKey) == "" || input.WorkspaceKey != strings.TrimSpace(input.WorkspaceKey) ||
		strings.TrimSpace(input.MessageID) == "" || input.MessageID != strings.TrimSpace(input.MessageID) ||
		strings.TrimSpace(input.TargetAgentID) == "" || input.TargetAgentID != strings.TrimSpace(input.TargetAgentID) ||
		strings.TrimSpace(input.Body) == "" {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		MessageID         string `json:"message_id"`
		TargetAgentID     string `json:"target_agent_id"`
		SessionID         string `json:"session_id,omitempty"`
		Body              string `json:"body"`
		SourceKind        string `json:"source_kind,omitempty"`
		SourceRef         string `json:"source_ref,omitempty"`
		DriverRunID       string `json:"driver_run_id,omitempty"`
		TaskRunID         string `json:"task_run_id,omitempty"`
		TriggerEventID    string `json:"trigger_event_id,omitempty"`
		TriggerDeliveryID string `json:"trigger_delivery_id,omitempty"`
		DedupeKey         string `json:"dedupe_key,omitempty"`
	}{
		MessageID: input.MessageID, TargetAgentID: input.TargetAgentID,
		SessionID: input.SessionID, Body: input.Body, SourceKind: input.SourceKind,
		SourceRef: input.SourceRef, DriverRunID: input.DriverRunID, TaskRunID: input.TaskRunID,
		TriggerEventID: input.TriggerEventID, TriggerDeliveryID: input.TriggerDeliveryID,
		DedupeKey: input.DedupeKey,
	}
	var response interaction.InboxRecord
	err := store.client.Do(
		ctx,
		http.MethodPost,
		interactionBasePath(input.WorkspaceKey)+"/inbox",
		request,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("enqueue interaction inbox", err)
	}
	return &response, nil
}

func (store *interactionStore) ClaimInteractionInbox(
	ctx context.Context,
	input InteractionInboxClaimInput,
) (*interaction.InboxRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if input.LeaseTTL <= 0 {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		SessionID       string `json:"session_id"`
		AgentID         string `json:"agent_id"`
		NodeID          string `json:"node_id"`
		LeaseID         string `json:"lease_id"`
		FencingToken    int64  `json:"fencing_token"`
		LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
	}{
		SessionID: input.Proof.SessionID, AgentID: input.Proof.AgentID,
		NodeID: input.Proof.NodeID, LeaseID: input.Proof.LeaseID,
		FencingToken:    input.Proof.FencingToken,
		LeaseTTLSeconds: interactionTTLSeconds(input.LeaseTTL),
	}
	var response interaction.InboxRecord
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Proof.WorkspaceKey)+"/inbox/claim-next",
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("claim interaction inbox", err)
	}
	return &response, nil
}

func (store *interactionStore) CompleteInteractionInbox(
	ctx context.Context,
	input InteractionInboxCompleteInput,
) (*interaction.InboxRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(input.Proof); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.InboxMessageID) == "" ||
		input.InboxMessageID != strings.TrimSpace(input.InboxMessageID) ||
		input.Attempt <= 0 || strings.TrimSpace(input.Status) == "" {
		return nil, ErrInteractionInvalid
	}
	request := struct {
		SessionID         string `json:"session_id"`
		AgentID           string `json:"agent_id"`
		NodeID            string `json:"node_id"`
		LeaseID           string `json:"lease_id"`
		FencingToken      int64  `json:"fencing_token"`
		Attempt           int    `json:"attempt"`
		Status            string `json:"status"`
		DeliveredThreadID string `json:"delivered_thread_id,omitempty"`
		ErrorClass        string `json:"error_class,omitempty"`
	}{
		SessionID: input.Proof.SessionID, AgentID: input.Proof.AgentID,
		NodeID: input.Proof.NodeID, LeaseID: input.Proof.LeaseID,
		FencingToken: input.Proof.FencingToken, Attempt: input.Attempt, Status: input.Status,
		DeliveredThreadID: input.DeliveredThreadID, ErrorClass: input.ErrorClass,
	}
	var response interaction.InboxRecord
	err := store.client.DoWithHeaders(
		ctx,
		http.MethodPost,
		interactionBasePath(input.Proof.WorkspaceKey)+"/inbox/"+
			pathEscape(input.InboxMessageID)+"/complete",
		request,
		&response,
		interactionLeaseTokenHeaders(input.Proof.LeaseToken),
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("complete interaction inbox", err)
	}
	return &response, nil
}

func (store *interactionStore) ListInteractionActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
) ([]InteractionActivity, error) {
	return store.listInteractionActivity(ctx, workspace, agentID, limit, "")
}

func (store *interactionStore) ListInteractionSessionActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
) ([]InteractionActivity, error) {
	return store.listInteractionActivity(ctx, workspace, agentID, limit, "sessions")
}

func (store *interactionStore) ListInteractionExecutionActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
) ([]InteractionActivity, error) {
	return store.listInteractionActivity(ctx, workspace, agentID, limit, "execution")
}

func (store *interactionStore) listInteractionActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
	kind string,
) ([]InteractionActivity, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionCoordinates(workspace, agentID); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, ErrInteractionInvalid
	}
	query := url.Values{"agent_id": []string{agentID}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var response struct {
		Activity []InteractionActivity `json:"activity"`
		Count    int                   `json:"count"`
	}
	path := interactionBasePath(workspace) + "/activity"
	operation := "list combined interaction activity"
	if kind != "" {
		path += "/" + kind
		operation = "list interaction " + kind + " activity"
	}
	err := store.client.Do(
		ctx,
		http.MethodGet,
		withQuery(path, query),
		nil,
		&response,
	)
	if err != nil {
		return nil, mapInteractionAuthorityError(operation, err)
	}
	if response.Activity == nil {
		response.Activity = []InteractionActivity{}
	}
	return response.Activity, nil
}

func validateInteractionStartInput(input InteractionSessionStartInput) error {
	for _, value := range []string{
		input.WorkspaceKey, input.SessionID, input.AgentID, input.NodeID,
		input.Kind, input.LeaseID,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return ErrInteractionInvalid
		}
	}
	if input.Attempt < 0 || input.LeaseTTL <= 0 {
		return ErrInteractionInvalid
	}
	return nil
}

func validateInteractionCoordinates(workspace, id string) error {
	if strings.TrimSpace(workspace) == "" || workspace != strings.TrimSpace(workspace) ||
		strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
		return ErrInteractionInvalid
	}
	return nil
}

func interactionBasePath(workspace string) string {
	return "/api/v1/" + pathEscape(workspace) + "/interaction"
}

func interactionTTLSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int((value + time.Second - 1) / time.Second)
}

func interactionLeaseTokenHeaders(token string) map[string]string {
	return map[string]string{"X-Agent-Lease-Token": token}
}

func cloneInteractionMetadata(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func validateInteractionStartResult(result *InteractionSessionStartResult) error {
	if result == nil || result.Session == nil || result.Lease == nil ||
		strings.TrimSpace(result.Token) == "" {
		return fmt.Errorf("FleetDB returned an incomplete Interaction start: %w", ErrInteractionInvalidPersistedState)
	}
	return nil
}
