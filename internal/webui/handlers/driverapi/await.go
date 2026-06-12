// Await-event driver ops (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW9).
//
// POST /api/workspaces/{ws}/driver/events/await — register-and-check the
// run's next await: respond {status:"satisfied"|"timed_out", event:{...}}
// inline (including idempotent replay of a finished awaitIndex) or suspend
// the run and respond {status:"suspended"}; the runner treats suspended as
// "exit now; resume re-runs from the top".
//
// GET /api/workspaces/{ws}/driver/events/awaits — the run's awaits in index
// order (terminal rows with recorded events inline, plus pending rows) for
// context rebuilding on re-entry.
//
// Both routes authenticate exactly like every driver op: the parent DriverRun
// identity headers verified through the fenced-heartbeat path (verifyParent).
// The await instance key is derived server-side from the verified run id —
// never from the body — so a workflow cannot forge another run's instance.
// Wire shapes are camelCase per the driver-op (SDK v2) convention.
package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// awaitActorList accepts the actor field as either a single JSON string or a
// string array on the wire.
type awaitActorList []string

func (a *awaitActorList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*a = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = awaitActorList{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("actor must be a string or an array of strings: %w", domain.ErrInvalid)
	}
	*a = many
	return nil
}

// normalized trims entries, drops empties and dedupes preserving order.
func (a awaitActorList) normalized() []string {
	var out []string
	seen := make(map[string]struct{}, len(a))
	for _, actor := range a {
		actor = strings.TrimSpace(actor)
		if actor == "" {
			continue
		}
		if _, dup := seen[actor]; dup {
			continue
		}
		seen[actor] = struct{}{}
		out = append(out, actor)
	}
	return out
}

// awaitEventParams is the camelCase events/await request body.
type awaitEventParams struct {
	// Pattern is the fully rendered subject-scoped key to wait for (RULE 1,
	// exact equality, no glob).
	Pattern string `json:"pattern"`
	// Actor is the optional eligible-resolver allow-list (RULE 4): a string
	// or string array.
	Actor awaitActorList `json:"actor,omitempty"`
	// TimeoutMs is the mandatory await timeout (RULE 5).
	TimeoutMs int64 `json:"timeoutMs"`
	// AwaitIndex is the 1-based ordinal of this await within the run; the
	// server derives the instance key from the verified run id.
	AwaitIndex int `json:"awaitIndex"`
}

// awaitWireEvent is the recorded resolving event on the response wire.
type awaitWireEvent struct {
	ID string `json:"id"`
	// Payload is the size-capped resume payload persisted on the satisfied
	// row, returned inline on first resolution and on every replay.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Actor is the resolving actor when the event is journaled (synthetic
	// timeout events carry none).
	Actor      string    `json:"actor,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
}

// awaitEventResponse is the events/await response: status satisfied or
// timed_out with the recorded event inline, or suspended (event absent).
type awaitEventResponse struct {
	Status      string          `json:"status"`
	InstanceKey string          `json:"instanceKey"`
	Pattern     string          `json:"pattern"`
	Deadline    time.Time       `json:"deadline"`
	Event       *awaitWireEvent `json:"event,omitempty"`
}

// awaitEvent is the events/await handler: verify the parent run owns its
// lease, then run the check-then-park op flow (internal/driver/await_op.go).
func (m *Module) awaitEvent(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[awaitEventParams](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	fence, err := id.FencingToken()
	if err != nil {
		return nil, err
	}
	outcome, err := driverpkg.AwaitEvent(ctx, m.store, driverpkg.AwaitEventOptions{
		WorkspaceKey: ws,
		RunID:        parent.RunID,
		NodeID:       id.NodeID,
		LeaseID:      id.LeaseID,
		FencingToken: fence,
		Pattern:      strings.TrimSpace(params.Pattern),
		ActorAllow:   params.Actor.normalized(),
		TimeoutMs:    params.TimeoutMs,
		AwaitIndex:   params.AwaitIndex,
	})
	if err != nil {
		return nil, err
	}
	return m.awaitEventResponse(ctx, ws, outcome), nil
}

// awaitEventResponse renders the op outcome onto the camelCase wire.
func (m *Module) awaitEventResponse(ctx context.Context, ws string, outcome *driverpkg.AwaitEventOutcome) awaitEventResponse {
	resp := awaitEventResponse{Status: outcome.Status}
	inst := outcome.Instance
	if inst == nil {
		return resp
	}
	resp.InstanceKey = inst.InstanceKey
	resp.Pattern = inst.Pattern
	resp.Deadline = inst.Deadline
	if inst.Status.IsTerminal() {
		resp.Event = m.awaitWireEvent(ctx, ws, inst)
	}
	return resp
}

// awaitWireEvent builds the recorded-event payload from the terminal await
// row, enriching actor/occurredAt from the trigger-event journal best-effort
// (synthetic timeout events are not journaled).
func (m *Module) awaitWireEvent(ctx context.Context, ws string, inst *domain.AwaitInstance) *awaitWireEvent {
	event := &awaitWireEvent{ID: inst.SatisfiedByEventID, Payload: inst.SatisfiedPayload}
	if inst.ResumedAt != nil {
		event.OccurredAt = *inst.ResumedAt
	}
	if domain.IsAwaitTimeoutEventID(inst.SatisfiedByEventID) {
		return event
	}
	if journaled, err := m.store.TriggerEvents().Get(ctx, ws, inst.SatisfiedByEventID); err == nil {
		event.Actor = journaled.ActorRef
		if !journaled.OccurredAt.IsZero() {
			event.OccurredAt = journaled.OccurredAt
		}
	}
	return event
}

// handleAwaitEvent serves POST /api/workspaces/{ws}/driver/events/await. The
// two-segment path cannot ride the generic {op} route, so it registers its
// own pattern and reuses the shared authorized-op pipeline.
func (m *Module) handleAwaitEvent(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	m.serveAuthorizedOp(w, r, m.awaitEvent, tokenID)
}

// awaitListResponse is the GET events/awaits response. AwaitInstance is
// camelCase-tagged (the driver/watch wire type).
type awaitListResponse struct {
	RunID  string                  `json:"runId"`
	Awaits []*domain.AwaitInstance `json:"awaits"`
}

// handleListAwaits serves GET /api/workspaces/{ws}/driver/events/awaits: the
// verified run's awaits in index order for re-entry context rebuilding.
func (m *Module) handleListAwaits(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	ws := r.PathValue("ws")
	id, ok := requestIdentity(w, r, tokenID)
	if !ok {
		return
	}
	parent, err := m.verifyParent(r.Context(), ws, id)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	awaits, err := driverpkg.ListRunAwaits(r.Context(), m.store, ws, parent.RunID)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, awaitListResponse{RunID: parent.RunID, Awaits: awaits})
}

// writeAwaitOpError maps the structured await sentinels onto the error
// envelope ahead of the generic domain mapping (they wrap domain.ErrInvalid,
// which would otherwise flatten them to "invalid"). Reports whether it
// handled err.
func writeAwaitOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, domain.ErrAwaitPatternUnscoped):
		writeOpError(w, http.StatusBadRequest, domain.AwaitErrCodePatternUnscoped, err.Error(), false)
	case errors.Is(err, domain.ErrAwaitTimeoutRequired):
		writeOpError(w, http.StatusBadRequest, domain.AwaitErrCodeTimeoutRequired, err.Error(), false)
	case errors.Is(err, domain.ErrAwaitInstanceKeyMalformed):
		writeOpError(w, http.StatusBadRequest, domain.AwaitErrCodeInstanceKeyMalformed, err.Error(), false)
	case errors.Is(err, domain.ErrCompositionDepthExceeded):
		writeOpError(w, http.StatusBadRequest, domain.CompositionErrCodeDepthExceeded, err.Error(), false)
	case errors.Is(err, domain.ErrAwaitActorForbidden):
		writeOpError(w, http.StatusForbidden, "await_actor_forbidden", err.Error(), false)
	case errors.Is(err, domain.ErrDriverRunAlreadyResumed):
		// Defensive: the op flow handles this inline; surfaced only if a
		// future caller lets it escape.
		writeOpError(w, http.StatusConflict, "driver_run_already_resumed", err.Error(), false)
	default:
		return false
	}
	return true
}
