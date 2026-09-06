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
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/route"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// maxApprovalBodyBytes caps the inbound approval payload.
const maxApprovalBodyBytes = 1 << 20

// DefaultApprovalEventType is the event type an approval emits when the
// request does not name one; awaits register patterns like
// "approval:{subject}".
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
	store  store.Store
	awaits *trigger.AwaitMatcher
	logger *slog.Logger
}

// NewModule constructs the approvals module backed by the given store.
// Nil-safe: with a nil store, Register registers nothing.
func NewModule(st store.Store) *Module {
	return &Module{store: st, awaits: &trigger.AwaitMatcher{Store: st}, logger: slog.Default()}
}

func (m *Module) Register(mux route.Router) {
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
func (m *Module) postApproval(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	actor, userID, ok := sessionActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "approval requires a verified session identity")
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
	eventID, payload, err := m.emitApproval(r.Context(), ws, params, actor, userID)
	if err != nil {
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

// sessionActor resolves the verified approver identity from the session
// context: the email when the token carries one, else the immutable user id.
func sessionActor(r *http.Request) (actor, userID string, ok bool) {
	return middleware.VerifiedUserActorFromContext(r.Context())
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
// returns its id plus the marshaled decision payload. Backends without the
// appender capability journal server-side in their dispatch wiring; the
// matcher dispatch still runs (documented registration-scan gap).
func (m *Module) emitApproval(ctx context.Context, ws string, params approvalParams, actor, userID string) (string, json.RawMessage, error) {
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
	appender, ok := m.store.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		m.logger.Debug("approval journal append skipped: backend journals server-side",
			"workspace", ws, "event_id", eventID)
		return eventID, payload, nil
	}
	if _, err := appender.AppendTriggerEvent(ctx, &domain.TriggerEvent{
		WorkspaceKey:    ws,
		EventID:         eventID,
		SourceKind:      approvalSourceKind,
		SourceEventID:   eventID,
		EventType:       params.EventType,
		SubjectRef:      params.SubjectRef,
		ActorRef:        actor,
		Origin:          domain.TriggerEventOriginExternal,
		OccurredAt:      now,
		ReceivedAt:      now,
		IdempotencyKey:  approvalSourceKind + ":" + ws + ":" + eventID,
		SignatureStatus: "session",
	}); err != nil {
		return "", nil, fmt.Errorf("journal approval event: %w", err)
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
