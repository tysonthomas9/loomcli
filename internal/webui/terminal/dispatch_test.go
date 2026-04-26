package terminal

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// mockCall captures one invocation against a mockPTYSource.
type mockCall struct {
	method  string
	key     SessionKey
	cols    uint16
	rows    uint16
	argv    []string
	connID  string
	wsID    string
	maxSess int // for SessionCountFor parity, retained for future expansion
}

// mockPTYSource is an in-package test double that records every call. Kept
// inside dispatch_test.go (not the public API) because its only purpose is
// asserting on the Dispatcher's forwarding behavior.
type mockPTYSource struct {
	mu              sync.Mutex
	calls           []mockCall
	attachReattach  bool
	attachErr       error
	killErr         error
	hasSession      bool
	attachmentCount int
	sessionCount    int
	sessionCountFor int
	maxSessions     int
}

func (m *mockPTYSource) record(c mockCall) {
	m.mu.Lock()
	m.calls = append(m.calls, c)
	m.mu.Unlock()
}

// snapshot returns a copy of the recorded calls.
func (m *mockPTYSource) snapshot() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockPTYSource) AttachSession(key SessionKey, cols, rows uint16, argv []string) (Attachment, bool, error) {
	// Defensive copy of argv: callers may reuse the slice.
	var argvCopy []string
	if argv != nil {
		argvCopy = append([]string(nil), argv...)
	}
	m.record(mockCall{method: "AttachSession", key: key, cols: cols, rows: rows, argv: argvCopy})
	if m.attachErr != nil {
		return nil, false, m.attachErr
	}
	return nil, m.attachReattach, nil
}

func (m *mockPTYSource) Detach(key SessionKey, connID string) {
	m.record(mockCall{method: "Detach", key: key, connID: connID})
}

func (m *mockPTYSource) Kill(key SessionKey) error {
	m.record(mockCall{method: "Kill", key: key})
	return m.killErr
}

func (m *mockPTYSource) HasSession(key SessionKey) bool {
	m.record(mockCall{method: "HasSession", key: key})
	return m.hasSession
}

func (m *mockPTYSource) AttachmentCount(key SessionKey) int {
	m.record(mockCall{method: "AttachmentCount", key: key})
	return m.attachmentCount
}

func (m *mockPTYSource) SessionCount() int {
	m.record(mockCall{method: "SessionCount"})
	return m.sessionCount
}

func (m *mockPTYSource) SessionCountFor(wsID string) int {
	m.record(mockCall{method: "SessionCountFor", wsID: wsID})
	return m.sessionCountFor
}

func (m *mockPTYSource) MaxSessions() int {
	m.record(mockCall{method: "MaxSessions"})
	return m.maxSessions
}

var _ PTYSource = (*mockPTYSource)(nil)

// TestDispatcher_DefaultOff_FullForwardingToEphemeral covers plan-rbp.5.3:
// when persistent is nil, the dispatcher must forward every PTYSource method
// to the ephemeral backend with arguments unchanged. This is the regression
// contract — flipping the feature flag off must produce bit-for-bit
// equivalent behavior to using MultiPTYManager directly.
func TestDispatcher_DefaultOff_FullForwardingToEphemeral(t *testing.T) {
	eph := &mockPTYSource{
		attachReattach:  true,
		hasSession:      true,
		attachmentCount: 3,
		sessionCount:    7,
		sessionCountFor: 5,
		maxSessions:     11,
	}
	// Persistent nil + classify returning ephemeral always.
	d := NewDispatcher(eph, nil, func(SessionKey) AgentKind { return AgentEphemeral })

	key := SessionKey{Workspace: "ws-1", Name: "main"}
	argv := []string{"-c", "echo hello"}

	att, reattached, err := d.AttachSession(key, 80, 24, argv)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if att != nil {
		t.Errorf("AttachSession att = %v, want nil (mock returns nil)", att)
	}
	if !reattached {
		t.Errorf("AttachSession reattached = false, want true (mock returns true)")
	}

	d.Detach(key, "conn-7")

	if err := d.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !d.HasSession(key) {
		t.Errorf("HasSession = false, want true")
	}
	if got := d.AttachmentCount(key); got != 3 {
		t.Errorf("AttachmentCount = %d, want 3", got)
	}
	if got := d.SessionCount(); got != 7 {
		t.Errorf("SessionCount = %d, want 7", got)
	}
	if got := d.SessionCountFor("ws-1"); got != 5 {
		t.Errorf("SessionCountFor = %d, want 5", got)
	}
	if got := d.MaxSessions(); got != 11 {
		t.Errorf("MaxSessions = %d, want 11", got)
	}

	calls := eph.snapshot()
	wantMethods := []string{
		"AttachSession", "Detach", "Kill", "HasSession",
		"AttachmentCount", "SessionCount", "SessionCountFor", "MaxSessions",
	}
	if len(calls) != len(wantMethods) {
		t.Fatalf("recorded %d calls, want %d (%v)", len(calls), len(wantMethods), calls)
	}
	for i, want := range wantMethods {
		if calls[i].method != want {
			t.Errorf("call[%d].method = %q, want %q", i, calls[i].method, want)
		}
	}

	// Argument-faithfulness checks.
	if calls[0].key != key || calls[0].cols != 80 || calls[0].rows != 24 || len(calls[0].argv) != 2 || calls[0].argv[0] != "-c" {
		t.Errorf("AttachSession args mangled: %+v", calls[0])
	}
	if calls[1].key != key || calls[1].connID != "conn-7" {
		t.Errorf("Detach args mangled: %+v", calls[1])
	}
	if calls[2].key != key {
		t.Errorf("Kill key = %v, want %v", calls[2].key, key)
	}
	if calls[3].key != key || calls[4].key != key {
		t.Errorf("Has/AttachmentCount key mismatch")
	}
	if calls[6].wsID != "ws-1" {
		t.Errorf("SessionCountFor wsID = %q, want ws-1", calls[6].wsID)
	}
}

// TestDispatcher_DefaultOff_NoClassify covers the "classify is nil" path:
// every key must still route to ephemeral.
func TestDispatcher_DefaultOff_NoClassify(t *testing.T) {
	eph := &mockPTYSource{}
	d := NewDispatcher(eph, nil, nil)

	key := SessionKey{Workspace: "ws", Name: "n"}
	_, _, _ = d.AttachSession(key, 80, 24, nil)
	d.Detach(key, "c")
	_ = d.Kill(key)
	_ = d.HasSession(key)
	_ = d.AttachmentCount(key)
	_ = d.SessionCount()
	_ = d.SessionCountFor("ws")
	_ = d.MaxSessions()

	if got := len(eph.snapshot()); got != 8 {
		t.Errorf("ephemeral recorded %d calls, want 8 (no-classify must always route ephemeral)", got)
	}
}

// TestDispatcher_PersistentClassified_RoutesToAgentd verifies that when both
// backends are wired and classify reports AgentPersistent, the persistent
// backend receives the call.
func TestDispatcher_PersistentClassified_RoutesToAgentd(t *testing.T) {
	eph := &mockPTYSource{maxSessions: 100, sessionCount: 1, sessionCountFor: 1}
	per := &mockPTYSource{maxSessions: 7, sessionCount: 4, sessionCountFor: 4}

	classified := SessionKey{Workspace: "ws-persistent", Name: "agent-1"}

	classify := func(k SessionKey) AgentKind {
		if k.Workspace == classified.Workspace {
			return AgentPersistent
		}
		return AgentEphemeral
	}
	d := NewDispatcher(eph, per, classify)

	if _, _, err := d.AttachSession(classified, 80, 24, []string{"echo", "hi"}); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	d.Detach(classified, "c1")
	if err := d.Kill(classified); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_ = d.HasSession(classified)
	_ = d.AttachmentCount(classified)

	// Workspace-level routing: persistent path.
	if got := d.SessionCountFor("ws-persistent"); got != 4 {
		t.Errorf("SessionCountFor(persistent) = %d, want 4", got)
	}

	// SessionCount + MaxSessions still come from ephemeral by contract.
	if got := d.SessionCount(); got != 1 {
		t.Errorf("SessionCount = %d, want 1 (ephemeral)", got)
	}
	if got := d.MaxSessions(); got != 100 {
		t.Errorf("MaxSessions = %d, want 100 (ephemeral)", got)
	}

	if got := len(eph.snapshot()); got != 2 {
		t.Errorf("ephemeral got %d calls, want 2 (SessionCount, MaxSessions only); %v", got, eph.snapshot())
	}

	perCalls := per.snapshot()
	wantMethods := []string{"AttachSession", "Detach", "Kill", "HasSession", "AttachmentCount", "SessionCountFor"}
	if len(perCalls) != len(wantMethods) {
		t.Fatalf("persistent got %d calls, want %d: %v", len(perCalls), len(wantMethods), perCalls)
	}
	for i, want := range wantMethods {
		if perCalls[i].method != want {
			t.Errorf("persistent call[%d].method = %q, want %q", i, perCalls[i].method, want)
		}
	}
	if perCalls[0].argv == nil || perCalls[0].argv[0] != "echo" {
		t.Errorf("AttachSession argv not preserved: %+v", perCalls[0])
	}
	if perCalls[5].wsID != "ws-persistent" {
		t.Errorf("SessionCountFor wsID = %q, want ws-persistent", perCalls[5].wsID)
	}
}

// TestDispatcher_PersistentClassifiedButNil_FallsBackToEphemeral exercises
// the misconfig path: classify says persistent, but the dispatcher was
// constructed with persistent == nil. Behavior must fall back to ephemeral
// and not error.
func TestDispatcher_PersistentClassifiedButNil_FallsBackToEphemeral(t *testing.T) {
	eph := &mockPTYSource{}
	d := NewDispatcher(eph, nil, func(SessionKey) AgentKind { return AgentPersistent })

	key := SessionKey{Workspace: "ws", Name: "n"}
	if _, _, err := d.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("AttachSession returned err = %v, want nil (must not error on fallback)", err)
	}
	if got := len(eph.snapshot()); got != 1 {
		t.Errorf("ephemeral recorded %d calls, want 1", got)
	}
}

// TestDispatcher_FallbackWarningEmittedOnce verifies that the fallback
// warning is emitted at most once per (workspace, name) tuple. The marker
// map is package-private so we observe the side effect by running the
// AttachSession path many times with the same key and asserting that
// warnedFallback only contains one entry, then doing the same with a
// different key and asserting two entries.
func TestDispatcher_FallbackWarningEmittedOnce(t *testing.T) {
	eph := &mockPTYSource{}
	d := NewDispatcher(eph, nil, func(SessionKey) AgentKind { return AgentPersistent })

	keyA := SessionKey{Workspace: "ws-A", Name: "agent-A"}
	keyB := SessionKey{Workspace: "ws-B", Name: "agent-B"}

	// 5 hits for keyA, 3 hits for keyB.
	for i := 0; i < 5; i++ {
		_, _, _ = d.AttachSession(keyA, 80, 24, nil)
		d.Detach(keyA, "c")
		_ = d.Kill(keyA)
		_ = d.HasSession(keyA)
		_ = d.AttachmentCount(keyA)
	}
	for i := 0; i < 3; i++ {
		_, _, _ = d.AttachSession(keyB, 80, 24, nil)
	}

	d.mu.Lock()
	got := len(d.warnedFallback)
	_, hasA := d.warnedFallback[keyA]
	_, hasB := d.warnedFallback[keyB]
	d.mu.Unlock()

	if got != 2 {
		t.Errorf("warnedFallback has %d entries, want 2", got)
	}
	if !hasA || !hasB {
		t.Errorf("missing entries: hasA=%v hasB=%v", hasA, hasB)
	}
}

// TestDispatcher_FallbackWarningRaceFree exercises pickForWithFallback
// concurrently with the same key. The map must only ever observe one entry
// for that key — running with -race must not flag any data race on the
// warnedFallback map.
func TestDispatcher_FallbackWarningRaceFree(t *testing.T) {
	eph := &mockPTYSource{}
	d := NewDispatcher(eph, nil, func(SessionKey) AgentKind { return AgentPersistent })
	key := SessionKey{Workspace: "ws", Name: "n"}

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = d.HasSession(key)
			}
		}()
	}
	wg.Wait()

	d.mu.Lock()
	got := len(d.warnedFallback)
	d.mu.Unlock()
	if got != 1 {
		t.Errorf("warnedFallback has %d entries, want 1", got)
	}
}

// TestDispatcher_PropagatesAttachError verifies that errors from the
// chosen backend are returned unchanged.
func TestDispatcher_PropagatesAttachError(t *testing.T) {
	wantErr := errors.New("attach failed")
	eph := &mockPTYSource{attachErr: wantErr}
	d := NewDispatcher(eph, nil, nil)

	_, _, err := d.AttachSession(SessionKey{Workspace: "w", Name: "n"}, 80, 24, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

// TestDispatcher_ImplementsPTYSource is a static-typing sanity check.
func TestDispatcher_ImplementsPTYSource(t *testing.T) {
	var _ PTYSource = (*Dispatcher)(nil)
	d := NewDispatcher(&mockPTYSource{}, nil, nil)
	var _ PTYSource = d
}

// TestDispatcher_ConcurrentAttachStableCount is a smoke test that hammers
// AttachSession concurrently to confirm no atomic-counter assumptions in
// the dispatcher leak across goroutines.
func TestDispatcher_ConcurrentAttachStableCount(t *testing.T) {
	eph := &mockPTYSource{}
	d := NewDispatcher(eph, nil, nil)

	const goroutines = 16
	var counter atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_, _, _ = d.AttachSession(SessionKey{Workspace: "w", Name: "n"}, 80, 24, nil)
				counter.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := counter.Load(); got != int64(goroutines*25) {
		t.Errorf("counter = %d, want %d", got, goroutines*25)
	}
	if got := len(eph.snapshot()); got != goroutines*25 {
		t.Errorf("ephemeral recorded %d, want %d", got, goroutines*25)
	}
}
