// Integration tests for the session-authenticated approval endpoint (vet
// A2): verified-identity enforcement, the eligible-approver check, the
// journal-first append (RULE 2) and matcher dispatch through the real
// httptest mux + memstore wiring.
package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const approvalsTestWS = "WS"

// Test identity headers the harness middleware translates into the verified
// session identity (the production auth middleware's job).
const (
	testUserHeader  = "X-Test-User"
	testEmailHeader = "X-Test-Email"
)

type approvalsHarness struct {
	store  *memstore.Store
	server *httptest.Server
}

func newApprovalsHarness(t *testing.T) *approvalsHarness {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: approvalsTestWS, Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: approvalsTestWS, DriverID: "approval-gate", Name: "approval-gate",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: approvalsTestWS, VersionID: "v1", DriverID: "approval-gate", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user := r.Header.Get(testUserHeader); user != "" {
			identity := middleware.UserIdentity{UserID: user, Email: r.Header.Get(testEmailHeader)}
			r = r.WithContext(middleware.WithUserIdentity(r.Context(), identity))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return &approvalsHarness{store: st, server: server}
}

// suspendedRun creates, claims and suspends a run pending on pattern with the
// given allow-list, returning the await instance key.
func (h *approvalsHarness) suspendedRun(t *testing.T, runID, pattern string, actorAllow []string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: approvalsTestWS, RunID: runID, DriverID: "approval-gate", DriverVersionID: "v1", Entrypoint: "run",
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}
	run, err := h.store.DriverRuns().Claim(ctx, approvalsTestWS, runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("Claim run: %v", err)
	}
	key := domain.AwaitInstanceKey(runID, 1)
	res, err := h.store.Awaits().RegisterAwaitAndCheck(ctx, approvalsTestWS, store.AwaitRegistration{
		InstanceKey: key, RunID: runID, Pattern: pattern, ActorAllow: actorAllow,
		Deadline: time.Now().Add(time.Hour),
	})
	if err != nil || res.Satisfied {
		t.Fatalf("RegisterAwaitAndCheck = %+v, %v; want pending", res, err)
	}
	if _, err := h.store.DriverRuns().Suspend(ctx, approvalsTestWS, runID,
		run.NodeID, run.LeaseID, run.FencingToken, key); err != nil {
		t.Fatalf("Suspend run: %v", err)
	}
	return key
}

type approvalCall struct {
	user  string
	email string
	body  map[string]any
}

func (h *approvalsHarness) post(t *testing.T, call approvalCall) (*http.Response, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/workspaces/"+approvalsTestWS+"/approvals", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if call.user != "" {
		req.Header.Set(testUserHeader, call.user)
	}
	if call.email != "" {
		req.Header.Set(testEmailHeader, call.email)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST approvals: %v", err)
	}
	defer resp.Body.Close()
	decoded := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, decoded
}

func (h *approvalsHarness) journalEvents(t *testing.T) []*domain.TriggerEvent {
	t.Helper()
	events, err := h.store.TriggerEvents().List(context.Background(), approvalsTestWS, store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List trigger events: %v", err)
	}
	return events
}

func TestApprovalRequiresVerifiedSession(t *testing.T) {
	h := newApprovalsHarness(t)
	resp, decoded := h.post(t, approvalCall{body: map[string]any{"subjectRef": "acme/widgets#7@shaA"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	if events := h.journalEvents(t); len(events) != 0 {
		t.Fatalf("journal = %d events after anonymous approval, want none", len(events))
	}
}

func TestApprovalValidation(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "missing subject", body: map[string]any{"decision": "approved"}},
		{name: "blank subject", body: map[string]any{"subjectRef": "   "}},
		{name: "bad decision", body: map[string]any{"subjectRef": "acme/widgets#7@shaA", "decision": "maybe"}},
		{name: "blank event type renders unscoped", body: map[string]any{"subjectRef": " ", "eventType": " "}},
	}
	h := newApprovalsHarness(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, decoded := h.post(t, approvalCall{user: "user-1", email: "alice@example.com", body: tc.body})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%v), want 400", resp.StatusCode, decoded)
			}
		})
	}
	if events := h.journalEvents(t); len(events) != 0 {
		t.Fatalf("journal = %d events after rejected validations, want none", len(events))
	}
}

// The happy path: an eligible session approver resolves the pending await,
// the suspended run re-queues, and the decision payload (with the verified
// actor) is persisted on the satisfied row.
func TestApprovalResolvesAndResumesEligibleAwait(t *testing.T) {
	h := newApprovalsHarness(t)
	pattern := domain.AwaitEventKey(DefaultApprovalEventType, "acme/widgets#7@shaA")
	key := h.suspendedRun(t, "run-gate", pattern, []string{"alice@example.com"})

	resp, decoded := h.post(t, approvalCall{
		user: "user-alice", email: "alice@example.com",
		body: map[string]any{"subjectRef": "acme/widgets#7@shaA", "note": "lgtm"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if decoded["status"] != DecisionApproved || decoded["actor"] != "alice@example.com" ||
		decoded["pendingMatched"] != float64(1) {
		t.Fatalf("response = %v, want approved by alice with one pending match", decoded)
	}
	eventID, _ := decoded["eventId"].(string)
	if !strings.HasPrefix(eventID, "approval-") {
		t.Fatalf("eventId = %q, want approval- prefix", eventID)
	}

	ctx := context.Background()
	run, err := h.store.DriverRuns().Get(ctx, approvalsTestWS, "run-gate")
	if err != nil || run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != eventID {
		t.Fatalf("run = %+v, %v; want queued resumed by %s", run, err, eventID)
	}
	satisfied, err := h.store.Awaits().GetSatisfiedAwait(ctx, approvalsTestWS, key)
	if err != nil || satisfied.Status != domain.AwaitSatisfied || satisfied.SatisfiedByEventID != eventID {
		t.Fatalf("satisfied = %+v, %v; want satisfied by %s", satisfied, err, eventID)
	}
	var payload approvalPayload
	if err := json.Unmarshal(satisfied.SatisfiedPayload, &payload); err != nil {
		t.Fatalf("decode satisfied payload: %v", err)
	}
	if payload.Decision != DecisionApproved || payload.ApprovedBy != "alice@example.com" ||
		payload.ApprovedByUserID != "user-alice" || payload.Note != "lgtm" {
		t.Fatalf("payload = %+v, want approved by alice (user-alice) note lgtm", payload)
	}
	events := h.journalEvents(t)
	if len(events) != 1 || events[0].EventID != eventID || events[0].ActorRef != "alice@example.com" ||
		events[0].EventType != DefaultApprovalEventType || events[0].SubjectRef != "acme/widgets#7@shaA" {
		t.Fatalf("journal = %+v, want the approval event with the verified actor", events)
	}
}

// The eligible-approver check fails closed: an authenticated but ineligible
// session actor gets 403, nothing is journaled, the await stays pending and
// the run stays suspended.
func TestApprovalIneligibleActorRefusedAndNothingEmitted(t *testing.T) {
	h := newApprovalsHarness(t)
	pattern := domain.AwaitEventKey(DefaultApprovalEventType, "acme/widgets#7@shaA")
	h.suspendedRun(t, "run-guarded", pattern, []string{"release-manager@example.com"})

	resp, decoded := h.post(t, approvalCall{
		user: "user-mallory", email: "mallory@example.com",
		body: map[string]any{"subjectRef": "acme/widgets#7@shaA"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	if events := h.journalEvents(t); len(events) != 0 {
		t.Fatalf("journal = %d events after refused approval, want none", len(events))
	}
	ctx := context.Background()
	run, err := h.store.DriverRuns().Get(ctx, approvalsTestWS, "run-guarded")
	if err != nil || run.Status != domain.DriverRunSuspendedAwaitingEvent || run.ResumeSourceEventID != "" {
		t.Fatalf("run = %+v, %v; want still suspended untouched", run, err)
	}
	pending, err := h.store.Awaits().ListAwaitsByPattern(ctx, approvalsTestWS, pattern)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v; want the guarded await still pending", pending, err)
	}
}

// RULE 2 through the endpoint: an approval granted BEFORE any await exists
// is journaled, and a later registration on the same key satisfies inline
// from the scan — provided the registering await admits the recorded actor.
func TestApprovalBeforeRegistrationSatisfiesLaterAwaitInline(t *testing.T) {
	h := newApprovalsHarness(t)
	resp, decoded := h.post(t, approvalCall{
		user: "user-alice", email: "alice@example.com",
		body: map[string]any{"subjectRef": "acme/widgets#9@shaB"},
	})
	if resp.StatusCode != http.StatusOK || decoded["pendingMatched"] != float64(0) {
		t.Fatalf("response = %d %v, want 200 with zero pending matches", resp.StatusCode, decoded)
	}
	eventID, _ := decoded["eventId"].(string)

	res, err := h.store.Awaits().RegisterAwaitAndCheck(context.Background(), approvalsTestWS, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("run-later", 1), RunID: "run-later",
		Pattern:    domain.AwaitEventKey(DefaultApprovalEventType, "acme/widgets#9@shaB"),
		ActorAllow: []string{"alice@example.com"},
		Deadline:   time.Now().Add(time.Hour),
	})
	if err != nil || !res.Satisfied || res.Instance.SatisfiedByEventID != eventID {
		t.Fatalf("registration = %+v, %v; want inline satisfaction by %s", res, err, eventID)
	}

	// The same pre-granted approval does NOT satisfy a registration whose
	// allow-list excludes the recorded actor (RULE 4 at scan time).
	guarded, err := h.store.Awaits().RegisterAwaitAndCheck(context.Background(), approvalsTestWS, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("run-later-guarded", 1), RunID: "run-later-guarded",
		Pattern:    domain.AwaitEventKey(DefaultApprovalEventType, "acme/widgets#9@shaB"),
		ActorAllow: []string{"release-manager@example.com"},
		Deadline:   time.Now().Add(time.Hour),
	})
	if err != nil || guarded.Satisfied {
		t.Fatalf("guarded registration = %+v, %v; want pending", guarded, err)
	}
}

// A rejection resolves the await too: the workflow resumes and branches on
// payload.decision.
func TestApprovalRejectionResolvesAwait(t *testing.T) {
	h := newApprovalsHarness(t)
	pattern := domain.AwaitEventKey(DefaultApprovalEventType, "acme/widgets#7@shaA")
	key := h.suspendedRun(t, "run-reject", pattern, nil) // empty allow-list: any verified actor

	resp, decoded := h.post(t, approvalCall{
		user: "user-bob", // no email: the actor falls back to the user id
		body: map[string]any{"subjectRef": "acme/widgets#7@shaA", "decision": "rejected"},
	})
	if resp.StatusCode != http.StatusOK || decoded["status"] != DecisionRejected || decoded["actor"] != "user-bob" {
		t.Fatalf("response = %d %v, want 200 rejected by user-bob", resp.StatusCode, decoded)
	}
	satisfied, err := h.store.Awaits().GetSatisfiedAwait(context.Background(), approvalsTestWS, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	var payload approvalPayload
	if err := json.Unmarshal(satisfied.SatisfiedPayload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Decision != DecisionRejected || payload.ApprovedBy != "user-bob" {
		t.Fatalf("payload = %+v, want rejected by user-bob", payload)
	}
	run, err := h.store.DriverRuns().Get(context.Background(), approvalsTestWS, "run-reject")
	if err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("run = %+v, %v; want re-queued on rejection", run, err)
	}
}
