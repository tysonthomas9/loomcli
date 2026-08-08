// Package interactionmanagement exposes Interaction-owned read projections to
// the Web UI. AgentSession and batch-run persistence remain distinct; this
// adapter publishes only the capability's combined activity query.
package interactionmanagement

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type ActivityAPI interface {
	ListActivity(
		context.Context,
		authority.OperatorAuthority,
		interaction.ActivityQuery,
	) ([]interaction.Activity, error)
}

type SessionCommandAPI interface {
	PatchSession(context.Context, authority.SessionAuthority, interaction.PatchSessionCommand) (*interaction.AgentSession, error)
	PublishTranscript(context.Context, authority.SessionAuthority, interaction.PublishTranscriptCommand) (*interaction.AgentSession, error)
	HeartbeatSession(context.Context, authority.SessionAuthority, interaction.HeartbeatSessionCommand) (*interaction.AgentSession, error)
	FinishSession(context.Context, authority.SessionAuthority, interaction.FinishSessionCommand) (*interaction.AgentSession, error)
	UpdateTerminal(context.Context, authority.SessionAuthority, interaction.UpdateTerminalCommand) (*interaction.TerminalSession, error)
	ClaimInbox(context.Context, authority.SessionAuthority, interaction.ClaimInboxCommand) (*interaction.InboxMessage, error)
	CompleteInbox(context.Context, authority.SessionAuthority, interaction.CompleteInboxCommand) (*interaction.InboxMessage, error)
}

type SessionAuthorityResolver interface {
	ResolveSessionAuthority(
		context.Context,
		authority.Action,
		interaction.SessionAuthorityProof,
	) (authority.SessionAuthority, error)
}

type Config struct {
	Interaction        ActivityAPI
	Authority          workflowcataloghttp.OperatorAuthorityResolver
	SessionAuthorities SessionAuthorityResolver
}

type Module struct {
	interaction        ActivityAPI
	sessionCommands    SessionCommandAPI
	authority          workflowcataloghttp.OperatorAuthorityResolver
	sessionAuthorities SessionAuthorityResolver
}

func New(config Config) *Module {
	commands, _ := config.Interaction.(SessionCommandAPI)
	return &Module{
		interaction: config.Interaction, sessionCommands: commands,
		authority: config.Authority, sessionAuthorities: config.SessionAuthorities,
	}
}

func (module *Module) Register(mux *http.ServeMux) {
	if module == nil || mux == nil {
		return
	}
	mux.HandleFunc(
		"GET /api/workspaces/{ws}/agents/{id}/activity",
		module.listAgentActivity,
	)
	mux.HandleFunc(
		"PATCH /api/workspaces/{ws}/interaction/sessions/{sessionId}",
		module.patchSession,
	)
	mux.HandleFunc(
		"POST /api/workspaces/{ws}/interaction/sessions/{sessionId}/heartbeat",
		module.heartbeatSession,
	)
	mux.HandleFunc(
		"POST /api/workspaces/{ws}/interaction/sessions/{sessionId}/transcript",
		module.publishTranscript,
	)
	mux.HandleFunc(
		"POST /api/workspaces/{ws}/interaction/sessions/{sessionId}/finish",
		module.finishSession,
	)
	mux.HandleFunc(
		"POST /api/workspaces/{ws}/interaction/sessions/{sessionId}/inbox/claim-next",
		module.claimInbox,
	)
	mux.HandleFunc(
		"POST /api/workspaces/{ws}/interaction/sessions/{sessionId}/inbox/{messageId}/complete",
		module.completeInbox,
	)
}

const (
	sessionTokenHeader        = "X-Loom-Session-Token" //nolint:gosec // credential header name
	sessionAgentHeader        = "X-Loom-Session-Agent-ID"
	sessionTerminalHeader     = "X-Loom-Session-Terminal-ID"
	sessionNodeHeader         = "X-Loom-Session-Node-ID"
	sessionLeaseHeader        = "X-Loom-Session-Lease-ID"
	sessionFenceHeader        = "X-Loom-Session-Fencing-Token"
	transcriptMetadataHeader  = "X-Loom-Transcript-Metadata"
	maxSessionRequestBytes    = 1 << 20
	maxSessionTranscriptBytes = (64 << 20) - (1 << 20)
	defaultSessionLeaseTTL    = 2 * time.Minute
	maxSessionLeaseTTL        = 30 * time.Minute
	expectedSessionTokenSize  = 32
)

type sessionProofRequest struct {
	AgentID      string `json:"agent_id"`
	TerminalID   string `json:"terminal_id,omitempty"`
	NodeID       string `json:"node_id"`
	LeaseID      string `json:"lease_id"`
	FencingToken int64  `json:"fencing_token"`
}

type patchSessionRequest struct {
	sessionProofRequest
	Phase                *string           `json:"phase,omitempty"`
	MetadataUpserts      map[string]string `json:"metadata_upserts,omitempty"`
	MetadataRemovals     []string          `json:"metadata_removals,omitempty"`
	TranscriptArtifactID *string           `json:"transcript_artifact_id,omitempty"`
}

type heartbeatSessionRequest struct {
	sessionProofRequest
	Phase           string `json:"phase,omitempty"`
	LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
}

type finishSessionRequest struct {
	sessionProofRequest
	Status               interaction.SessionStatus `json:"status"`
	Summary              string                    `json:"summary,omitempty"`
	ErrorClass           string                    `json:"error_class,omitempty"`
	ExitCode             *int                      `json:"exit_code,omitempty"`
	TranscriptArtifactID string                    `json:"transcript_artifact_id,omitempty"`
}

type claimInboxRequest struct {
	sessionProofRequest
	LeaseTTLSeconds int `json:"lease_ttl_seconds"`
}

type completeInboxRequest struct {
	sessionProofRequest
	Attempt           int                     `json:"attempt"`
	Status            interaction.InboxStatus `json:"status"`
	DeliveredThreadID string                  `json:"delivered_thread_id,omitempty"`
	ErrorClass        string                  `json:"error_class,omitempty"`
}

func (module *Module) patchSession(response http.ResponseWriter, request *http.Request) {
	var input patchSessionRequest
	if !module.decodeSessionRequest(response, request, &input) {
		return
	}
	auth, ok := module.resolveSession(
		response, request, interaction.ActionPatchSession, input.sessionProofRequest,
	)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	value, err := module.sessionCommands.PatchSession(
		request.Context(),
		auth,
		interaction.PatchSessionCommand{
			WorkspaceKey: canonicalWorkspaceFromRequest(request), SessionID: request.PathValue("sessionId"),
			Phase: input.Phase, MetadataUpserts: input.MetadataUpserts,
			MetadataRemovals:     input.MetadataRemovals,
			TranscriptArtifactID: input.TranscriptArtifactID,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (module *Module) heartbeatSession(response http.ResponseWriter, request *http.Request) {
	var input heartbeatSessionRequest
	if !module.decodeSessionRequest(response, request, &input) {
		return
	}
	ttl, ok := sessionLeaseTTL(response, input.LeaseTTLSeconds)
	if !ok {
		return
	}
	auth, ok := module.resolveSession(
		response, request, interaction.ActionHeartbeatSession, input.sessionProofRequest,
	)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	value, err := module.sessionCommands.HeartbeatSession(
		request.Context(),
		auth,
		interaction.HeartbeatSessionCommand{
			WorkspaceKey: canonicalWorkspaceFromRequest(request), SessionID: request.PathValue("sessionId"),
			Phase: input.Phase, LeaseTTL: ttl,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (module *Module) publishTranscript(response http.ResponseWriter, request *http.Request) {
	if module == nil || module.sessionCommands == nil || module.sessionAuthorities == nil {
		writeMappedError(response, interaction.ErrUnavailable)
		return
	}
	input, ok := readSessionProofHeaders(response, request)
	if !ok {
		return
	}
	auth, ok := module.resolveSession(response, request, interaction.ActionPublishTranscript, input)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/x-ndjson" {
		writeError(response, http.StatusUnsupportedMediaType, "invalid", "canonical transcript content type is required")
		return
	}
	metadata, ok := readTranscriptMetadata(response, request)
	if !ok {
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maxSessionTranscriptBytes))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid", "canonical transcript exceeds the supported size")
		return
	}
	value, err := module.sessionCommands.PublishTranscript(
		request.Context(),
		auth,
		interaction.PublishTranscriptCommand{
			WorkspaceKey: canonicalWorkspaceFromRequest(request),
			SessionID:    request.PathValue("sessionId"),
			Content:      content,
			Metadata:     metadata,
		},
	)
	clear(content)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func readTranscriptMetadata(response http.ResponseWriter, request *http.Request) (map[string]string, bool) {
	raw := strings.TrimSpace(request.Header.Get(transcriptMetadataHeader))
	request.Header.Del(transcriptMetadataHeader)
	if raw == "" {
		return map[string]string{}, true
	}
	if len(raw) > 64<<10 {
		writeError(response, http.StatusBadRequest, "invalid", "transcript metadata exceeds the supported size")
		return nil, false
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", "invalid transcript metadata")
		return nil, false
	}
	return metadata, true
}

func readSessionProofHeaders(response http.ResponseWriter, request *http.Request) (sessionProofRequest, bool) {
	fence, err := strconv.ParseInt(strings.TrimSpace(request.Header.Get(sessionFenceHeader)), 10, 64)
	input := sessionProofRequest{
		AgentID:      strings.TrimSpace(request.Header.Get(sessionAgentHeader)),
		TerminalID:   strings.TrimSpace(request.Header.Get(sessionTerminalHeader)),
		NodeID:       strings.TrimSpace(request.Header.Get(sessionNodeHeader)),
		LeaseID:      strings.TrimSpace(request.Header.Get(sessionLeaseHeader)),
		FencingToken: fence,
	}
	for _, name := range []string{
		sessionAgentHeader, sessionTerminalHeader, sessionNodeHeader,
		sessionLeaseHeader, sessionFenceHeader,
	} {
		request.Header.Del(name)
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid", "complete canonical session proof is required")
		return sessionProofRequest{}, false
	}
	return input, true
}

func (module *Module) finishSession(response http.ResponseWriter, request *http.Request) {
	var input finishSessionRequest
	if !module.decodeSessionRequest(response, request, &input) {
		return
	}
	raw, proof, ok := readSessionProof(response, request, input.sessionProofRequest)
	if !ok {
		return
	}
	defer clear(raw)
	auth, ok := module.resolveSessionProof(
		response,
		request,
		interaction.ActionFinishSession,
		proof,
		raw,
	)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	value, err := module.sessionCommands.FinishSession(
		request.Context(),
		auth,
		interaction.FinishSessionCommand{
			WorkspaceKey: proof.WorkspaceKey, SessionID: request.PathValue("sessionId"),
			Status: input.Status, Summary: input.Summary, ErrorClass: input.ErrorClass,
			ExitCode: input.ExitCode, TranscriptArtifactID: input.TranscriptArtifactID,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (module *Module) claimInbox(response http.ResponseWriter, request *http.Request) {
	var input claimInboxRequest
	if !module.decodeSessionRequest(response, request, &input) {
		return
	}
	ttl, ok := sessionLeaseTTL(response, input.LeaseTTLSeconds)
	if !ok {
		return
	}
	auth, ok := module.resolveSession(
		response, request, interaction.ActionClaimInbox, input.sessionProofRequest,
	)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	value, err := module.sessionCommands.ClaimInbox(
		request.Context(),
		auth,
		interaction.ClaimInboxCommand{
			WorkspaceKey: canonicalWorkspaceFromRequest(request), AgentID: input.AgentID,
			SessionID: request.PathValue("sessionId"), LeaseTTL: ttl,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (module *Module) completeInbox(response http.ResponseWriter, request *http.Request) {
	var input completeInboxRequest
	if !module.decodeSessionRequest(response, request, &input) {
		return
	}
	auth, ok := module.resolveSession(
		response, request, interaction.ActionCompleteInbox, input.sessionProofRequest,
	)
	if !ok {
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	value, err := module.sessionCommands.CompleteInbox(
		request.Context(),
		auth,
		interaction.CompleteInboxCommand{
			WorkspaceKey: canonicalWorkspaceFromRequest(request), MessageID: request.PathValue("messageId"),
			SessionID: request.PathValue("sessionId"), Attempt: input.Attempt, Status: input.Status,
			DeliveredThreadID: input.DeliveredThreadID, ErrorClass: input.ErrorClass,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (module *Module) decodeSessionRequest(
	response http.ResponseWriter,
	request *http.Request,
	value any,
) bool {
	if module == nil || module.sessionCommands == nil || module.sessionAuthorities == nil {
		writeMappedError(response, interaction.ErrUnavailable)
		return false
	}
	err := serverhandler.DecodeOneJSON(response, request, value, serverhandler.JSONDecodeOptions{
		MaxBytes: maxSessionRequestBytes, DisallowUnknownFields: true,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		writeError(response, http.StatusBadRequest, "invalid", "Interaction request must contain exactly one JSON object")
		return false
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid", "invalid Interaction session JSON")
		return false
	}
	return true
}

func (module *Module) resolveSession(
	response http.ResponseWriter,
	request *http.Request,
	action authority.Action,
	input sessionProofRequest,
) (authority.SessionAuthority, bool) {
	raw, proof, ok := readSessionProof(response, request, input)
	if !ok {
		return authority.SessionAuthority{}, false
	}
	defer clear(raw)
	return module.resolveSessionProof(response, request, action, proof, raw)
}

func readSessionProof(
	response http.ResponseWriter,
	request *http.Request,
	input sessionProofRequest,
) ([]byte, interaction.SessionAuthorityProof, bool) {
	workspace := canonicalWorkspaceFromRequest(request)
	sessionID := strings.TrimSpace(request.PathValue("sessionId"))
	raw := []byte(strings.TrimSpace(request.Header.Get(sessionTokenHeader)))
	request.Header.Del(sessionTokenHeader)
	if workspace == "" || sessionID == "" || input.AgentID == "" || input.NodeID == "" ||
		input.LeaseID == "" || input.FencingToken <= 0 || !validSessionToken(raw) {
		clear(raw)
		writeError(response, http.StatusBadRequest, "invalid", "complete canonical session proof is required")
		return nil, interaction.SessionAuthorityProof{}, false
	}
	proof := interaction.SessionAuthorityProof{
		WorkspaceKey: workspace, SessionID: sessionID,
		AgentID: strings.TrimSpace(input.AgentID), TerminalID: strings.TrimSpace(input.TerminalID),
		NodeID: strings.TrimSpace(input.NodeID), LeaseID: strings.TrimSpace(input.LeaseID),
		FencingToken: input.FencingToken,
	}
	return raw, proof, true
}

func (module *Module) resolveSessionProof(
	response http.ResponseWriter,
	request *http.Request,
	action authority.Action,
	proof interaction.SessionAuthorityProof,
	raw []byte,
) (authority.SessionAuthority, bool) {
	token := interaction.NewLeaseToken(raw)
	proof.Token = token
	auth, err := module.sessionAuthorities.ResolveSessionAuthority(request.Context(), action, proof)
	token.Close()
	if err != nil {
		writeMappedError(response, err)
		return authority.SessionAuthority{}, false
	}
	return auth, true
}

func validSessionToken(raw []byte) bool {
	if len(raw) != expectedSessionTokenSize*2 {
		return false
	}
	decoded := make([]byte, expectedSessionTokenSize)
	_, err := hex.Decode(decoded, raw)
	clear(decoded)
	if err != nil {
		return false
	}
	for _, value := range raw {
		if value >= 'A' && value <= 'F' {
			return false
		}
	}
	return true
}

func sessionLeaseTTL(response http.ResponseWriter, seconds int) (time.Duration, bool) {
	if seconds == 0 {
		return defaultSessionLeaseTTL, true
	}
	if seconds < 1 || seconds > int(maxSessionLeaseTTL/time.Second) {
		writeError(
			response,
			http.StatusBadRequest,
			"invalid",
			"lease_ttl_seconds must be between 1 and "+strconv.Itoa(int(maxSessionLeaseTTL/time.Second)),
		)
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func (module *Module) listAgentActivity(response http.ResponseWriter, request *http.Request) {
	workspace := canonicalWorkspaceFromRequest(request)
	agentID := strings.TrimSpace(request.PathValue("id"))
	if workspace == "" || agentID == "" {
		writeError(response, http.StatusBadRequest, "invalid", "canonical workspace and agent id are required")
		return
	}
	limit, ok := serverhandler.ParseRunLimit(response, request)
	if !ok {
		return
	}
	if module == nil || module.interaction == nil || module.authority == nil {
		writeMappedError(response, interaction.ErrUnavailable)
		return
	}
	auth, err := module.authority.ResolveOperatorAuthority(
		request,
		workspace,
		interaction.ActionReadActivity,
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	activity, err := module.interaction.ListActivity(
		request.Context(),
		auth,
		interaction.ActivityQuery{
			WorkspaceKey: workspace,
			AgentID:      agentID,
			Limit:        limit,
		},
	)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	if activity == nil {
		activity = []interaction.Activity{}
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"activity": activity,
		"count":    len(activity),
	})
}

func canonicalWorkspaceFromRequest(request *http.Request) string {
	if request == nil {
		return ""
	}
	return strings.TrimSpace(middleware.WorkspaceFromContext(request.Context()))
}

func writeMappedError(response http.ResponseWriter, err error) {
	if classification, ok := serverhandler.ClassifyAuthenticationAuthorityError(err); ok {
		message := "operator authentication required"
		if classification.Status == http.StatusForbidden {
			message = "operator is not allowed to read this workspace"
		}
		writeError(response, classification.Status, classification.Code, message)
		return
	}
	switch {
	case errors.Is(err, interaction.ErrNotOwner):
		writeError(response, http.StatusForbidden, "forbidden", "operator is not allowed to read this workspace")
	case errors.Is(err, interaction.ErrInvalid):
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, interaction.ErrNotFound):
		writeError(response, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, interaction.ErrConflict),
		errors.Is(err, interaction.ErrInvalidTransition):
		writeError(response, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, interaction.ErrUnavailable):
		writeError(response, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "internal", "Interaction request failed")
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "error": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
