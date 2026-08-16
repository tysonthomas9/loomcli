// Issue-journal-bridge tests live in the external trigger_test package and
// exercise the bridge through its consumer-owned event-emission port.
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

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// fakeIssueJournalReader is a scripted automation.IssueJournalReader: it serves
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
	events  []automation.JournalEvent
	next    string
	hasMore bool
}

type failingInternalEmitter struct {
	mu    sync.Mutex
	calls int
	err   error
}

type capturedInternalEmission struct {
	workspace string
	event     trigger.InternalEvent
}

type capturingInternalEmitter struct {
	mu        sync.Mutex
	emissions []capturedInternalEmission
}

func (emitter *capturingInternalEmitter) Emit(_ context.Context, workspace string, event trigger.InternalEvent) (*trigger.InternalEmitResult, error) {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	event.SubjectAttrs = cloneTestStringMap(event.SubjectAttrs)
	emitter.emissions = append(emitter.emissions, capturedInternalEmission{workspace: workspace, event: event})
	return &trigger.InternalEmitResult{EventType: event.EventType, Origin: event.Origin}, nil
}

func (emitter *capturingInternalEmitter) snapshot() []capturedInternalEmission {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return append([]capturedInternalEmission(nil), emitter.emissions...)
}

func (emitter *capturingInternalEmitter) eventsOfType(eventType string) []trigger.InternalEvent {
	emissions := emitter.snapshot()
	events := make([]trigger.InternalEvent, 0, len(emissions))
	for _, emission := range emissions {
		if emission.event.EventType == eventType {
			events = append(events, emission.event)
		}
	}
	return events
}

func cloneTestStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (e *failingInternalEmitter) Emit(context.Context, string, trigger.InternalEvent) (*trigger.InternalEmitResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return nil, e.err
}

func (e *failingInternalEmitter) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (r *fakeIssueJournalReader) ListIssueEvents(_ context.Context, _, afterCursor string, _ int) ([]automation.JournalEvent, string, bool, error) {
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
func issueEvent(id, action, actor, entityID string, after string) automation.JournalEvent {
	var raw json.RawMessage
	if after != "" {
		raw = json.RawMessage(after)
	}
	return automation.JournalEvent{ID: id, Action: action, Actor: actor, EntityID: entityID, After: raw}
}

// newBridge wires a scoped bridge over a fresh workspace query store and a
// capture of the current event-emission port. A pre-seeded cursor of "" makes the drain
// start from the beginning without the bootstrap fast-forward; pass a nil
// cursor store and rely on bootstrap when testing first-run behavior.
func newBridge(t *testing.T, reader *fakeIssueJournalReader, cursors trigger.IssueJournalCursorStore) (*trigger.IssueJournalBridge, *capturingInternalEmitter) {
	t.Helper()
	s := memstore.New()
	emitter := &capturingInternalEmitter{}
	return &trigger.IssueJournalBridge{
		Store:        s,
		Source:       emitter,
		Reader:       reader,
		WorkspaceKey: "WS",
		Cursors:      cursors,
	}, emitter
}

// seenStart marks a workspace as already observed (cursor "") so RunOnce drains
// instead of bootstrapping.
func seenStart(cursors *fixedCursorStore, ws string) {
	cursors.Save(ws, "")
}

func TestIssueJournalBridgeEmitsCreateOnInternalBinding(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("100", "issue.create", "user:alice", "42",
				`{"status":"open","title":"Crash on boot","repo":"acme/app","created_by":"alice","number":42}`),
		}, next: "100"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 || out.Skipped != 0 || out.FastForwarded != 0 {
		t.Fatalf("result = %+v, want 1 emitted", out)
	}

	emissions := emitter.snapshot()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1", len(emissions))
	}
	if got, _ := cursors.Load("WS"); got != "100" {
		t.Fatalf("cursor = %q, want 100", got)
	}

	// The bridge sends deterministic source identity and trusted journal data;
	// Automation owns normalization, idempotency, and persistence.
	event := emissions[0].event
	if emissions[0].workspace != "WS" || event.EventID != "fleet-journal-100" ||
		event.EventType != "issue.create" || event.Origin != automation.EventOriginSystem ||
		event.SubjectRef != "issue:42" ||
		event.ActorRef != "user:alice" {
		t.Fatalf("emitted event = %+v, want journal identity", event)
	}
}

// TestIssueJournalBridgeProvenanceAndAttrs asserts the run payload envelope:
// origin=system, hopDepth=0 (depth-0 root), parentEventId empty, and the
// emitter payload is the After snapshot. SubjectAttrs ride the dispatch input.
func TestIssueJournalBridgeProvenanceAndAttrs(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("7", "issue.create", "user:bob", "9",
				`{"status":"open","title":"T","repo":"acme/app","created_by":"bob"}`),
		}, next: "7"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	emissions := emitter.snapshot()
	if len(emissions) != 1 || emissions[0].event.Origin != automation.EventOriginSystem || emissions[0].event.ParentEventID != "" {
		t.Fatalf("emissions = %+v, want one system root", emissions)
	}
	var ev map[string]any
	if err := json.Unmarshal(emissions[0].event.Payload, &ev); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if ev["status"] != "open" || ev["title"] != "T" {
		t.Fatalf("emitter payload = %v, want the After snapshot", ev)
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
			"": {events: []automation.JournalEvent{
				issueEvent("100", "issue.create", "u", "1", `{"status":"open"}`),
			}, next: "100", hasMore: true},
			"100": {events: []automation.JournalEvent{
				issueEvent("101", "issue.create", "u", "2", `{"status":"open"}`),
			}, next: "101"},
		},
		failAt: map[int]error{2: errors.New("journal read boom")},
	}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)

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
	if emissions := emitter.snapshot(); len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1 (second entry never emitted)", len(emissions))
	}
}

// TestIssueJournalBridgeDrainsPastFilteredPage proves the bridge advances past a
// page the reader filtered out ENTIRELY — a raw window of non-issue mutations
// (driver_run/task_run/role churn) with no issue event. fleet-db filters
// entity_type=issue server-side and reports has_more against the POST-FILTER
// count, so such a page arrives as {events:[], nextCursor:<advanced>,
// hasMore:false}. The old drain advanced the cursor only by the last EMITTED id
// (empty here) and stopped on !hasMore, pinning the cursor and stalling the lane
// forever; the bridge must resume from the reader's nextCursor instead.
func TestIssueJournalBridgeDrainsPastFilteredPage(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		// A full raw window of non-issue mutations: zero issue events returned, the
		// cursor advanced, has_more=false (post-filter count 0 != page limit).
		"": {events: nil, next: "50", hasMore: false},
		// The next window finally carries the real issue.create.
		"50": {events: []automation.JournalEvent{
			issueEvent("100", "issue.create", "user:alice", "42", `{"status":"open"}`),
		}, next: "100", hasMore: false},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (drained past the filtered page to the issue event)", out.Emitted)
	}
	if emissions := emitter.snapshot(); len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1", len(emissions))
	}
	if got, _ := cursors.Load("WS"); got != "100" {
		t.Fatalf("cursor = %q, want 100 (advanced past the all-filtered page)", got)
	}
}

func TestIssueJournalBridgeActionAllowlistSkips(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("10", "issue.update", "u", "1", `{"status":"open"}`),
			issueEvent("11", "issue.create", "u", "2", `{"status":"open"}`),
			issueEvent("12", "issue.close", "u", "1", `{"status":"closed"}`),
		}, next: "12"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Default allowlist = issue.create only: update and close are skipped, but
	// the cursor still advances past them all (no stall on a skipped entry).
	if out.Emitted != 1 || out.Skipped != 2 {
		t.Fatalf("result = %+v, want 1 emitted 2 skipped", out)
	}
	if emissions := emitter.snapshot(); len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1 (only issue.create emitted)", len(emissions))
	}
	if got, _ := cursors.Load("WS"); got != "12" {
		t.Fatalf("cursor = %q, want 12 (advanced past skipped entries)", got)
	}
}

func TestIssueJournalBridgeCustomAllowlist(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("10", "issue.create", "u", "1", `{"status":"open"}`),
			issueEvent("11", "issue.close", "u", "1", `{"status":"closed"}`),
		}, next: "11"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge, emitter := newBridge(t, reader, cursors)
	bridge.ActionAllowlist = []string{"issue.close"} // close behind the allowlist

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 || out.Skipped != 1 {
		t.Fatalf("result = %+v, want 1 emitted (close) 1 skipped (create)", out)
	}
	emissions := emitter.snapshot()
	if len(emissions) != 1 || emissions[0].event.EventType != "issue.close" {
		t.Fatalf("emissions = %+v, want one issue.close journal event", emissions)
	}
}

// TestIssueJournalBridgeBootstrapFastForward proves the first observation of a
// workspace emits NOTHING and holds the cursor at the journal tail.
func TestIssueJournalBridgeBootstrapFastForward(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("1", "issue.create", "u", "1", `{"status":"open"}`),
			issueEvent("2", "issue.create", "u", "2", `{"status":"open"}`),
		}, next: "2", hasMore: true},
		"2": {events: []automation.JournalEvent{
			issueEvent("3", "issue.create", "u", "3", `{"status":"open"}`),
		}, next: "3"},
	}}
	cursors := newFixedCursorStore() // empty: WS is unseen -> bootstrap
	bridge, emitter := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.FastForwarded != 1 || out.Emitted != 0 {
		t.Fatalf("result = %+v, want bootstrap fast-forward, nothing emitted", out)
	}
	if emissions := emitter.snapshot(); len(emissions) != 0 {
		t.Fatalf("emissions = %d, want 0 (no triage storm)", len(emissions))
	}
	if got, _ := cursors.Load("WS"); got != "3" {
		t.Fatalf("cursor = %q, want 3 (paused at journal tail)", got)
	}

	// The next pass starts from the paused tail and emits only NEW entries.
	reader.mu.Lock()
	reader.pages["3"] = journalPage{events: []automation.JournalEvent{
		issueEvent("4", "issue.create", "u", "4", `{"status":"open"}`),
	}, next: "4"}
	reader.mu.Unlock()

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce second pass: %v", err)
	}
	if emissions := emitter.snapshot(); len(emissions) != 1 {
		t.Fatalf("emissions after new entry = %d, want 1", len(emissions))
	}
}

// TestIssueJournalBridgeReplayEnvDrainsFromZero opts into replay-from-zero and
// asserts the bootstrap pass emits the historical entry instead of
// fast-forwarding.
func TestIssueJournalBridgeReplayEnvDrainsFromZero(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BRIDGE_REPLAY", "1")
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("1", "issue.create", "u", "1", `{"status":"open"}`),
		}, next: "1"},
	}}
	cursors := newFixedCursorStore() // unseen WS, but replay opts into drain-from-0
	bridge, emitter := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.FastForwarded != 0 || out.Emitted != 1 {
		t.Fatalf("result = %+v, want replay-from-zero emit, no fast-forward", out)
	}
	if emissions := emitter.snapshot(); len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1 (historical entry replayed)", len(emissions))
	}
}

// TestIssueJournalBridgeMultiWorkspaceCursorIsolation runs an unscoped sweep
// across two workspaces and asserts each carries its own cursor and emits only
// its own journal.
func TestIssueJournalBridgeMultiWorkspaceCursorIsolation(t *testing.T) {
	s := memstore.New()
	if _, err := s.Workspaces().Create(t.Context(), workspaceowner.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create WS: %v", err)
	}
	if _, err := s.Workspaces().Create(t.Context(), workspaceowner.WorkspaceCreate{Key: "WS2", Name: "ws2"}); err != nil {
		t.Fatalf("create WS2: %v", err)
	}
	reader := &perWorkspaceReader{m: map[string]*fakeIssueJournalReader{
		"WS": {pages: map[string]journalPage{
			"": {events: []automation.JournalEvent{issueEvent("a1", "issue.create", "u", "1", `{"status":"open"}`)}, next: "a1"},
		}},
		"WS2": {pages: map[string]journalPage{
			"": {events: []automation.JournalEvent{
				issueEvent("b1", "issue.create", "u", "1", `{"status":"open"}`),
				issueEvent("b2", "issue.create", "u", "2", `{"status":"open"}`),
			}, next: "b2"},
		}},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	seenStart(cursors, "WS2")
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{Store: s, Source: emitter, Reader: reader, Cursors: cursors}

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
	counts := map[string]int{}
	for _, emission := range emitter.snapshot() {
		counts[emission.workspace]++
	}
	if counts["WS"] != 1 || counts["WS2"] != 2 {
		t.Fatalf("emissions by workspace = %+v, want WS=1 WS2=2", counts)
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

// TestIssueJournalBridgeBackoffAfterPersistentEmissionFailure is the
// regression for the serve log/disk incident: a durable admission rejection
// used to be retried and logged on every two-second tick because only reader
// failures participated in backoff. Every workspace-pass failure now shares
// the same exponential window.
func TestIssueJournalBridgeBackoffAfterPersistentEmissionFailure(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{
			issueEvent("emit-1", "issue.create", "user:alice", "TASK-1", `{"status":"open"}`),
		}, next: "emit-1"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	emitter := &failingInternalEmitter{err: errors.New("persistent admission rejection")}
	bridge := &trigger.IssueJournalBridge{
		Store: memstore.New(), Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors,
	}

	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatal("pass 1 error = nil, want admission failure")
	}
	if reader.callCount() != 1 || emitter.callCount() != 1 {
		t.Fatalf("pass 1 reader/emitter calls = %d/%d, want 1/1", reader.callCount(), emitter.callCount())
	}
	if out, err := bridge.RunOnce(t.Context()); err != nil || out.BackedOff != 1 {
		t.Fatalf("pass 2 result/error = %+v/%v, want one backed-off workspace", out, err)
	}
	if reader.callCount() != 1 || emitter.callCount() != 1 {
		t.Fatalf("backoff performed work: reader/emitter calls = %d/%d", reader.callCount(), emitter.callCount())
	}
	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatal("pass 3 error = nil, want second admission failure")
	}
	if reader.callCount() != 2 || emitter.callCount() != 2 {
		t.Fatalf("pass 3 reader/emitter calls = %d/%d, want 2/2", reader.callCount(), emitter.callCount())
	}
	// Two consecutive failures produce a three-pass skip window.
	for pass := 4; pass <= 6; pass++ {
		out, err := bridge.RunOnce(t.Context())
		if err != nil || out.BackedOff != 1 {
			t.Fatalf("pass %d result/error = %+v/%v, want backed off", pass, out, err)
		}
	}
	if reader.callCount() != 2 || emitter.callCount() != 2 {
		t.Fatalf("expanded backoff performed work: reader/emitter calls = %d/%d", reader.callCount(), emitter.callCount())
	}
}

func TestIssueJournalBridgeNilDependenciesRejected(t *testing.T) {
	tests := []struct {
		name   string
		bridge *trigger.IssueJournalBridge
	}{
		{name: "nil store", bridge: &trigger.IssueJournalBridge{Source: &capturingInternalEmitter{}, Reader: &fakeIssueJournalReader{}}},
		{name: "nil source", bridge: &trigger.IssueJournalBridge{Store: memstore.New(), Reader: &fakeIssueJournalReader{}}},
		{name: "nil reader", bridge: &trigger.IssueJournalBridge{Store: memstore.New(), Source: &capturingInternalEmitter{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.bridge.RunOnce(t.Context()); !errors.Is(err, persistence.ErrInvalid) {
				t.Fatalf("RunOnce err = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestIssueJournalBridgeNoBindingAdvancesCursor proves a no-listener emission
// (persistence.ErrNotFound — no binding on internal.issue.created) does NOT stall
// the bridge: the cursor advances past the unbound entry.
func TestIssueJournalBridgeNoBindingAdvancesCursor(t *testing.T) {
	s := memstore.New() // no internal binding seeded
	if _, err := s.Workspaces().Create(t.Context(), workspaceowner.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create WS: %v", err)
	}
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []automation.JournalEvent{issueEvent("5", "issue.create", "u", "1", `{"status":"open"}`)}, next: "5"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge := &trigger.IssueJournalBridge{Store: s, Source: &failingInternalEmitter{err: persistence.ErrNotFound}, Reader: reader, WorkspaceKey: "WS", Cursors: cursors}

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

func (r *perWorkspaceReader) ListIssueEvents(ctx context.Context, ws, afterCursor string, limit int) ([]automation.JournalEvent, string, bool, error) {
	inner, ok := r.m[ws]
	if !ok {
		return nil, afterCursor, false, nil
	}
	return inner.ListIssueEvents(ctx, ws, afterCursor, limit)
}
