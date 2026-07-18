package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Internal-event loopback source (router v2, chunk C14).
//
// TRANSPORT DECISION: the blueprint sketched this source as a subscriber on
// fleet-db's journal SSE stream. That shape only works against the fleet-db
// backend and races the watch-client surface that is still moving, so the
// landed shape is the simpler one that works against BOTH stores: an internal
// EMIT path on loom serve. Workflows reach it through the driver-op HTTP API
// ("emit-event", run-scoped auth), and in-process server components call
// Emit directly. Either way every workflow-originated event re-enters the
// router through this single component, which stamps provenance and enforces
// the structural self-trigger guard before dispatching via
// DispatchTriggerRouteV2. Per the locked provenance decision the guard is
// structural — origin (external|workflow|system) + hop_depth with a hard cap
// — not an actor-name convention.
//
// PROVENANCE PLACEMENT: both stores' route-dispatch lane deliberately stamps
// the persisted TriggerEvent origin=external hop_depth=0 server-side so
// ingress callers cannot forge provenance (fleet-db accepts-and-ignores
// origin/hop_depth on that wire). Until a trusted dispatch lane exists, the
// loopback therefore carries provenance in two places it fully owns: the
// dispatch payload envelope ({"origin","hopDepth","parentEventId",...}, so
// bindings and audit can filter) and a serve-local hop-depth ledger keyed by
// the loopback idempotency key (so chained re-triggers accumulate depth and
// the cap actually trips). Only loom serve runs the loopback (invariant 1),
// so the single-process ledger sees every internal hop; a serve restart
// resets chain depths, bounded again by the cap on the next chain.

// DefaultInternalEventHopDepthCap bounds workflow re-trigger chains, mirroring
// fleet-db's models.DefaultTriggerEventHopDepthCap: a workflow-originated
// event carries hop_depth = parent+1 and is dropped once the depth would
// exceed the cap.
const DefaultInternalEventHopDepthCap = 4

// internalHopDepthCapEnv overrides the default hop depth cap when set to a
// positive integer (loomcli twin of fleet-db's FLEET_TRIGGER_HOP_DEPTH_CAP).
const internalHopDepthCapEnv = "LOOM_TRIGGER_HOP_DEPTH_CAP"

// ErrInternalHopDepthExceeded mirrors fleet-db's
// models.ErrTriggerHopDepthExceeded for the loomcli loopback lane.
var ErrInternalHopDepthExceeded = errors.New("internal trigger event hop depth cap exceeded")

// DropReasonHopDepthExceeded marks an emission dropped by the structural
// self-trigger guard. Dropped emissions are NOT errors: the emitting workflow
// succeeded, the chain just reached its depth budget, so the loopback audits
// and swallows instead of failing the caller into a retry loop.
const DropReasonHopDepthExceeded = "hop_depth_exceeded"

// InternalRouteKeyPrefix scopes loopback dispatches: an internal event with
// normalized type "issue.created" enters the router on route key
// "internal.issue.created", so bindings opt into internal events explicitly
// (exact RouteKey or an internal.* pattern) and external ingress lanes can
// never collide with the internal namespace.
const InternalRouteKeyPrefix = "internal."

// internalSignatureStatus is stamped on loopback dispatches: there is no
// inbound signature to verify, the event was produced inside the platform.
const internalSignatureStatus = "internal"

// maxInternalHopDepthLedgerEntries bounds the in-memory provenance ledger
// (FIFO eviction). An evicted parent falls back to its persisted hop depth,
// degrading toward a chain-depth reset, never toward over-dropping.
const maxInternalHopDepthLedgerEntries = 4096

// internalEventVerbNormalization maps journal-style action verbs to the
// proposal's roster naming (issue.create -> issue.created). Unknown verbs
// pass through unchanged so already-normalized types are stable.
var internalEventVerbNormalization = map[string]string{
	"create":   "created",
	"update":   "updated",
	"delete":   "deleted",
	"open":     "opened",
	"close":    "closed",
	"block":    "blocked",
	"start":    "started",
	"complete": "completed",
	"finish":   "finished",
	"fail":     "failed",
	"cancel":   "cancelled",
	"claim":    "claimed",
	"release":  "released",
	"assign":   "assigned",
}

// NormalizeInternalEventType lowercases the type and maps its final
// dot-segment through the action-verb table (issue.create -> issue.created,
// task.block -> task.blocked); types whose final segment is not a known verb —
// including already-normalized ones — pass through unchanged.
func NormalizeInternalEventType(raw string) (string, error) {
	eventType := strings.ToLower(strings.TrimSpace(raw))
	if eventType == "" || strings.ContainsAny(eventType, " \t\r\n") {
		return "", fmt.Errorf("internal event type %q must be a non-empty dotted identifier: %w", raw, domain.ErrInvalid)
	}
	verb := eventType
	prefix := ""
	if i := strings.LastIndex(eventType, "."); i >= 0 {
		prefix, verb = eventType[:i+1], eventType[i+1:]
	}
	if normalized, ok := internalEventVerbNormalization[verb]; ok {
		return prefix + normalized, nil
	}
	return eventType, nil
}

// InternalEventIdempotencyKey is the loopback ingress idempotency key:
// "internal:{ws}:{eventID}". Stable across emit retries and serve restarts,
// so a re-emitted event dedups exactly-once in the dispatch path.
func InternalEventIdempotencyKey(ws, eventID string) string {
	return "internal:" + ws + ":" + eventID
}

// InternalEvent is one workflow- or system-originated event handed to the
// loopback for re-entry into the trigger router.
type InternalEvent struct {
	// EventID is the emitter's stable id for this occurrence — the
	// idempotency anchor. Required.
	EventID string
	// EventType is the journal-style action or already-normalized event type
	// (issue.create, issue.created, task.blocked, ...). Required.
	EventType string
	// Origin must be workflow (default when empty) or system. External
	// origin is reserved for the webhook ingest path and rejected here so
	// internal emitters cannot forge external provenance.
	Origin domain.TriggerEventOrigin
	// ParentEventID names the trigger event whose handling produced this
	// emission (for the driver-op lane it is derived structurally from the
	// verified emitting run, never trusted from the client). Hop depth is
	// the parent's depth + 1; an empty/unknown parent is a depth-0 root.
	ParentEventID string
	// EmittedByRunID records the emitting DriverRun for audit (stamped into
	// the payload envelope).
	EmittedByRunID string
	SubjectRef     string
	ActorRef       string
	EpicID         string
	Payload        json.RawMessage
	SubjectAttrs   map[string]string
}

// InternalEmitResult reports one loopback emission: either the guard dropped
// it (Dropped + DropReason, Dispatch nil) or it re-entered the router and
// Dispatch carries the fan-out legs.
type InternalEmitResult struct {
	Dropped    bool
	DropReason string
	EventType  string
	RouteKey   string
	Origin     domain.TriggerEventOrigin
	HopDepth   int
	Dispatch   *store.TriggerRouteDispatchResult
}

// InternalEventEmitter is the narrow compatibility seam used by legacy
// runtime producers while Automation owns admission. InternalSource remains
// the in-memory/store-backed conformance implementation; production serve
// composition can supply an adapter backed by Automation without exposing a
// TriggerRoute dispatcher to the producer.
type InternalEventEmitter interface {
	Emit(context.Context, string, InternalEvent) (*InternalEmitResult, error)
}

var _ InternalEventEmitter = (*InternalSource)(nil)

// InternalSource is the single loopback ingress on loom serve. Zero value
// plus Store is ready to use; safe for concurrent use.
type InternalSource struct {
	Store store.Store
	// AwaitResolver is the explicitly injected Execution mutation port used by
	// the legacy loopback await fast path. Nil keeps event emission available
	// but makes await resolution fail closed.
	AwaitResolver store.AtomicAwaitStore
	// HopDepthCap overrides the structural guard's cap when positive;
	// otherwise LOOM_TRIGGER_HOP_DEPTH_CAP, then
	// DefaultInternalEventHopDepthCap apply.
	HopDepthCap int
	// Logger receives the audit record for guard drops (slog.Default when
	// nil).
	Logger *slog.Logger

	mu     sync.Mutex
	depths map[string]int // loopback idempotency key -> stamped hop depth
	order  []string       // FIFO eviction order for depths
}

// internalProvenanceEnvelope is the dispatch payload wrapper (camelCase,
// loomcli driver wire): provenance the loopback stamped plus the emitter's
// original payload under "event".
type internalProvenanceEnvelope struct {
	Origin         string          `json:"origin"`
	HopDepth       int             `json:"hopDepth"`
	ParentEventID  string          `json:"parentEventId,omitempty"`
	EmittedByRunID string          `json:"emittedByRunId,omitempty"`
	Event          json.RawMessage `json:"event,omitempty"`
}

// Emit stamps provenance on one internal event and re-enters it into the
// trigger router via DispatchTriggerRouteV2 on route key
// "internal.{event_type}". Workflow-originated events carry hop_depth =
// parent+1 and are dropped (result.Dropped, audit-logged, no error) once the
// depth would exceed the cap; system ROOTS (no ParentEventID — cron,
// schedulers) sit at depth 0 and never trip the guard, while a system event
// that names a parent (server lifecycle events continuing a trigger chain,
// e.g. run.finished per AW6's C19 stamping) accumulates parent+1 and is
// capped like the workflow lane, so internal.* bindings cannot recursively
// amplify off lifecycle events. Dispatch errors return as-is (a route with no
// matching binding wraps domain.ErrNotFound — "nobody listening" is the
// caller's call). Re-emitting the same EventID is safe: the loopback
// idempotency key dedups the event and every fan-out leg.
func (s *InternalSource) Emit(ctx context.Context, ws string, ev InternalEvent) (*InternalEmitResult, error) {
	ws = strings.TrimSpace(ws)
	eventID := strings.TrimSpace(ev.EventID)
	if ws == "" || eventID == "" {
		return nil, fmt.Errorf("internal event: workspace and event id are required: %w", domain.ErrInvalid)
	}
	eventType, err := NormalizeInternalEventType(ev.EventType)
	if err != nil {
		return nil, err
	}
	routeKey := InternalRouteKeyPrefix + eventType

	origin, hopDepth, dropped, err := s.guardProvenance(ctx, ws, eventID, eventType, routeKey, ev)
	if err != nil || dropped != nil {
		return dropped, err
	}

	payload, err := marshalInternalEnvelope(origin, hopDepth, ev)
	if err != nil {
		return nil, err
	}

	idempotencyKey := InternalEventIdempotencyKey(ws, eventID)
	dispatch, err := s.Store.TriggerRoutes().DispatchTriggerRouteV2(ctx, ws, routeKey, store.TriggerRouteDispatch{
		IdempotencyKey:  idempotencyKey,
		SourceEventID:   eventID,
		EventType:       eventType,
		SubjectRef:      ev.SubjectRef,
		ActorRef:        ev.ActorRef,
		EpicID:          ev.EpicID,
		SignatureStatus: internalSignatureStatus,
		Payload:         payload,
		SubjectAttrs:    ev.SubjectAttrs,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch internal trigger route %q in workspace %q: %w", routeKey, ws, err)
	}
	s.recordHopDepth(idempotencyKey, hopDepth)
	s.dispatchAwaits(ctx, ws, eventID, eventType, origin, ev)
	return &InternalEmitResult{
		EventType: eventType,
		RouteKey:  routeKey,
		Origin:    origin,
		HopDepth:  hopDepth,
		Dispatch:  dispatch,
	}, nil
}

// marshalInternalEnvelope encodes the loopback dispatch payload: stamped
// provenance wrapping the emitter's original payload.
func marshalInternalEnvelope(origin domain.TriggerEventOrigin, hopDepth int, ev InternalEvent) (json.RawMessage, error) {
	payload, err := json.Marshal(internalProvenanceEnvelope{
		Origin:         string(origin),
		HopDepth:       hopDepth,
		ParentEventID:  strings.TrimSpace(ev.ParentEventID),
		EmittedByRunID: ev.EmittedByRunID,
		Event:          ev.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("encode internal event payload: %w", err)
	}
	return payload, nil
}

// dispatchAwaits feeds one admitted loopback event to the dispatch-time await
// matcher (AW7). The matcher receives the emitter's payload, not the
// provenance envelope: an awaiting run resumes with the event the emitter
// produced, provenance stays a routing concern. Best-effort — the emission
// already dispatched durably, so matcher errors are logged, never returned.
// Events the guard dropped or that found no binding never reach this point;
// the run.finished lifecycle lane runs its own journal-anchored matcher pass
// (internal/driver) so composition is independent of binding configuration.
func (s *InternalSource) dispatchAwaits(ctx context.Context, ws, eventID, eventType string, origin domain.TriggerEventOrigin, ev InternalEvent) {
	matcher := &AwaitMatcher{Store: s.Store, AtomicResolver: s.AwaitResolver, Logger: s.Logger}
	if _, err := matcher.Dispatch(ctx, ws, AwaitDispatchEvent{
		EventID:    eventID,
		EventType:  eventType,
		SourceKind: eventpolicy.SourceKindInternal,
		Origin:     origin,
		SubjectRef: ev.SubjectRef,
		ActorRef:   ev.ActorRef,
		Payload:    ev.Payload,
	}); err != nil {
		logger := s.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("internal event await dispatch failed",
			"workspace", ws, "event_id", eventID, "event_type", eventType, "error", err)
	}
}

// guardProvenance applies the structural loop guard: it defaults the origin
// to workflow, accumulates hop depth from the parent chain, and either drops
// an event that would exceed the cap (returning the audit-logged drop result)
// or rejects a forged/unknown origin outright. Depth-0 system roots never
// reach the cap check (the cap is always positive).
func (s *InternalSource) guardProvenance(ctx context.Context, ws, eventID, eventType, routeKey string, ev InternalEvent) (domain.TriggerEventOrigin, int, *InternalEmitResult, error) {
	origin := ev.Origin
	if origin == "" {
		origin = domain.TriggerEventOriginWorkflow
	}
	hopDepth := 0
	switch origin {
	case domain.TriggerEventOriginSystem:
		// System lane: parentless emissions are scheduler-produced roots at
		// depth 0. A system lifecycle event continuing a chain (run.finished
		// for a run admitted by a trigger event, AW6) names its parent and
		// accumulates depth structurally, same as the workflow lane.
		if strings.TrimSpace(ev.ParentEventID) != "" {
			hopDepth = s.parentHopDepth(ctx, ws, ev.ParentEventID) + 1
		}
	case domain.TriggerEventOriginWorkflow:
		hopDepth = s.parentHopDepth(ctx, ws, ev.ParentEventID) + 1
	default:
		return origin, 0, nil, fmt.Errorf("internal event origin %q: emitters may stamp workflow or system only (external is reserved for the webhook ingest path): %w", origin, domain.ErrInvalid)
	}
	if capDepth := s.effectiveHopDepthCap(); hopDepth > capDepth {
		s.auditDrop(ws, eventID, eventType, ev, hopDepth, capDepth)
		return origin, hopDepth, &InternalEmitResult{
			Dropped:    true,
			DropReason: DropReasonHopDepthExceeded,
			EventType:  eventType,
			RouteKey:   routeKey,
			Origin:     origin,
			HopDepth:   hopDepth,
		}, nil
	}
	return origin, hopDepth, nil, nil
}

// ChainHopDepth resolves the chain depth of the named parent trigger event —
// serve-local ledger first, persisted depth as fallback, 0 for an empty or
// unknown parent. Exposed for trusted server-side lifecycle emitters (AW6's
// run.finished) that journal an event BEFORE handing it to Emit and must
// stamp the same parent+1 depth on the journaled record the loopback
// envelope will carry.
func (s *InternalSource) ChainHopDepth(ctx context.Context, ws, parentEventID string) int {
	return s.parentHopDepth(ctx, ws, parentEventID)
}

// parentHopDepth resolves the hop depth of the named parent trigger event:
// the serve-local ledger first (it carries the real loopback depth — the
// persisted record's origin/hop_depth is stamped external/0 by the dispatch
// lane), then the persisted event's normalized depth, looked up both by the
// caller's id directly and by the persisted event's own idempotency key. An
// empty or unknown parent is a depth-0 root.
func (s *InternalSource) parentHopDepth(ctx context.Context, ws, parentEventID string) int {
	parentEventID = strings.TrimSpace(parentEventID)
	if parentEventID == "" {
		return 0
	}
	// The caller may hold the original emit EventID rather than the
	// store-assigned id of the persisted event.
	if depth, ok := s.lookupHopDepth(InternalEventIdempotencyKey(ws, parentEventID)); ok {
		return depth
	}
	parent, err := s.Store.TriggerEvents().Get(ctx, ws, parentEventID)
	if err != nil {
		return 0
	}
	parent.NormalizeProvenance()
	depth := parent.HopDepth
	if d, ok := s.lookupHopDepth(parent.IdempotencyKey); ok && d > depth {
		depth = d
	}
	return depth
}

func (s *InternalSource) effectiveHopDepthCap() int {
	if s.HopDepthCap > 0 {
		return s.HopDepthCap
	}
	if raw := strings.TrimSpace(os.Getenv(internalHopDepthCapEnv)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return DefaultInternalEventHopDepthCap
}

// auditDrop writes the structured audit record for a guard drop.
func (s *InternalSource) auditDrop(ws, eventID, eventType string, ev InternalEvent, hopDepth, capDepth int) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("internal trigger event dropped: "+ErrInternalHopDepthExceeded.Error(),
		"workspace", ws,
		"event_id", eventID,
		"event_type", eventType,
		"parent_event_id", strings.TrimSpace(ev.ParentEventID),
		"emitted_by_run_id", ev.EmittedByRunID,
		"subject_ref", ev.SubjectRef,
		"hop_depth", hopDepth,
		"hop_depth_cap", capDepth,
		"drop_reason", DropReasonHopDepthExceeded,
	)
}

func (s *InternalSource) lookupHopDepth(key string) (int, bool) {
	if key == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	depth, ok := s.depths[key]
	return depth, ok
}

func (s *InternalSource) recordHopDepth(key string, depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.depths == nil {
		s.depths = make(map[string]int)
	}
	if _, exists := s.depths[key]; !exists {
		s.order = append(s.order, key)
		for len(s.order) > maxInternalHopDepthLedgerEntries {
			evicted := s.order[0]
			s.order = s.order[1:]
			delete(s.depths, evicted)
		}
	}
	s.depths[key] = depth
}
