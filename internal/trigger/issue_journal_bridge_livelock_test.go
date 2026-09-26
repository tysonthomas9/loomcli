package trigger_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

const wantPageCap = 500

const invalidJournalAction = "issue create"

func newBridgeWithReader(t *testing.T, reader store.IssueJournalReader, cursors trigger.IssueJournalCursorStore) *trigger.IssueJournalBridge {
	t.Helper()
	s := memstore.New()
	setupInternalBinding(t, s)
	return &trigger.IssueJournalBridge{
		Store:        s,
		Source:       &trigger.InternalSource{Store: s},
		Reader:       reader,
		WorkspaceKey: "WS",
		Cursors:      cursors,
	}
}

func auditLogger(b *trigger.IssueJournalBridge) *bytes.Buffer {
	var audit bytes.Buffer
	b.Logger = slog.New(slog.NewTextHandler(&audit, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &audit
}

func TestDrainFrom_AdvancesOnServerCursorWhenBatchEmpty(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: nil, next: "B", hasMore: true},
		"B": {events: []store.JournalEvent{
			issueEvent("C1", "issue.create", "u", "42", `{"status":"open"}`),
		}, next: "C1"},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, s := newBridge(t, reader, cursors)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("result = %+v, want 1 emitted after stepping over the empty page", out)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
	if got, _ := cursors.Load("WS"); got != "C1" {
		t.Fatalf("cursor = %q, want C1 (emitted id wins over the server cursor)", got)
	}
	if reader.callCount() != 2 {
		t.Fatalf("reader calls = %d, want 2 (empty page, then the page with work)", reader.callCount())
	}
}

func TestDrainFrom_StopsWhenCursorDoesNotAdvance(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: nil, next: "A", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, _ := newBridge(t, reader, cursors)
	audit := auditLogger(bridge)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce err = %v, want nil (a stalled server is not a sweep failure)", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("result = %+v, want nothing emitted", out)
	}
	if n := reader.callCount(); n > 3 {
		t.Fatalf("reader calls = %d, want the sweep bounded (<= 3)", n)
	}
	if !strings.Contains(audit.String(), "cursor did not advance") {
		t.Fatalf("audit %q missing the no-progress WARN", audit.String())
	}
}

func TestDrainFrom_RepeatedStallWidensBackoffWindow(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: nil, next: "A", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, _ := newBridge(t, reader, cursors)

	want := []int{1, 1, 2, 2, 2, 2, 3}
	for sweep, wantCalls := range want {
		if _, err := bridge.RunOnce(t.Context()); err != nil {
			t.Fatalf("RunOnce sweep %d: %v", sweep+1, err)
		}
		if got := reader.callCount(); got != wantCalls {
			t.Fatalf("reader calls after sweep %d = %d, want %d (window 1,3,... not a constant half rate)",
				sweep+1, got, wantCalls)
		}
	}
}

func TestDrainFrom_ProgressClearsStallBackoff(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: nil, next: "A", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, _ := newBridge(t, reader, cursors)

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if reader.callCount() != 1 {
		t.Fatalf("calls after sweep 2 = %d, want 1", reader.callCount())
	}

	reader.mu.Lock()
	reader.pages["A"] = journalPage{events: nil, next: "B", hasMore: true}
	reader.pages["B"] = journalPage{events: nil, next: "B", hasMore: true}
	reader.mu.Unlock()

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("sweep 3: %v", err)
	}
	if got := reader.callCount(); got != 3 {
		t.Fatalf("calls after sweep 3 = %d, want 3 (A then B)", got)
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("sweep 4: %v", err)
	}
	if got := reader.callCount(); got != 3 {
		t.Fatalf("calls after sweep 4 = %d, want 3 (still inside the window)", got)
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("sweep 5: %v", err)
	}
	if got := reader.callCount(); got != 4 {
		t.Fatalf("calls after sweep 5 = %d, want 4 (window reset to 1 by the advance, not widened to 3)", got)
	}
}

func TestDrainFrom_FirstEventEmitFailureDoesNotAdvanceCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: []store.JournalEvent{
			issueEvent("E1", invalidJournalAction, "u", "1", `{"status":"open"}`),
		}, next: "B", hasMore: true},
		"B": {events: nil, next: "B"},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, s := newBridge(t, reader, cursors)
	bridge.ActionAllowlist = []string{invalidJournalAction}

	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatalf("RunOnce err = nil, want the emit failure surfaced")
	}
	if got, _ := cursors.Load("WS"); got != "A" {
		t.Fatalf("cursor = %q, want A (neither the server cursor B nor the unhandled entry E1)", got)
	}
	if events, _ := internalCounts(t, s); events != 0 {
		t.Fatalf("events = %d, want 0 (nothing was handled)", events)
	}

	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatalf("second RunOnce err = nil, want the entry retried and failing again")
	}
	if got := reader.callCount(); got != 2 {
		t.Fatalf("reader calls = %d, want 2 (page A re-read)", got)
	}
	if got, _ := cursors.Load("WS"); got != "A" {
		t.Fatalf("cursor after retry = %q, want A", got)
	}
}

func TestDrainFrom_PartialEmitFailureSavesEmittedCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: []store.JournalEvent{
			issueEvent("E1", "issue.create", "u", "1", `{"status":"open"}`),
			issueEvent("E2", invalidJournalAction, "u", "2", `{"status":"open"}`),
		}, next: "B", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, s := newBridge(t, reader, cursors)
	bridge.ActionAllowlist = []string{"issue.create", invalidJournalAction}

	out, err := bridge.RunOnce(t.Context())
	if err == nil {
		t.Fatalf("RunOnce err = nil, want the second entry's emit failure surfaced")
	}
	if out.Emitted != 1 {
		t.Fatalf("result = %+v, want 1 emitted before the failure", out)
	}
	if got, _ := cursors.Load("WS"); got != "E1" {
		t.Fatalf("cursor = %q, want E1 (the last durably handled entry, never the server's B)", got)
	}
	if events, _ := internalCounts(t, s); events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
}

type pagingJournalReader struct {
	mu    sync.Mutex
	calls int
}

func (r *pagingJournalReader) ListIssueEvents(_ context.Context, _, _ string, _ int) ([]store.JournalEvent, string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	id := fmt.Sprintf("e%d", r.calls)
	return []store.JournalEvent{issueEvent(id, "issue.update", "u", "1", `{"status":"open"}`)}, id, true, nil
}

func (r *pagingJournalReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestDrainFrom_PageCap(t *testing.T) {
	reader := &pagingJournalReader{}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge := newBridgeWithReader(t, reader, cursors)
	audit := auditLogger(bridge)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce err = %v, want nil (the cap is a pause, not a failure)", err)
	}
	if got := reader.callCount(); got != wantPageCap {
		t.Fatalf("reader calls = %d, want exactly %d", got, wantPageCap)
	}
	if out.Skipped != wantPageCap {
		t.Fatalf("result = %+v, want %d skipped", out, wantPageCap)
	}
	wantCursor := fmt.Sprintf("e%d", wantPageCap)
	if got, _ := cursors.Load("WS"); got != wantCursor {
		t.Fatalf("cursor = %q, want %q (resume exactly where the cap stopped)", got, wantCursor)
	}
	if !strings.Contains(audit.String(), "page cap reached") {
		t.Fatalf("audit %q missing the page-cap record", audit.String())
	}
}

func TestJournalTail_StopsWhenCursorDoesNotAdvance(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: nil, next: "", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	bridge, _ := newBridge(t, reader, cursors)
	audit := auditLogger(bridge)

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce err = %v, want nil", err)
	}
	if out.FastForwarded != 1 {
		t.Fatalf("result = %+v, want the workspace fast-forwarded", out)
	}
	if n := reader.callCount(); n > 3 {
		t.Fatalf("reader calls = %d, want the fast-forward bounded (<= 3)", n)
	}
	if !strings.Contains(audit.String(), "cursor did not advance") {
		t.Fatalf("audit %q missing the no-progress WARN", audit.String())
	}
}

func TestDrainFrom_ContextCancelledBeforeRead(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"A": {events: nil, next: "B", hasMore: true},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "A")
	bridge, _ := newBridge(t, reader, cursors)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := bridge.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce err = %v, want context.Canceled", err)
	}
	if got := reader.callCount(); got != 0 {
		t.Fatalf("reader calls = %d, want 0", got)
	}
}

type cancellingJournalReader struct {
	mu     sync.Mutex
	calls  int
	cancel context.CancelFunc
}

func (r *cancellingJournalReader) ListIssueEvents(_ context.Context, _, _ string, _ int) ([]store.JournalEvent, string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.cancel()
	return nil, fmt.Sprintf("c%d", r.calls), true, nil
}

func TestDrainFrom_ContextCancelledMidDrainStopsBeforeNextRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &cancellingJournalReader{cancel: cancel}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	bridge := newBridgeWithReader(t, reader, cursors)

	if _, err := bridge.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce err = %v, want context.Canceled", err)
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1 (cancellation costs no extra request)", reader.calls)
	}
}
