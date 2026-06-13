// Issue-journal-bridge tests live in the external trigger_test package so they
// drive the real memstore dispatch path through InternalSource (memstore
// imports trigger for the pattern engine, so an in-package test would cycle) —
// mirroring internal_source_test.go.
package trigger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// fakeIssueJournalReader is a scripted store.IssueJournalReader: it serves
// pages keyed by the incoming afterCursor, so tests describe a journal as a
// cursor->page map and watch the bridge walk it. failAt forces an error on the
// Nth call (1-indexed) to exercise the failure backoff and mid-drain retry.
type fakeIssueJournalReader struct {
	mu     sync.Mutex
	pages  map[string]journalPage // afterCursor -> page served for it
	calls  []string               // afterCursor of every call, in order
	failAt map[int]error          // call number (1-indexed) -> error to return
}

type journalPage struct {
	events  []store.JournalEvent
	next    string
	hasMore bool
}

func (r *fakeIssueJournalReader) ListIssueEvents(_ context.Context, _, afterCursor string, _ int) ([]store.JournalEvent, string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, afterCursor)
	if err, ok := r.failAt[len(r.calls)]; ok {
		return nil, "", false, err
	}
	page, ok := r.pages[afterCursor]
	if !ok {
		// Unknown cursor: empty tail that resumes where it asked.
		return nil, afterCursor, false, nil
	}
	return page.events, page.next, page.hasMore, nil
}

func (r *fakeIssueJournalReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// fixedCursorStore is a deterministic injectable IssueJournalCursorStore for
// the cursor-isolation and persistence assertions.
type fixedCursorStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newFixedCursorStore() *fixedCursorStore { return &fixedCursorStore{m: map[string]string{}} }

func (c *fixedCursorStore) Load(ws string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[ws]
	return v, ok
}

func (c *fixedCursorStore) Save(ws, cursor string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[ws] = cursor
}

// issueEvent is a tiny constructor for a journal entry with an issue After
// snapshot (snake_case v1 wire).
func issueEvent(id, action, actor, entityID string, after string) store.JournalEvent {
	var raw json.RawMessage
	if after != "" {
		raw = json.RawMessage(after)
	}
	return store.JournalEvent{ID: id, Action: action, Actor: actor, EntityID: entityID, After: raw}
}

// newBridge wires a scoped bridge over a fresh memstore that already carries an
// internal.issue.created binding (setupInternalBinding from
// internal_source_test.go). A pre-seeded cursor of "" found makes the drain
// start from the beginning without the bootstrap fast-forward; pass a nil
// cursor store and rely on bootstrap when testing first-run behavior.
func newBridge(t *testing.T, reader *fakeIssueJournalReader, cursors trigger.IssueJournalCursorStore) (*trigger.IssueJournalBridge, *memstore.Store) {
	t.Helper()
	s := memstore.New()
	setupInternalBinding(t, s)
	return &trigger.IssueJournalBridge{
		Store:        s,
		Source:       &trigger.InternalSource{Store: s},
		Reader:       reader,
		WorkspaceKey: "WS",
		Cursors:      cursors,
	}, s
}

// seenStart marks a workspace as already observed (cursor "") so RunOnce drains
// instead of bootstrapping.
func seenStart(cursors *fixedCursorStore, ws string) {
	cursors.Save(ws, "")
}

func TestIssueJournalBridgeEmitsCreateOnInternalBinding(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("100", "issue.create", "user:alice", "42",
				`{"status":"open","title":"Crash on boot","repo":"acme/app","created_by":"alice","number":42}`),
		}, next: "100"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 || out.Skipped != 0 || out.FastForwarded != 0 {
		t.Fatalf("result = %+v, want 1 emitted", out)
	}

	events, runs := internalCounts(t, s)
	if events != 1 || runs != 1 {
		t.Fatalf("store = %d events %d runs, want 1/1", events, runs)
	}
	if got, _ := cursors.Load("WS"); got != "100" {
		t.Fatalf("cursor = %q, want 100", got)
	}

	// The persisted event carries the deterministic loopback identity and the
	// system origin in its envelope; the After snapshot landed in SubjectAttrs.
	evs, err := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	event := evs[0]
	if event.SourceEventID != "fleet-journal-100" ||
		event.IdempotencyKey != "internal:WS:fleet-journal-100" ||
		event.EventType != "issue.created" ||
		event.SubjectRef != "issue:42" ||
		event.ActorRef != "user:alice" {
		t.Fatalf("persisted event = %+v, want loopback identity from journal entry", event)
	}
}

// TestIssueJournalBridgeProvenanceAndAttrs asserts the run payload envelope:
// origin=system, hopDepth=0 (depth-0 root), parentEventId empty, and the
// emitter payload is the After snapshot. SubjectAttrs ride the dispatch input.
func TestIssueJournalBridgeProvenanceAndAttrs(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("7", "issue.create", "user:bob", "9",
				`{"status":"open","title":"T","repo":"acme/app","created_by":"bob"}`),
		}, next: "7"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, s := newBridge(t, reader, cursors)

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("List runs = %v, %v; want 1 run", runs, err)
	}
	var envelope struct {
		Origin        string          `json:"origin"`
		HopDepth      int             `json:"hopDepth"`
		ParentEventID string          `json:"parentEventId"`
		Event         json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Origin != "system" || envelope.HopDepth != 0 || envelope.ParentEventID != "" {
		t.Fatalf("envelope = %+v, want system origin depth-0 root", envelope)
	}
	var ev map[string]any
	if err := json.Unmarshal(envelope.Event, &ev); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if ev["status"] != "open" || ev["title"] != "T" {
		t.Fatalf("emitter payload = %v, want the After snapshot", ev)
	}
}

func TestIssueJournalBridgeReplayDedups(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("100", "issue.create", "u", "42", `{"status":"open"}`),
		}, next: "100"},
	}}
	// Re-run from the SAME starting cursor: the bridge re-emits the same stream
	// id, which dedups in the dispatch path (no second event/run).
	cursors := newFixedCursorStore()
	bridge, s := newBridge(t, reader, cursors)

	for pass := 0; pass < 2; pass++ {
		seenStart(cursors, "WS") // force the drain to start at "" both passes
		if _, err := bridge.RunOnce(t.Context()); err != nil {
			t.Fatalf("RunOnce pass %d: %v", pass, err)
		}
	}
	if events, runs := internalCounts(t, s); events != 1 || runs != 1 {
		t.Fatalf("store after replay = %d events %d runs, want 1/1 deduped", events, runs)
	}
}

// TestIssueJournalBridgeEmitFailureHoldsCursor proves the catch-up-race
// mitigation: when an Emit fails mid-batch the cursor advances only past the
// entries handled BEFORE the failure, so the failed entry is retried next pass.
// Here the binding listens on internal.issue.created; the failure is simulated
// by a reader error AFTER the first page so the second event never persists,
// and the cursor sits on the first event's id.
func TestIssueJournalBridgeEmitFailureHoldsCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{
		pages: map[string]journalPage{
			"": {events: []store.JournalEvent{
				issueEvent("100", "issue.create", "u", "1", `{"status":"open"}`),
			}, next: "100", hasMore: true},
			"100": {events: []store.JournalEvent{
				issueEvent("101", "issue.create", "u", "2", `{"status":"open"}`),
			}, next: "101"},
		},
		failAt: map[int]error{2: errors.New("journal read boom")},
	}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err == nil {
		t.Fatalf("RunOnce err = nil, want the reader failure surfaced")
	}
	if out.Emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (first page only)", out.Emitted)
	}
	// Cursor advanced past the first page (durably handled) but not past 101.
	if got, _ := cursors.Load("WS"); got != "100" {
		t.Fatalf("cursor = %q, want 100 (first page persisted, second page failed)", got)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events = %d, want 1 (second entry never emitted)", events)
	}
}

func TestIssueJournalBridgeActionAllowlistSkips(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("10", "issue.update", "u", "1", `{"status":"open"}`),
			issueEvent("11", "issue.create", "u", "2", `{"status":"open"}`),
			issueEvent("12", "issue.close", "u", "1", `{"status":"closed"}`),
		}, next: "12"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Default allowlist = issue.create only: update and close are skipped, but
	// the cursor still advances past them all (no stall on a skipped entry).
	if out.Emitted != 1 || out.Skipped != 2 {
		t.Fatalf("result = %+v, want 1 emitted 2 skipped", out)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events = %d, want 1 (only issue.create emitted)", events)
	}
	if got, _ := cursors.Load("WS"); got != "12" {
		t.Fatalf("cursor = %q, want 12 (advanced past skipped entries)", got)
	}
}

func TestIssueJournalBridgeCustomAllowlist(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("10", "issue.create", "u", "1", `{"status":"open"}`),
			issueEvent("11", "issue.close", "u", "1", `{"status":"closed"}`),
		}, next: "11"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, s := newBridge(t, reader, cursors)
	bridge.ActionAllowlist = []string{"issue.close"} // close behind the allowlist
	// The default binding listens on internal.issue.created; add one on
	// internal.issue.closed so the closed emission has a listener.
	if _, err := s.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-closed", Name: "b-closed",
		SourceKind: "internal", RouteKey: "internal.issue.closed",
		DriverID: "issue-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create issue.closed binding: %v", err)
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 || out.Skipped != 1 {
		t.Fatalf("result = %+v, want 1 emitted (close) 1 skipped (create)", out)
	}
	evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if len(evs) != 1 || evs[0].EventType != "issue.closed" {
		t.Fatalf("events = %+v, want one issue.closed", evs)
	}
}

// TestIssueJournalBridgeBootstrapFastForward proves the first observation of a
// workspace emits NOTHING and parks the cursor at the journal tail.
func TestIssueJournalBridgeBootstrapFastForward(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("1", "issue.create", "u", "1", `{"status":"open"}`),
			issueEvent("2", "issue.create", "u", "2", `{"status":"open"}`),
		}, next: "2", hasMore: true},
		"2": {events: []store.JournalEvent{
			issueEvent("3", "issue.create", "u", "3", `{"status":"open"}`),
		}, next: "3"},
	}}
	cursors := newFixedCursorStore() // empty: WS is unseen -> bootstrap
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.FastForwarded != 1 || out.Emitted != 0 {
		t.Fatalf("result = %+v, want bootstrap fast-forward, nothing emitted", out)
	}
	if events, runs := internalCounts(t, s); events != 0 || runs != 0 {
		t.Fatalf("store = %d events %d runs, want 0/0 (no triage storm)", events, runs)
	}
	if got, _ := cursors.Load("WS"); got != "3" {
		t.Fatalf("cursor = %q, want 3 (parked at journal tail)", got)
	}

	// The next pass starts from the parked tail and emits only NEW entries.
	reader.mu.Lock()
	reader.pages["3"] = journalPage{events: []store.JournalEvent{
		issueEvent("4", "issue.create", "u", "4", `{"status":"open"}`),
	}, next: "4"}
	reader.mu.Unlock()

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce second pass: %v", err)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events after new entry = %d, want 1 (only the post-bootstrap entry)", events)
	}
}

// TestIssueJournalBridgeReplayEnvDrainsFromZero opts into replay-from-zero and
// asserts the bootstrap pass emits the historical entry instead of
// fast-forwarding.
func TestIssueJournalBridgeReplayEnvDrainsFromZero(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BRIDGE_REPLAY", "1")
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("1", "issue.create", "u", "1", `{"status":"open"}`),
		}, next: "1"},
	}}
	cursors := newFixedCursorStore() // unseen WS, but replay opts into drain-from-0
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.FastForwarded != 0 || out.Emitted != 1 {
		t.Fatalf("result = %+v, want replay-from-zero emit, no fast-forward", out)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events = %d, want 1 (historical entry replayed)", events)
	}
}

// TestIssueJournalBridgeMultiWorkspaceCursorIsolation runs an unscoped sweep
// across two workspaces and asserts each carries its own cursor and emits only
// its own journal.
func TestIssueJournalBridgeMultiWorkspaceCursorIsolation(t *testing.T) {
	s := memstore.New()
	if _, err := s.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create WS: %v", err)
	}
	if _, err := s.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS2", Name: "ws2"}); err != nil {
		t.Fatalf("create WS2: %v", err)
	}
	setupInternalBinding(t, s)
	setupInternalBindingIn(t, s, "WS2")

	reader := &perWorkspaceReader{m: map[string]*fakeIssueJournalReader{
		"WS": {pages: map[string]journalPage{
			"": {events: []store.JournalEvent{issueEvent("a1", "issue.create", "u", "1", `{"status":"open"}`)}, next: "a1"},
		}},
		"WS2": {pages: map[string]journalPage{
			"": {events: []store.JournalEvent{
				issueEvent("b1", "issue.create", "u", "1", `{"status":"open"}`),
				issueEvent("b2", "issue.create", "u", "2", `{"status":"open"}`),
			}, next: "b2"},
		}},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	seenStart(cursors, "WS2")
	bridge := &trigger.IssueJournalBridge{Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader, Cursors: cursors}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 3 {
		t.Fatalf("emitted = %d, want 3 across both workspaces", out.Emitted)
	}
	if c, _ := cursors.Load("WS"); c != "a1" {
		t.Fatalf("WS cursor = %q, want a1", c)
	}
	if c, _ := cursors.Load("WS2"); c != "b2" {
		t.Fatalf("WS2 cursor = %q, want b2", c)
	}
	if n := countEvents(t, s, "WS"); n != 1 {
		t.Fatalf("WS events = %d, want 1", n)
	}
	if n := countEvents(t, s, "WS2"); n != 2 {
		t.Fatalf("WS2 events = %d, want 2", n)
	}
}

// TestIssueJournalBridgeBackoffAfterFailures proves the consecutive-failure
// backoff: after a reader failure the workspace is skipped on the immediately
// following sweep (window doubles to 2 sweeps), then retried.
func TestIssueJournalBridgeBackoffAfterFailures(t *testing.T) {
	reader := &fakeIssueJournalReader{
		pages:  map[string]journalPage{"": {events: nil, next: ""}},
		failAt: map[int]error{1: errors.New("boom")},
	}
	var audit bytes.Buffer
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, _ := newBridge(t, reader, cursors)
	bridge.Logger = slog.New(slog.NewTextHandler(&audit, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Pass 1: reader fails -> error surfaced, one reader call, failure recorded.
	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatalf("pass 1 err = nil, want reader failure")
	}
	if reader.callCount() != 1 {
		t.Fatalf("calls after pass 1 = %d, want 1", reader.callCount())
	}

	// Pass 2: inside the backoff window (1 failure -> window 2 sweeps) -> skip,
	// no reader call.
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if out.BackedOff != 1 || reader.callCount() != 1 {
		t.Fatalf("pass 2 result = %+v calls = %d, want backed off with no new reader call", out, reader.callCount())
	}

	// Pass 3: window elapsed -> reader is polled again (now succeeds, clearing
	// the backoff).
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if reader.callCount() != 2 {
		t.Fatalf("calls after pass 3 = %d, want 2 (retried after window)", reader.callCount())
	}
	if !strings.Contains(audit.String(), "failure backoff") {
		t.Fatalf("audit %q missing backoff record", audit.String())
	}
}

func TestIssueJournalBridgeNilDependenciesRejected(t *testing.T) {
	tests := []struct {
		name   string
		bridge *trigger.IssueJournalBridge
	}{
		{name: "nil store", bridge: &trigger.IssueJournalBridge{Source: &trigger.InternalSource{}, Reader: &fakeIssueJournalReader{}}},
		{name: "nil source", bridge: &trigger.IssueJournalBridge{Store: memstore.New(), Reader: &fakeIssueJournalReader{}}},
		{name: "nil reader", bridge: &trigger.IssueJournalBridge{Store: memstore.New(), Source: &trigger.InternalSource{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.bridge.RunOnce(t.Context()); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("RunOnce err = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestIssueJournalBridgeNoBindingAdvancesCursor proves a no-listener emission
// (domain.ErrNotFound — no binding on internal.issue.created) does NOT stall
// the bridge: the cursor advances past the unbound entry.
func TestIssueJournalBridgeNoBindingAdvancesCursor(t *testing.T) {
	s := memstore.New() // no internal binding seeded
	if _, err := s.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create WS: %v", err)
	}
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{issueEvent("5", "issue.create", "u", "1", `{"status":"open"}`)}, next: "5"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge := &trigger.IssueJournalBridge{Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader, WorkspaceKey: "WS", Cursors: cursors}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce err = %v, want nil (no-listener is not a bridge failure)", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (counted as handled)", out.Emitted)
	}
	if got, _ := cursors.Load("WS"); got != "5" {
		t.Fatalf("cursor = %q, want 5 (advanced past unbound entry)", got)
	}
}

// --- test helpers -------------------------------------------------------

// perWorkspaceReader fans ListIssueEvents out to a per-workspace fake so the
// multi-workspace test keeps each journal isolated.
type perWorkspaceReader struct {
	m map[string]*fakeIssueJournalReader
}

func (r *perWorkspaceReader) ListIssueEvents(ctx context.Context, ws, afterCursor string, limit int) ([]store.JournalEvent, string, bool, error) {
	inner, ok := r.m[ws]
	if !ok {
		return nil, afterCursor, false, nil
	}
	return inner.ListIssueEvents(ctx, ws, afterCursor, limit)
}

// setupInternalBindingIn seeds the internal.issue.created binding (and its
// driver/version) in a non-default workspace.
func setupInternalBindingIn(t *testing.T, s *memstore.Store, ws string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws, DriverID: "issue-bot", Name: "issue-bot",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver in %q: %v", ws, err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: ws, VersionID: "v1", DriverID: "issue-bot", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version in %q: %v", ws, err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: ws, BindingID: "b-internal", Name: "b-internal",
		SourceKind: "internal", RouteKey: internalRouteKey,
		DriverID: "issue-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding in %q: %v", ws, err)
	}
}

func countEvents(t *testing.T, s *memstore.Store, ws string) int {
	t.Helper()
	evs, err := s.TriggerEvents().List(t.Context(), ws, store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events in %q: %v", ws, err)
	}
	return len(evs)
}
