package driverapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// appendAwaitJournalEvent journals one trigger event the way the dispatch
// path does (the registration-scan source), returning the assigned event ID.
func appendAwaitJournalEvent(t *testing.T, st store.Store, eventID, eventType, subjectRef, actorRef string) string {
	t.Helper()
	appender, ok := st.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		t.Fatalf("store %T does not implement TriggerEventAppender", st.TriggerEvents())
	}
	now := time.Now().UTC()
	event, err := appender.AppendTriggerEvent(context.Background(), &automation.Event{
		WorkspaceKey: "WS",
		EventID:      eventID,
		SourceKind:   "test",
		EventType:    eventType,
		SubjectRef:   subjectRef,
		ActorRef:     actorRef,
		Origin:       automation.EventOriginExternal,
		OccurredAt:   now,
		ReceivedAt:   now,
	})
	if err != nil {
		t.Fatalf("AppendTriggerEvent: %v", err)
	}
	return event.EventID
}

// awaitBody builds the canonical events/await request body.
func awaitBody(pattern string, timeoutMs int64, awaitIndex int) map[string]any {
	return map[string]any{"pattern": pattern, "timeoutMs": timeoutMs, "awaitIndex": awaitIndex}
}

func TestDriverAPIAwaitEventValidation(t *testing.T) {
	cases := []struct {
		name     string
		body     map[string]any
		maxEnv   string
		wantCode string
	}{
		{
			name:     "unscoped pattern",
			body:     awaitBody("pr.merged", 60_000, 1),
			wantCode: domain.AwaitErrCodePatternUnscoped,
		},
		{
			name:     "missing timeout",
			body:     map[string]any{"pattern": "pr.merged:pr#7", "awaitIndex": 1},
			wantCode: domain.AwaitErrCodeTimeoutRequired,
		},
		{
			name:     "timeout over env max",
			body:     awaitBody("pr.merged:pr#7", 5_000, 1),
			maxEnv:   "1000",
			wantCode: domain.AwaitErrCodeTimeoutRequired,
		},
		{
			name:     "await index missing",
			body:     map[string]any{"pattern": "pr.merged:pr#7", "timeoutMs": 60_000},
			wantCode: domain.AwaitErrCodeInstanceKeyMalformed,
		},
		{
			name:     "actor wrong type",
			body:     map[string]any{"pattern": "pr.merged:pr#7", "timeoutMs": 60_000, "awaitIndex": 1, "actor": 7},
			wantCode: "invalid",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.maxEnv != "" {
				t.Setenv(driverpkg.AwaitMaxTimeoutEnvVar, tc.maxEnv)
			}
			h := newTestHarness(t)
			resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: tc.body})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%v), want 400", resp.StatusCode, decoded)
			}
			if code := errorCode(t, decoded); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

func TestDriverAPIAwaitEventSatisfiedInline(t *testing.T) {
	h := newTestHarness(t)
	eventID := appendAwaitJournalEvent(t, h.store, "event-merge-1", "pr.merged", "pr#7", "alice")

	resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: awaitBody("pr.merged:pr#7", 60_000, 1)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	// camelCase wire shape: status/instanceKey/pattern/deadline/event{id,actor,occurredAt}.
	if decoded["status"] != string(domain.AwaitSatisfied) ||
		decoded["instanceKey"] != domain.AwaitInstanceKey(h.runID, 1) ||
		decoded["pattern"] != "pr.merged:pr#7" {
		t.Fatalf("response = %v, want satisfied run-1#await-1 on pr.merged:pr#7", decoded)
	}
	if _, ok := decoded["deadline"].(string); !ok {
		t.Fatalf("response = %v, want a deadline", decoded)
	}
	event, _ := decoded["event"].(map[string]any)
	if event == nil || event["id"] != eventID || event["actor"] != "alice" {
		t.Fatalf("event = %v, want id %q actor alice", event, eventID)
	}
	if _, ok := event["occurredAt"].(string); !ok {
		t.Fatalf("event = %v, want occurredAt", event)
	}
	// The workflow continues synchronously: the run stays running.
	run, err := h.store.DriverRuns().Get(context.Background(), "WS", h.runID)
	if err != nil || run.Status != domain.DriverRunRunning {
		t.Fatalf("run = %+v, %v; want still running", run, err)
	}
}

func TestDriverAPIAwaitEventPendingSuspendsRun(t *testing.T) {
	h := newTestHarness(t)

	resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: awaitBody("pr.merged:pr#9", 60_000, 1)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if decoded["status"] != driverpkg.AwaitOutcomeSuspended {
		t.Fatalf("response = %v, want suspended", decoded)
	}
	if _, hasEvent := decoded["event"]; hasEvent {
		t.Fatalf("response = %v, want no event on suspension", decoded)
	}
	run, err := h.store.DriverRuns().Get(context.Background(), "WS", h.runID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run.Status != domain.DriverRunSuspendedAwaitingEvent || run.NodeID != "" || run.LeaseID != "" {
		t.Fatalf("run = %+v, want suspended_awaiting_event with slot released", run)
	}
}

// TestDriverAPIAwaitEventReplayAfterResume drives the full suspend -> resolve ->
// resume -> re-entry cycle: the re-entered run hitting the same awaitIndex
// gets the recorded event with its persisted payload inline.
func TestDriverAPIAwaitEventReplayAfterResume(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	body := awaitBody("pr.merged:pr#9", 60_000, 1)
	instanceKey := domain.AwaitInstanceKey(h.runID, 1)

	resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: body})
	if resp.StatusCode != http.StatusOK || decoded["status"] != driverpkg.AwaitOutcomeSuspended {
		t.Fatalf("first call = %d %v, want suspended", resp.StatusCode, decoded)
	}

	// The resolver side (AW7): event arrives, resolves the await with the
	// size-capped payload, resumes the run.
	eventID := appendAwaitJournalEvent(t, h.store, "event-merge-9", "pr.merged", "pr#9", "bob")
	if _, err := h.store.Awaits().ResolveAwait(ctx, "WS", instanceKey, eventID, []byte(`{"sha":"abc123"}`), "bob"); err != nil {
		t.Fatalf("ResolveAwait: %v", err)
	}
	if _, err := h.store.DriverRuns().ResumeAwaiting(ctx, "WS", h.runID, instanceKey, eventID); err != nil {
		t.Fatalf("ResumeAwaiting: %v", err)
	}
	reclaimed, err := h.store.DriverRuns().Claim(ctx, "WS", h.runID, "node-2", "lease-2")
	if err != nil {
		t.Fatalf("re-claim run: %v", err)
	}
	headers := h.tokenHeadersForRun(t, reclaimed)

	resp, decoded = h.do(t, opRequest{op: "events/await", headers: headers, body: body})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if decoded["status"] != string(domain.AwaitSatisfied) {
		t.Fatalf("replay = %v, want satisfied", decoded)
	}
	event, _ := decoded["event"].(map[string]any)
	if event == nil || event["id"] != eventID || event["actor"] != "bob" {
		t.Fatalf("replay event = %v, want recorded event %q by bob", event, eventID)
	}
	payload, _ := event["payload"].(map[string]any)
	if payload["sha"] != "abc123" {
		t.Fatalf("replay payload = %v, want persisted resume payload inline", event["payload"])
	}
	// Replay is read-only on the run: it stays running.
	run, err := h.store.DriverRuns().Get(ctx, "WS", h.runID)
	if err != nil || run.Status != domain.DriverRunRunning {
		t.Fatalf("run after replay = %+v, %v; want running", run, err)
	}
}

func TestDriverAPIAwaitEventFencingMismatch(t *testing.T) {
	h := newTestHarness(t)
	headers := bearer(h.mintToken(t, time.Hour, func(claims *driverpkg.RunTokenClaims) {
		claims.FencingToken++
	}))

	resp, decoded := h.do(t, opRequest{op: "events/await", headers: headers, body: awaitBody("pr.merged:pr#7", 60_000, 1)})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
	// No write happened: the run is untouched and nothing was registered.
	run, err := h.store.DriverRuns().Get(context.Background(), "WS", h.runID)
	if err != nil || run.Status != domain.DriverRunRunning {
		t.Fatalf("run = %+v, %v; want untouched running run", run, err)
	}
}

// TestDriverAPIAwaitEventActorNormalization covers RULE 4 wire handling: a
// single string and an array both decode; entries are trimmed, de-duplicated
// and persisted on the registration.
func TestDriverAPIAwaitEventActorNormalization(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	// Await 1 resolves inline (actor allow-list as a single string matching
	// the journaled actor) so the run stays running for await 2.
	appendAwaitJournalEvent(t, h.store, "event-approve-1", "approval.granted", "deploy#1", "carol")
	body1 := awaitBody("approval.granted:deploy#1", 60_000, 1)
	body1["actor"] = "carol"
	resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: body1})
	if resp.StatusCode != http.StatusOK || decoded["status"] != string(domain.AwaitSatisfied) {
		t.Fatalf("string-actor await = %d %v, want satisfied", resp.StatusCode, decoded)
	}

	// Await 2 suspends; the array form is normalized before persisting.
	body2 := awaitBody("approval.granted:deploy#2", 60_000, 2)
	body2["actor"] = []string{" alice ", "", "bob", "alice"}
	resp, decoded = h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: body2})
	if resp.StatusCode != http.StatusOK || decoded["status"] != driverpkg.AwaitOutcomeSuspended {
		t.Fatalf("array-actor await = %d %v, want suspended", resp.StatusCode, decoded)
	}
	pending, err := h.store.Awaits().ListDueAwaitDeadlines(ctx, "WS", time.Now().Add(time.Hour), 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListDueAwaitDeadlines = %v, %v; want the pending await", pending, err)
	}
	got := pending[0].ActorAllow
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("ActorAllow = %v, want normalized [alice bob]", got)
	}
}

// listAwaits performs GET /driver/events/awaits with the given headers.
func listAwaits(t *testing.T, h *testHarness, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/workspaces/WS/driver/events/awaits", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, decoded
}

// TestDriverAPIListAwaits rebuilds re-entry context: terminal awaits carry
// their recorded events, pending awaits appear in index order.
func TestDriverAPIListAwaits(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	// Await 1: satisfied inline (run stays running).
	eventID := appendAwaitJournalEvent(t, h.store, "event-merge-1", "pr.merged", "pr#7", "alice")
	resp, decoded := h.do(t, opRequest{op: "events/await", headers: h.ownerHeaders(t), body: awaitBody("pr.merged:pr#7", 60_000, 1)})
	if resp.StatusCode != http.StatusOK || decoded["status"] != string(domain.AwaitSatisfied) {
		t.Fatalf("await 1 = %d %v, want satisfied", resp.StatusCode, decoded)
	}
	// Await 2: pending directly through the store (the crash-before-suspend
	// shape — a pending row while the run is still running).
	if _, err := h.store.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey(h.runID, 2),
		RunID:       h.runID,
		Pattern:     "pr.merged:pr#8",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("RegisterAwaitAndCheck: %v", err)
	}

	resp, decoded = listAwaits(t, h, h.ownerHeaders(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if decoded["runId"] != h.runID {
		t.Fatalf("runId = %v, want %q", decoded["runId"], h.runID)
	}
	awaits, _ := decoded["awaits"].([]any)
	if len(awaits) != 2 {
		t.Fatalf("awaits = %v, want two entries", decoded["awaits"])
	}
	first, _ := awaits[0].(map[string]any)
	second, _ := awaits[1].(map[string]any)
	if first["instanceKey"] != domain.AwaitInstanceKey(h.runID, 1) ||
		first["status"] != string(domain.AwaitSatisfied) || first["satisfiedByEventID"] != eventID {
		t.Fatalf("awaits[0] = %v, want satisfied await-1 by %q", first, eventID)
	}
	if second["instanceKey"] != domain.AwaitInstanceKey(h.runID, 2) ||
		second["status"] != string(domain.AwaitPending) || second["pattern"] != "pr.merged:pr#8" {
		t.Fatalf("awaits[1] = %v, want pending await-2", second)
	}
}

func TestDriverAPIListAwaitsRequiresIdentity(t *testing.T) {
	h := newTestHarness(t)
	resp, decoded := listAwaits(t, h, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}
