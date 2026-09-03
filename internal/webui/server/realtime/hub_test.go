package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestHub_RegisterClientAcknowledgesOnlyAfterDispatch(t *testing.T) {
	h := NewHub()
	dispatchStarted := make(chan struct{})
	releaseDispatch := make(chan struct{})
	h.dispatchBarrier = func(kind hubDispatchKind) {
		if kind == hubDispatchRegister {
			close(dispatchStarted)
			<-releaseDispatch
		}
	}
	go h.Run()
	defer h.Stop()

	client := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	registered := make(chan error, 1)
	go func() { registered <- h.RegisterClient(context.Background(), client) }()
	<-dispatchStarted
	if got := h.ClientCount(); got != 0 {
		t.Fatalf("client count before dispatch release = %d, want 0", got)
	}
	select {
	case err := <-registered:
		t.Fatalf("RegisterClient returned before client was added: %v", err)
	default:
	}
	close(releaseDispatch)
	if err := <-registered; err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if got := h.ClientCount(); got != 1 {
		t.Fatalf("client count after ack = %d, want 1", got)
	}
}

func TestHub_StopReleasesRegistrationWaiter(t *testing.T) {
	h := NewHub()
	dispatchStarted := make(chan struct{})
	releaseDispatch := make(chan struct{})
	h.dispatchBarrier = func(kind hubDispatchKind) {
		if kind == hubDispatchRegister {
			close(dispatchStarted)
			<-releaseDispatch
		}
	}
	go h.Run()

	result := make(chan error, 1)
	go func() {
		result <- h.RegisterClient(context.Background(), NewClient(1, 1, "0", nil, "ws-1"))
	}()
	<-dispatchStarted
	h.Stop()
	if err := <-result; !errors.Is(err, errHubStopped) {
		t.Fatalf("RegisterClient error = %v, want hub stopped", err)
	}
	close(releaseDispatch)
}

func TestHub_RegisterClientCancellationCannotLeakClient(t *testing.T) {
	h := NewHub()
	dispatchStarted := make(chan struct{})
	releaseDispatch := make(chan struct{})
	h.dispatchBarrier = func(kind hubDispatchKind) {
		if kind == hubDispatchRegister {
			close(dispatchStarted)
			<-releaseDispatch
		}
	}
	go h.Run()
	t.Cleanup(h.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- h.RegisterClient(ctx, NewClient(1, 1, "0", nil, "ws-1"))
	}()
	<-dispatchStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("RegisterClient error = %v, want context.Canceled", err)
	}
	if got := h.ClientCount(); got != 0 {
		t.Fatalf("client count after canceled registration = %d, want 0", got)
	}
	close(releaseDispatch)
}

func TestHub_BroadcastShortCircuitsWithoutWorkspaceClients(t *testing.T) {
	h := NewHub()
	h.Broadcast(&MutationPayload{Cursor: "c1.event", WorkspaceID: "ws-empty"})
	if got := len(h.broadcast); got != 0 {
		t.Fatalf("broadcast queue length = %d, want 0", got)
	}
	if got := h.GetRetryQueueDepth(); got != 0 {
		t.Fatalf("retry queue length = %d, want 0", got)
	}
}

func TestNewHub(t *testing.T) {
	h := NewHub()
	if h == nil {
		t.Fatal("expected non-nil hub")
	}
	if h.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", h.ClientCount())
	}
}

func TestHub_RegisterAndUnregister(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	_ = h.RegisterClient(context.Background(), c)

	// Give the run loop time to process
	time.Sleep(20 * time.Millisecond)
	if got := h.ClientCount(); got != 1 {
		t.Fatalf("expected 1 client, got %d", got)
	}

	h.UnregisterClient(c)
	time.Sleep(20 * time.Millisecond)
	if got := h.ClientCount(); got != 0 {
		t.Fatalf("expected 0 clients after unregister, got %d", got)
	}
}

func TestHub_BroadcastDelivers(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	_ = h.RegisterClient(context.Background(), c)
	time.Sleep(20 * time.Millisecond)

	h.Broadcast(&MutationPayload{
		Type:        "create",
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
	})

	select {
	case m := <-c.Send():
		if m.IssueID != "issue-1" {
			t.Errorf("expected issue-1, got %s", m.IssueID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestHub_BroadcastDropsEmptyWorkspace(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	_ = h.RegisterClient(context.Background(), c)
	time.Sleep(20 * time.Millisecond)

	// Mutation with empty WorkspaceID should be dropped
	h.Broadcast(&MutationPayload{
		Type:        "create",
		IssueID:     "issue-1",
		WorkspaceID: "",
	})

	select {
	case <-c.Send():
		t.Fatal("should not receive mutation with empty workspace_id")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestHub_WorkspaceFilter(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c1 := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	c2 := NewClient(2, ClientSendBuf, "0", nil, "ws-2")
	_ = h.RegisterClient(context.Background(), c1)
	_ = h.RegisterClient(context.Background(), c2)
	time.Sleep(20 * time.Millisecond)

	h.Broadcast(&MutationPayload{
		Type:        "update",
		IssueID:     "issue-5",
		WorkspaceID: "ws-1",
	})

	select {
	case m := <-c1.Send():
		if m.IssueID != "issue-5" {
			t.Errorf("c1 got wrong issue: %s", m.IssueID)
		}
	case <-time.After(time.Second):
		t.Fatal("c1 timed out")
	}

	select {
	case <-c2.Send():
		t.Fatal("c2 should not receive mutation for ws-1")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestHub_SourceRepoFilter(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := NewClient(1, ClientSendBuf, "0", []string{"repoA"}, "ws-1")
	_ = h.RegisterClient(context.Background(), c)
	time.Sleep(20 * time.Millisecond)

	// Should be delivered (matches filter)
	h.Broadcast(&MutationPayload{
		Type:        "create",
		IssueID:     "i1",
		WorkspaceID: "ws-1",
		SourceRepo:  "repoA",
	})
	select {
	case <-c.Send():
	case <-time.After(time.Second):
		t.Fatal("expected to receive matching repo mutation")
	}

	// Should NOT be delivered (different repo)
	h.Broadcast(&MutationPayload{
		Type:        "create",
		IssueID:     "i2",
		WorkspaceID: "ws-1",
		SourceRepo:  "repoB",
	})
	select {
	case <-c.Send():
		t.Fatal("should not receive mutation for repoB")
	case <-time.After(50 * time.Millisecond):
	}

	// Empty source repo on mutation -> delivered (fan-out)
	h.Broadcast(&MutationPayload{
		Type:        "create",
		IssueID:     "i3",
		WorkspaceID: "ws-1",
		SourceRepo:  "",
	})
	select {
	case <-c.Send():
	case <-time.After(time.Second):
		t.Fatal("expected to receive mutation with empty source_repo")
	}
}

func TestHub_Stop(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := NewClient(1, ClientSendBuf, "0", nil, "ws-1")
	_ = h.RegisterClient(context.Background(), c)
	time.Sleep(20 * time.Millisecond)

	h.Stop()

	// Send channel should be closed after stop
	_, ok := <-c.send
	if ok {
		t.Fatal("expected client send channel to be closed after hub stop")
	}
}

func TestHub_RegisterAfterStop(t *testing.T) {
	h := NewHub()
	go h.Run()
	h.Stop()

	c := NewClient(99, ClientSendBuf, "0", nil, "ws-1")
	if err := h.RegisterClient(context.Background(), c); !errors.Is(err, errHubStopped) {
		t.Fatalf("RegisterClient error = %v, want hub stopped", err)
	}
}

func TestHub_RetryQueue(t *testing.T) {
	h := NewHub()
	h.addClient(NewClient(1, 1, "0", nil, "ws-1"))
	// Don't run the hub yet -- broadcast channel will fill up, triggering retry queue
	for i := 0; i < 256+5; i++ {
		h.Broadcast(&MutationPayload{
			Type:        "create",
			IssueID:     "overflow",
			WorkspaceID: "ws-1",
		})
	}

	if depth := h.GetRetryQueueDepth(); depth != 5 {
		t.Errorf("expected 5 in retry queue, got %d", depth)
	}
}

func TestHub_RetryQueueBoundedUnderHighFanout(t *testing.T) {
	h := NewHub()
	h.addClient(NewClient(1, 1, "0", nil, "ws-1"))
	for i := 0; i < cap(h.broadcast)+1025; i++ {
		h.Broadcast(&MutationPayload{
			Type:        "update",
			IssueID:     "fanout-overflow",
			WorkspaceID: "ws-1",
		})
	}

	if depth := h.GetRetryQueueDepth(); depth != 1024 {
		t.Fatalf("retry queue depth = %d, want bounded cap 1024", depth)
	}
	if dropped := h.GetDroppedCount(); dropped != 1 {
		t.Fatalf("dropped count = %d, want 1 overflow drop", dropped)
	}
}

func TestHub_SlowClientDisconnectedWhenSendBufferFull(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := NewClient(1, 1, "1700000000000-0", nil, "ws-1")
	_ = h.RegisterClient(context.Background(), c)
	waitForHubCondition(t, func() bool { return h.ClientCount() == 1 })

	h.Broadcast(&MutationPayload{Type: "update", IssueID: "first", WorkspaceID: "ws-1"})
	waitForHubCondition(t, func() bool { return len(c.send) == 1 })

	h.Broadcast(&MutationPayload{Type: "update", IssueID: "second", WorkspaceID: "ws-1"})
	waitForHubCondition(t, func() bool { return h.ClientCount() == 0 })

	if _, ok := <-c.send; !ok {
		t.Fatal("expected first buffered mutation to remain readable before channel close")
	}
	if _, ok := <-c.send; ok {
		t.Fatal("expected slow client send channel to close after disconnect")
	}
}

func TestHub_GetActiveSourceRepos(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c1 := NewClient(1, ClientSendBuf, "0", []string{"repoA", "repoB"}, "ws-1")
	c2 := NewClient(2, ClientSendBuf, "0", []string{"repoB", "repoC"}, "ws-1")
	c3 := NewClient(3, ClientSendBuf, "0", nil, "ws-1") // no filter
	_ = h.RegisterClient(context.Background(), c1)
	_ = h.RegisterClient(context.Background(), c2)
	_ = h.RegisterClient(context.Background(), c3)
	time.Sleep(20 * time.Millisecond)

	repos := h.GetActiveSourceRepos()
	sort.Strings(repos)
	expected := []string{"repoA", "repoB", "repoC"}
	if len(repos) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, repos)
	}
	for i, r := range expected {
		if repos[i] != r {
			t.Errorf("repos[%d] = %q, want %q", i, repos[i], r)
		}
	}
}

func TestHub_GetActiveSourceRepos_Empty(t *testing.T) {
	h := NewHub()
	if repos := h.GetActiveSourceRepos(); repos != nil {
		t.Errorf("expected nil, got %v", repos)
	}
}

// --- Pure function tests ---

func TestParseSourceRepos(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"repoA", []string{"repoA"}},
		{"repoA,repoB", []string{"repoA", "repoB"}},
		{" repoA , repoB , ", []string{"repoA", "repoB"}},
		{",,,", nil},
	}
	for _, tt := range tests {
		got := ParseSourceRepos(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ParseSourceRepos(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ParseSourceRepos(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestMatchesWorkspaceFilter(t *testing.T) {
	tests := []struct {
		clientWS   string
		mutationWS string
		want       bool
	}{
		{"ws-1", "ws-1", true},
		{"ws-1", "ws-2", false},
		{"", "ws-1", false},
		{"ws-1", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got := MatchesWorkspaceFilter(tt.clientWS, tt.mutationWS)
		if got != tt.want {
			t.Errorf("MatchesWorkspaceFilter(%q, %q) = %v, want %v", tt.clientWS, tt.mutationWS, got, tt.want)
		}
	}
}

func TestMatchesSourceRepoFilter(t *testing.T) {
	tests := []struct {
		repos []string
		repo  string
		want  bool
	}{
		{nil, "repoA", true},          // no filter = pass all
		{nil, "", true},               // no filter, no repo
		{[]string{"repoA"}, "", true}, // has filter but empty repo = fan-out
		{[]string{"repoA"}, "repoA", true},
		{[]string{"repoA"}, "repoB", false},
		{[]string{"repoA", "repoB"}, "repoB", true},
	}
	for _, tt := range tests {
		got := MatchesSourceRepoFilter(tt.repos, tt.repo)
		if got != tt.want {
			t.Errorf("MatchesSourceRepoFilter(%v, %q) = %v, want %v", tt.repos, tt.repo, got, tt.want)
		}
	}
}

func TestParseLastSince(t *testing.T) {
	tests := []struct {
		name        string
		lastEventID string
		sinceQuery  string
		want        string
	}{
		{"no values", "", "", ""},
		{"header only", "100", "", "100"},
		{"query only", "", "200", "200"},
		{"header wins (larger)", "300", "200", "300"},
		{"query wins (larger)", "100", "400", "400"},
		{"invalid header", "abc", "200", "200"},
		{"fleet cursor header only", "1700000000000-0", "", "1700000000000-0"},
		{"opaque query wins", "100", "c1.cursor", "c1.cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/events", nil)
			if tt.lastEventID != "" {
				r.Header.Set("Last-Event-ID", tt.lastEventID)
			}
			if tt.sinceQuery != "" {
				q := r.URL.Query()
				q.Set("since", tt.sinceQuery)
				r.URL.RawQuery = q.Encode()
			}
			got := ParseLastSince(r)
			if got != tt.want {
				t.Errorf("ParseLastSince() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNextEventID_Monotonic(t *testing.T) {
	id1 := NextEventID()
	id2 := NextEventID()
	if id2 <= id1 {
		t.Errorf("expected id2 (%d) > id1 (%d)", id2, id1)
	}
}

func TestWriteSSEEventUsesMutationCursorAsEventID(t *testing.T) {
	rr := httptest.NewRecorder()
	sw, err := NewWriter(rr)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	err = writeSSEEvent(sw, &MutationPayload{
		Cursor:    "c1.cursor",
		Type:      "update",
		IssueID:   "issue-1",
		Timestamp: "2026-04-30T10:20:30.456Z",
	})
	if err != nil {
		t.Fatalf("writeSSEEvent() error = %v", err)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "id: c1.cursor\n") {
		t.Fatalf("SSE event ID should use mutation cursor, got:\n%s", body)
	}
	if !strings.Contains(body, "event: mutation\n") {
		t.Fatalf("SSE event missing mutation event name, got:\n%s", body)
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient(42, 8, "100", []string{"r1"}, "ws-1")
	if c.ID() != 42 {
		t.Errorf("expected id 42, got %d", c.ID())
	}
	if c.lastSince != "100" {
		t.Errorf("expected lastSince 100, got %s", c.lastSince)
	}
	if len(c.sourceRepos) != 1 || c.sourceRepos[0] != "r1" {
		t.Errorf("unexpected sourceRepos: %v", c.sourceRepos)
	}
	if c.workspaceID != "ws-1" {
		t.Errorf("expected workspaceID ws-1, got %s", c.workspaceID)
	}
}

func waitForHubCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
