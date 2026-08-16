// platform_await.go implements execution.AwaitStore and the DriverRun
// suspend/resume lifecycle against fleet-db's platform v1 await routes
// (chunk AW5):
//
//	POST /api/v1/{ws}/awaits/register-and-check
//	GET  /api/v1/{ws}/awaits?pattern=
//	GET  /api/v1/{ws}/awaits/due
//	GET  /api/v1/{ws}/awaits/{instance_key}/satisfied
//	POST /api/v1/{ws}/awaits/{instance_key}/resolve
//	POST /api/v1/{ws}/awaits/resolve-and-resume
//	POST /api/v1/{ws}/awaits/resolve-run-outcome
//	POST /api/v1/{ws}/driver-runs/{run_id}/suspend
//	POST /api/v1/{ws}/driver-runs/{run_id}/resume
//
// RULE 2 note: registration is one endpoint — register-and-check — so the
// atomic register-and-check transaction lives server-side and this client
// cannot mis-sequence a separate check/register pair.
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

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// awaitInstanceWire mirrors fleet-db's models.AwaitInstance JSON shape.
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
	SatisfiedActor     string          `json:"satisfied_actor"`
	SatisfiedPayload   json.RawMessage `json:"satisfied_payload"`
	ResumedAt          *time.Time      `json:"resumed_at"`
}

func (w *awaitInstanceWire) toDomain() *execution.AwaitInstance {
	return &execution.AwaitInstance{
		WorkspaceKey:       w.WorkspaceKey,
		InstanceKey:        w.InstanceKey,
		RunID:              w.RunID,
		Pattern:            w.Pattern,
		ActorAllow:         w.ActorAllow,
		Deadline:           w.Deadline,
		RegisteredAt:       w.RegisteredAt,
		Status:             execution.AwaitStatus(w.Status),
		SatisfiedByEventID: w.SatisfiedByEventID,
		SatisfiedActor:     w.SatisfiedActor,
		SatisfiedPayload:   w.SatisfiedPayload,
		ResumedAt:          w.ResumedAt,
	}
}

// awaitErrSentinel maps fleet-db's structured await error codes onto
// Execution-owned sentinels, so callers observe the owner vocabulary rather
// than the backend persistence vocabulary. Returns nil for non-await codes.
func awaitErrSentinel(code string) error {
	switch code {
	case execution.AwaitErrCodePatternUnscoped:
		return execution.ErrAwaitPatternUnscoped
	case execution.AwaitErrCodeTimeoutRequired:
		return execution.ErrAwaitTimeoutRequired
	case execution.AwaitErrCodeInstanceKeyMalformed:
		return execution.ErrAwaitInstanceKeyMalformed
	}
	return nil
}

type awaitStore struct{ client *Client }

var _ execution.AwaitStore = (*awaitStore)(nil)
var _ execution.AtomicAwaitStore = (*awaitStore)(nil)
var _ execution.RunOutcomeAwaitStore = (*awaitStore)(nil)

func (s *awaitStore) awaitsPath(ws string) string {
	return "/api/v1/" + pathEscape(ws) + "/awaits"
}

// RegisterAwaitAndCheck implements the atomic register-and-check registration.
// The registration is validated client-side first (execution.AwaitRegistration
// .Instance enforces RULES 1/3/5 with the domain sentinels); the server
// re-validates and re-checks the deadline against its own clock.
func (s *awaitStore) RegisterAwaitAndCheck(ctx context.Context, workspaceKey string, in execution.AwaitRegistration) (*execution.AwaitRegistrationResult, error) {
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
	return &execution.AwaitRegistrationResult{Instance: resp.Await.toDomain(), Satisfied: resp.Satisfied}, nil
}

// ResolveAwait resolves one pending await. The synthetic-timeout-event
// convention picks the terminal status: an await-timeout-* event ID lands
// the row in timed_out (resume-with-timeout-event decision), anything else
// in satisfied. The payload cap is enforced client-side too so an oversized
// payload never crosses the wire.
func (s *awaitStore) ResolveAwait(ctx context.Context, workspaceKey, instanceKey, eventID string, payload json.RawMessage, actor string) (*execution.AwaitResolution, error) {
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return nil, fmt.Errorf("fleetdb: resolve await %s: payload is %d bytes (cap %d): %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	status := execution.AwaitSatisfied
	resolveRoute := "/resolve"
	if execution.IsAwaitTimeoutEventID(eventID) {
		// Non-satisfied resolutions are the privileged system lane: fleet-db
		// rejects them on the public route so a plain await.update key can
		// never bypass the actor predicate (security review fix).
		status = execution.AwaitTimedOut
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
	return &execution.AwaitResolution{Instance: resp.Await.toDomain(), Resume: resp.Resume}, nil
}

// ResolveAwaitAndResume invokes FleetDB's generic atomic dispatch command.
// The service commits the terminal await row together with the run transition
// (or pending-resume marker), so neither a process crash nor a lost HTTP
// response can strand a suspended run behind an unindexed satisfied await.
func (s *awaitStore) ResolveAwaitAndResume(
	ctx context.Context,
	workspaceKey, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
) error {
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return fmt.Errorf("fleetdb: resolve await and resume %s: payload is %d bytes (cap %d): %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	status := execution.AwaitSatisfied
	if execution.IsAwaitTimeoutEventID(eventID) {
		if actor != execution.AwaitTimeoutActor {
			return fmt.Errorf("fleetdb: resolve await and resume %s: timeout event actor %q: %w",
				instanceKey, actor, execution.ErrAwaitActorForbidden)
		}
		status = execution.AwaitTimedOut
	}
	body := map[string]any{
		"instance_key": instanceKey,
		"event_id":     eventID,
		"status":       status,
		"actor":        actor,
	}
	if len(payload) > 0 {
		body["payload"] = payload
	}
	return s.client.do(ctx, "POST", s.awaitsPath(workspaceKey)+"/resolve-and-resume", body, nil)
}

// ResolveRunOutcomeAwaitAndResume invokes FleetDB's atomic run.finished
// command. The instance key stays in JSON rather than a path segment, and a
// successful replay guarantees both the await and parent run have converged.
func (s *awaitStore) ResolveRunOutcomeAwaitAndResume(
	ctx context.Context,
	workspaceKey, instanceKey, eventID string,
	payload json.RawMessage,
) error {
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return fmt.Errorf("fleetdb: resolve run outcome await %s: payload is %d bytes (cap %d): %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	body := map[string]any{
		"instance_key": instanceKey,
		"event_id":     eventID,
	}
	if len(payload) > 0 {
		body["payload"] = payload
	}
	return s.client.do(ctx, "POST", s.awaitsPath(workspaceKey)+"/resolve-run-outcome", body, nil)
}

func (s *awaitStore) ListAwaitsByPattern(ctx context.Context, workspaceKey, pattern string) ([]*execution.AwaitInstance, error) {
	q := url.Values{}
	q.Set("pattern", pattern)
	return s.listAwaits(ctx, withQuery(s.awaitsPath(workspaceKey), q))
}

func (s *awaitStore) ListDueAwaitDeadlines(ctx context.Context, workspaceKey string, before time.Time, limit int) ([]*execution.AwaitInstance, error) {
	q := url.Values{}
	if !before.IsZero() {
		q.Set("before", before.UTC().Format(time.RFC3339Nano))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return s.listAwaits(ctx, withQuery(s.awaitsPath(workspaceKey)+"/due", q))
}

func (s *awaitStore) listAwaits(ctx context.Context, path string) ([]*execution.AwaitInstance, error) {
	var resp struct {
		Awaits []*awaitInstanceWire `json:"awaits"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*execution.AwaitInstance, 0, len(resp.Awaits))
	for _, row := range resp.Awaits {
		out = append(out, row.toDomain())
	}
	return out, nil
}

func (s *awaitStore) GetSatisfiedAwait(ctx context.Context, workspaceKey, instanceKey string) (*execution.AwaitInstance, error) {
	var out awaitInstanceWire
	path := s.awaitsPath(workspaceKey) + "/" + pathEscape(instanceKey) + "/satisfied"
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

// --- DriverRun suspend/resume lifecycle (execution.DriverRunStore) ---

// driverRunStore's CRUD/claim methods live in platform.go; the await-driven
// suspend/resume legs live here next to the await client they pair with.
type driverRunStore struct{ client *Client }

var _ execution.DriverRunStore = (*driverRunStore)(nil)

// Suspend suspends a running run on its await instance. A 409
// driver_run_already_resumed response (the await resolved inside the
// pending->suspend window) surfaces as execution.ErrAlreadyResumed: the
// caller must not suspend and continues the run inline.
func (s *driverRunStore) Suspend(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*execution.DriverRunRecord, error) {
	body := map[string]any{
		"node_id":            nodeID,
		"lease_id":           leaseID,
		"fencing_token":      fencingToken,
		"await_instance_key": awaitInstanceKey,
	}
	var out execution.DriverRunRecord
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
func (s *driverRunStore) ResumeAwaiting(ctx context.Context, ws, runID, awaitInstanceKey, resumeSourceEventID string) (*execution.DriverRunRecord, error) {
	body := map[string]any{
		"await_instance_key": awaitInstanceKey,
		"resume_event_id":    resumeSourceEventID,
	}
	var out execution.DriverRunRecord
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/resume"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
