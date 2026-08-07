// Package approvals serves the minimal session-authenticated approval
// endpoint (ARCHITECTURE-PROPOSAL §7 step 8; the Phase D locked decision
// closing vet A2).
//
// POST /api/workspaces/{ws}/approvals records one approval (or rejection)
// decision by the VERIFIED session user and feeds it to the await machinery:
//
//  1. The actor is NEVER request data: it is the authenticated session
//     identity the webui auth middleware verified (JWT subject/email),
//     resolved via middleware.UserIdentityFromContext. No identity, no
//     approval (fail closed — there is no anonymous or open-mode approval).
//  2. Eligible-approver check (RULE 4 surface): when pending awaits exist on
//     the rendered subject key, the actor must be in at least one await's
//     ActorAllow predicate; otherwise the request is refused (403) and NO
//     event is emitted. The dispatch matcher re-enforces the predicate per
//     instance, so this pre-check is audit/UX — the matcher stays
//     authoritative.
//  3. Journal-first (RULE 2): the approval event is appended to the
//     trigger-event journal BEFORE matcher dispatch (the same shape as the
//     run.finished lifecycle emission), so an approval granted before the
//     workflow registers its await is found by the registration scan — no
//     lost wakeup. With no pending await the event is still journaled for
//     future registrations; eligibility is then enforced by the
//     registration scan's own actor filter.
//  4. Dispatch-time matching (AW7): the matcher resolves ALL pending awaits
//     whose pattern equals the rendered key and resumes their suspended
//     runs, with the decision payload persisted on each satisfied row.
//
// A rejection rides the same pipeline: it RESOLVES the await too (the
// workflow resumes and branches on payload.decision) — approval gates wake
// on the decision, not only on consent.
package approvals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// maxApprovalBodyBytes caps the inbound approval payload.
const maxApprovalBodyBytes = 1 << 20

var errApprovalPayloadTooLarge = errors.New("approval payload exceeds await resume limit")

// DefaultApprovalEventType is the only event type the approval endpoint may
// emit. Keeping this surface approval-scoped prevents a browser session from
// forging reserved lifecycle events such as run.finished.
const DefaultApprovalEventType = "approval"

// approvalSourceKind marks journal records produced by this endpoint.
const approvalSourceKind = "approval"

// Approval decisions accepted on the wire.
const (
	DecisionApproved = "approved"
	DecisionRejected = "rejected"
)

// Module serves the workspace-scoped approval route.
type Module struct {
	store     approvalStore
	awaits    AwaitDispatcher
	journal   automation.ApprovalJournal
	authority automation.ApprovalAuthorityProvider
	logger    *slog.Logger
}

type approvalStore interface {
	Awaits() store.AwaitStore
}

// AwaitDispatcher is the Execution-backed mutation surface used after the
// approval event is journaled. Production composition must inject it; this
// module never derives mutation authority from Store.
type AwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

type Config struct {
	Store     approvalStore
	Awaits    AwaitDispatcher
	Journal   automation.ApprovalJournal
	Authority automation.ApprovalAuthorityProvider
	Logger    *slog.Logger
}

// New constructs the approvals module. Nil Store keeps route registration
// inert; nil Awaits leaves the registered route fail-closed with 503.
func New(config Config) *Module {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{
		store: config.Store, awaits: config.Awaits, journal: config.Journal,
		authority: config.Authority, logger: logger,
	}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/approvals", m.postApproval)
}

// approvalParams is the camelCase request body. The actor is deliberately
// absent: it comes from the verified session identity only.
type approvalParams struct {
	// SubjectRef is the rendered subject the approval targets (required),
	// e.g. "acme/widgets#7@shaA".
	SubjectRef string `json:"subjectRef"`
	// EventType defaults to DefaultApprovalEventType.
	EventType string `json:"eventType,omitempty"`
	// Decision is approved (default) or rejected.
	Decision string `json:"decision,omitempty"`
	// Note is an optional free-form reviewer note carried on the payload.
	Note string `json:"note,omitempty"`
}

// approvalPayload is the camelCase resume payload persisted on satisfied
// await rows and journaled with the event.
type approvalPayload struct {
	Decision   string `json:"decision"`
	EventType  string `json:"eventType"`
	SubjectRef string `json:"subjectRef"`
	// ApprovedBy is the verified actor ref (session email, or user id when
	// the session carries no email).
	ApprovedBy string `json:"approvedBy"`
	// ApprovedByUserID is always the immutable JWT subject.
	ApprovedByUserID string `json:"approvedByUserId"`
	Note             string `json:"note,omitempty"`
	OccurredAt       string `json:"occurredAt"`
}

// approvalResolution reports one await instance this approval touched.
type approvalResolution struct {
	InstanceKey string `json:"instanceKey"`
	RunID       string `json:"runId"`
	Outcome     string `json:"outcome"`
}

// approvalResponse is the camelCase response wire.
type approvalResponse struct {
	Status     string `json:"status"`
	EventID    string `json:"eventId"`
	Actor      string `json:"actor"`
	SubjectKey string `json:"subjectKey"`
	// PendingMatched counts the pending awaits on the key at decision time
	// (zero means the event was journaled for a future registration).
	PendingMatched int                  `json:"pendingMatched"`
	Resolutions    []approvalResolution `json:"resolutions,omitempty"`
}

// postApproval is the endpoint flow: verified identity -> eligible-approver
// check -> journal-first append -> matcher dispatch.
//
//nolint:funlen // The handler preserves identity, eligibility, authority, journal, and dispatch ordering.
func (m *Module) postApproval(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	actor, userID, ok := sessionActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "approval requires a verified session identity")
		return
	}
	if m.awaits == nil || m.journal == nil || m.authority == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "approval journal or await resolution is unavailable")
		return
	}
	if !m.approvalActorAllowed(w, ws, actor, userID) {
		return
	}
	params, err := decodeApproval(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	subjectKey := domain.AwaitEventKey(params.EventType, params.SubjectRef)
	pending, err := m.pendingAwaits(r.Context(), ws, subjectKey)
	if errors.Is(err, errors.ErrUnsupported) {
		writeError(w, http.StatusNotImplemented, "unsupported", "await store unavailable for this backend: "+err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if len(pending) > 0 && !actorEligible(pending, actor) {
		m.auditIneligible(ws, subjectKey, actor, userID, len(pending))
		writeError(w, http.StatusForbidden, "await_actor_forbidden",
			fmt.Sprintf("session actor %q is not an eligible approver for %q", actor, subjectKey))
		return
	}
	approvalAuth, err := m.authority.AuthorityForVerifiedSession(r.Context(), ws, actor)
	if err != nil {
		if errors.Is(err, authority.ErrAdmissionDenied) || errors.Is(err, authority.ErrInvalidScope) {
			writeError(w, http.StatusForbidden, "forbidden", "approval authority was denied")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "unavailable", "approval authority is unavailable")
		return
	}
	eventID, payload, err := m.emitApproval(r.Context(), approvalAuth, ws, params, actor, userID)
	if err != nil {
		if errors.Is(err, errApprovalPayloadTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "await_payload_too_large", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	resp := approvalResponse{
		Status: params.Decision, EventID: eventID, Actor: actor,
		SubjectKey: subjectKey, PendingMatched: len(pending),
	}
	resp.Resolutions = m.dispatchApproval(r.Context(), ws, params, eventID, actor, payload)
	writeJSON(w, http.StatusOK, resp)
}

func (m *Module) approvalActorAllowed(w http.ResponseWriter, workspace, actor, userID string) bool {
	if !eventpolicy.IsReservedSystemActorRef(actor) {
		return true
	}
	m.auditReservedActor(workspace, actor, userID)
	writeError(w, http.StatusForbidden, "reserved_actor_ref",
		fmt.Sprintf("session identity cannot use reserved internal actor %q", actor))
	return false
}

// sessionActor resolves the verified approver identity from the session
// context: the email when the token carries one, else the immutable user id.
func sessionActor(r *http.Request) (actor, userID string, ok bool) {
	identity, ok := middleware.UserIdentityFromContext(r.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" {
		return "", "", false
	}
	actor = strings.TrimSpace(identity.Email)
	if actor == "" {
		actor = strings.TrimSpace(identity.UserID)
	}
	return actor, strings.TrimSpace(identity.UserID), true
}

// decodeApproval parses and normalizes the request body.
func decodeApproval(w http.ResponseWriter, r *http.Request) (approvalParams, error) {
	var params approvalParams
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxApprovalBodyBytes)).Decode(&params); err != nil {
		return params, fmt.Errorf("decode approval body: %s", err.Error())
	}
	params.SubjectRef = strings.TrimSpace(params.SubjectRef)
	params.EventType = strings.TrimSpace(params.EventType)
	if params.EventType == "" {
		params.EventType = DefaultApprovalEventType
	}
	if params.EventType != DefaultApprovalEventType {
		return params, fmt.Errorf("eventType must be %q on the approval endpoint", DefaultApprovalEventType)
	}
	params.Decision = strings.TrimSpace(params.Decision)
	if params.Decision == "" {
		params.Decision = DecisionApproved
	}
	if params.Decision != DecisionApproved && params.Decision != DecisionRejected {
		return params, fmt.Errorf("decision must be %q or %q", DecisionApproved, DecisionRejected)
	}
	if err := domain.ValidateAwaitPattern(domain.AwaitEventKey(params.EventType, params.SubjectRef)); err != nil {
		return params, fmt.Errorf("subjectRef and eventType must render a subject-scoped key: %s", err.Error())
	}
	return params, nil
}

// pendingAwaits lists the pending awaits on the rendered key. Backends
// without await support surface errors.ErrUnsupported; other errors are
// treated the same fail-closed way by the caller.
func (m *Module) pendingAwaits(ctx context.Context, ws, subjectKey string) ([]*domain.AwaitInstance, error) {
	pending, err := m.store.Awaits().ListAwaitsByPattern(ctx, ws, subjectKey)
	if err != nil {
		return nil, err
	}
	return pending, nil
}

// actorEligible reports whether the verified actor may resolve at least one
// of the pending awaits (empty ActorAllow admits any actor).
func actorEligible(pending []*domain.AwaitInstance, actor string) bool {
	for _, inst := range pending {
		if len(inst.ActorAllow) == 0 {
			return true
		}
		for _, allowed := range inst.ActorAllow {
			if allowed == actor {
				return true
			}
		}
	}
	return false
}

// emitApproval journals the approval event (journal-first, RULE 2) and
// returns its id plus the marshaled decision payload. The appender is required:
// returning success without a durable event would reopen the registration
// race this endpoint exists to close.
func (m *Module) emitApproval(
	ctx context.Context,
	auth authority.OperatorAuthority,
	ws string,
	params approvalParams,
	actor,
	userID string,
) (string, json.RawMessage, error) {
	eventID, err := newApprovalEventID()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(approvalPayload{
		Decision: params.Decision, EventType: params.EventType, SubjectRef: params.SubjectRef,
		ApprovedBy: actor, ApprovedByUserID: userID, Note: params.Note,
		OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode approval payload: %w", err)
	}
	if len(payload) > automation.MaxApprovalPayloadBytes {
		return "", nil, fmt.Errorf("%w: %d bytes exceeds %d",
			errApprovalPayloadTooLarge, len(payload), automation.MaxApprovalPayloadBytes)
	}
	committed, err := m.journal.JournalApproval(ctx, auth, automation.JournalApprovalCommand{
		WorkspaceKey: ws, EventID: eventID, EventType: params.EventType,
		SubjectRef: params.SubjectRef, ActorRef: actor, OccurredAt: now,
		Payload: append(json.RawMessage(nil), payload...),
	})
	if err != nil {
		return "", nil, err
	}
	if committed == nil || committed.EventID != eventID {
		return "", nil, automation.ErrInvalidPersistedState
	}
	return eventID, payload, nil
}

// dispatchApproval runs the dispatch-time await matcher over the journaled
// approval. Best-effort like every post-journal hook: the event is durable,
// so matcher errors are logged and the recorded per-instance outcomes
// returned as far as they got.
func (m *Module) dispatchApproval(ctx context.Context, ws string, params approvalParams, eventID, actor string, payload json.RawMessage) []approvalResolution {
	result, err := m.awaits.Dispatch(ctx, ws, trigger.AwaitDispatchEvent{
		EventID:    eventID,
		EventType:  params.EventType,
		SourceKind: approvalSourceKind,
		Origin:     automation.EventOriginExternal,
		SubjectRef: params.SubjectRef,
		ActorRef:   actor,
		Payload:    payload,
	})
	if err != nil {
		m.logger.Warn("approval await dispatch failed",
			"workspace", ws, "event_id", eventID, "subject_ref", params.SubjectRef, "error", err)
	}
	resolutions := make([]approvalResolution, 0, len(result.Records))
	for _, record := range result.Records {
		resolutions = append(resolutions, approvalResolution{
			InstanceKey: record.InstanceKey,
			RunID:       record.RunID,
			Outcome:     string(record.Outcome),
		})
	}
	return resolutions
}

// auditIneligible records the refused approval attempt (vet A2/A3 audit).
func (m *Module) auditIneligible(ws, subjectKey, actor, userID string, pending int) {
	m.logger.Warn("approval refused: session actor not eligible",
		"workspace", ws,
		"subject_key", subjectKey,
		"actor_ref", actor,
		"user_id", userID,
		"pending_awaits", pending,
	)
}

// auditReservedActor records an authenticated session that collided with the
// server-owned actor namespace before any approval event was journaled.
func (m *Module) auditReservedActor(ws, actor, userID string) {
	m.logger.Warn("approval refused: session actor uses reserved internal identity",
		"workspace", ws,
		"actor_ref", actor,
		"user_id", userID,
	)
}

// newApprovalEventID mints a random approval event id.
func newApprovalEventID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("mint approval event id: %w", err)
	}
	return "approval-" + hex.EncodeToString(buf[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}
