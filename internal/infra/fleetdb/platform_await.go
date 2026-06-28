// platform_await.go implements store.AwaitStore and the DriverRun
// suspend/resume lifecycle against fleet-db's platform v1 await routes
// (chunk AW5):
//
//	POST /api/v1/{ws}/awaits/register-and-check
//	GET  /api/v1/{ws}/awaits?pattern=
//	GET  /api/v1/{ws}/awaits/due
//	GET  /api/v1/{ws}/awaits/{instance_key}/satisfied
//	POST /api/v1/{ws}/awaits/{instance_key}/resolve
//	POST /api/v1/{ws}/driver-runs/{run_id}/suspend
//	POST /api/v1/{ws}/driver-runs/{run_id}/resume
//
// RULE 2 note: registration is one endpoint — register-and-check — so the
// atomic check-then-park transaction lives server-side and this client
// cannot mis-sequence a separate check/park pair.
//
// Instance keys contain '#' and are therefore always path-escaped (%23).
// Responses are snake_case v1 wire shapes decoded into local DTOs; await
// validation failures come back as structured await_* error codes that
// classifyHTTPError maps onto the domain sentinels (see awaitErrSentinel).
package fleetdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// awaitInstanceWire mirrors fleet-db's models.AwaitInstance JSON shape.
// fleet-db additionally carries satisfied_actor; the loomcli domain row has
// no such field (the verified actor is an audit detail of the store), so it
// is intentionally dropped here.
type awaitInstanceWire struct {
	WorkspaceKey       string          `json:"workspace_key"`
	InstanceKey        string          `json:"instance_key"`
	RunID              string          `json:"run_id"`
	Pattern            string          `json:"pattern"`
	ActorAllow         []string        `json:"actor_allow"`
	Deadline           time.Time       `json:"deadline"`
	RegisteredAt       time.Time       `json:"registered_at"`
	Status             string          `json:"status"`
	SatisfiedByEventID string          `json:"satisfied_by_event_id"`
	SatisfiedPayload   json.RawMessage `json:"satisfied_payload"`
	ResumedAt          *time.Time      `json:"resumed_at"`
}

func (w *awaitInstanceWire) toDomain() *domain.AwaitInstance {
	return &domain.AwaitInstance{
		WorkspaceKey:       w.WorkspaceKey,
		InstanceKey:        w.InstanceKey,
		RunID:              w.RunID,
		Pattern:            w.Pattern,
		ActorAllow:         w.ActorAllow,
		Deadline:           w.Deadline,
		RegisteredAt:       w.RegisteredAt,
		Status:             domain.AwaitStatus(w.Status),
		SatisfiedByEventID: w.SatisfiedByEventID,
		SatisfiedPayload:   w.SatisfiedPayload,
		ResumedAt:          w.ResumedAt,
	}
}

// awaitErrSentinel maps fleet-db's structured await error codes back onto
// the loomcli domain sentinels (which wrap domain.ErrInvalid), so callers
// observe identical errors from memstore and the HTTP backend. Returns nil
// for non-await codes.
func awaitErrSentinel(code string) error {
	switch code {
	case domain.AwaitErrCodePatternUnscoped:
		return domain.ErrAwaitPatternUnscoped
	case domain.AwaitErrCodeTimeoutRequired:
		return domain.ErrAwaitTimeoutRequired
	case domain.AwaitErrCodeInstanceKeyMalformed:
		return domain.ErrAwaitInstanceKeyMalformed
	}
	return nil
}

type awaitStore struct{ client *Client }

var _ store.AwaitStore = (*awaitStore)(nil)

func (s *awaitStore) awaitsPath(ws string) string {
	return "/api/v1/" + pathEscape(ws) + "/awaits"
}

// RegisterAwaitAndCheck implements the atomic check-then-park registration.
// The registration is validated client-side first (store.AwaitRegistration
// .Instance enforces RULES 1/3/5 with the domain sentinels); the server
// re-validates and re-checks the deadline against its own clock.
func (s *awaitStore) RegisterAwaitAndCheck(ctx context.Context, workspaceKey string, in store.AwaitRegistration) (*store.AwaitResult, error) {
	inst, err := in.Instance(workspaceKey, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"instance_key":  inst.InstanceKey,
		"run_id":        inst.RunID,
		"pattern":       inst.Pattern,
		"actor_allow":   inst.ActorAllow,
		"deadline":      inst.Deadline,
		"registered_at": inst.RegisteredAt,
	}
	var resp struct {
		Await     *awaitInstanceWire `json:"await"`
		Satisfied bool               `json:"satisfied"`
	}
	if err := s.client.do(ctx, "POST", s.awaitsPath(workspaceKey)+"/register-and-check", body, &resp); err != nil {
		return nil, err
	}
	if resp.Await == nil {
		return nil, fmt.Errorf("fleetdb: register await %s: response carries no await row", inst.InstanceKey)
	}
	return &store.AwaitResult{Instance: resp.Await.toDomain(), Satisfied: resp.Satisfied}, nil
}

// ResolveAwait resolves one pending await. The synthetic-timeout-event
// convention picks the terminal status: an await-timeout-* event ID lands
// the row in timed_out (resume-with-timeout-event decision), anything else
// in satisfied. The payload cap is enforced client-side too so an oversized
// payload never crosses the wire.
func (s *awaitStore) ResolveAwait(ctx context.Context, workspaceKey, instanceKey, eventID string, payload json.RawMessage, actor string) (*store.AwaitResolution, error) {
	if len(payload) > domain.DefaultAwaitResumePayloadCap {
		return nil, fmt.Errorf("fleetdb: resolve await %s: payload is %d bytes (cap %d): %w",
			instanceKey, len(payload), domain.DefaultAwaitResumePayloadCap, domain.ErrInvalid)
	}
	status := domain.AwaitSatisfied
	resolveRoute := "/resolve"
	if domain.IsAwaitTimeoutEventID(eventID) {
		// Non-satisfied resolutions are the privileged system lane: fleet-db
		// rejects them on the public route so a plain await.update key can
		// never bypass the actor predicate (security review fix).
		status = domain.AwaitTimedOut
		resolveRoute = "/resolve-system"
	}
	body := map[string]any{
		"event_id": eventID,
		"status":   status,
		"actor":    actor,
	}
	if len(payload) > 0 {
		// Omitted entirely when empty: a nil RawMessage would otherwise be
		// marshaled as a literal JSON null and persisted verbatim.
		body["payload"] = payload
	}
	var resp struct {
		Await  *awaitInstanceWire `json:"await"`
		Resume bool               `json:"resume"`
	}
	path := s.awaitsPath(workspaceKey) + "/" + pathEscape(instanceKey) + resolveRoute
	if err := s.client.do(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}
	if resp.Await == nil {
		return nil, fmt.Errorf("fleetdb: resolve await %s: response carries no await row", instanceKey)
	}
	return &store.AwaitResolution{Instance: resp.Await.toDomain(), Resume: resp.Resume}, nil
}

func (s *awaitStore) ListAwaitsByPattern(ctx context.Context, workspaceKey, pattern string) ([]*domain.AwaitInstance, error) {
	q := url.Values{}
	q.Set("pattern", pattern)
	return s.listAwaits(ctx, withQuery(s.awaitsPath(workspaceKey), q))
}

func (s *awaitStore) ListDueAwaitDeadlines(ctx context.Context, workspaceKey string, before time.Time, limit int) ([]*domain.AwaitInstance, error) {
	q := url.Values{}
	if !before.IsZero() {
		q.Set("before", before.UTC().Format(time.RFC3339Nano))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return s.listAwaits(ctx, withQuery(s.awaitsPath(workspaceKey)+"/due", q))
}

func (s *awaitStore) listAwaits(ctx context.Context, path string) ([]*domain.AwaitInstance, error) {
	var resp struct {
		Awaits []*awaitInstanceWire `json:"awaits"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.AwaitInstance, 0, len(resp.Awaits))
	for _, row := range resp.Awaits {
		out = append(out, row.toDomain())
	}
	return out, nil
}

func (s *awaitStore) GetSatisfiedAwait(ctx context.Context, workspaceKey, instanceKey string) (*domain.AwaitInstance, error) {
	var out awaitInstanceWire
	path := s.awaitsPath(workspaceKey) + "/" + pathEscape(instanceKey) + "/satisfied"
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

// --- DriverRun suspend/resume lifecycle (store.DriverRunStore) ---

// driverRunStore's CRUD/claim methods live in platform.go; the await-driven
// suspend/resume legs live here next to the await client they pair with.
type driverRunStore struct{ client *Client }

var _ store.DriverRunStore = (*driverRunStore)(nil)

// Suspend parks a running run on its await instance. A 409
// driver_run_already_resumed response (the await resolved inside the
// park->suspend window) surfaces as domain.ErrDriverRunAlreadyResumed: the
// caller must not park and continues the run inline.
func (s *driverRunStore) Suspend(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*domain.DriverRun, error) {
	body := map[string]any{
		"node_id":            nodeID,
		"lease_id":           leaseID,
		"fencing_token":      fencingToken,
		"await_instance_key": awaitInstanceKey,
	}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/suspend"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResumeAwaiting re-queues a suspended run after its await resolved. Per
// the resume-tolerant-of-still-running decision the server may instead
// record a pending-resume marker on a run that has not finished suspending
// yet; the returned run is then still running and the suspend leg will
// observe ErrDriverRunAlreadyResumed.
func (s *driverRunStore) ResumeAwaiting(ctx context.Context, ws, runID, awaitInstanceKey, resumeSourceEventID string) (*domain.DriverRun, error) {
	body := map[string]any{
		"await_instance_key": awaitInstanceKey,
		"resume_event_id":    resumeSourceEventID,
	}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/resume"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
