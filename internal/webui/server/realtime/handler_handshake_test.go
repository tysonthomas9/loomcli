package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	backendfleet "github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

type recordedFrame struct {
	id    string
	event string
	data  string
}

type recordingFrameWriter struct {
	mu        sync.Mutex
	frames    []recordedFrame
	written   chan recordedFrame
	connected chan struct{}
	once      sync.Once
}

type blockingFrameWriter struct {
	*recordingFrameWriter
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingFrameWriter() *blockingFrameWriter {
	return &blockingFrameWriter{
		recordingFrameWriter: newRecordingFrameWriter(),
		blocked:              make(chan struct{}),
		release:              make(chan struct{}),
	}
}

func (w *blockingFrameWriter) WriteEventID(id, event, data string) error {
	if event == "mutation" {
		w.once.Do(func() {
			close(w.blocked)
			<-w.release
		})
	}
	return w.recordingFrameWriter.WriteEventID(id, event, data)
}

func newRecordingFrameWriter() *recordingFrameWriter {
	return &recordingFrameWriter{
		written:   make(chan recordedFrame, 32),
		connected: make(chan struct{}),
	}
}

func (w *recordingFrameWriter) WriteRetry(ms int) error {
	w.record(recordedFrame{event: "retry", data: string(rune(ms))})
	return nil
}

func (w *recordingFrameWriter) WriteEventID(id, event, data string) error {
	w.record(recordedFrame{id: id, event: event, data: data})
	return nil
}

func (w *recordingFrameWriter) WriteEventNoID(event, data string) error {
	w.record(recordedFrame{event: event, data: data})
	if event == "connected" {
		w.once.Do(func() { close(w.connected) })
	}
	return nil
}

func (w *recordingFrameWriter) WriteResync(id, reason string) error {
	w.record(recordedFrame{id: id, event: "resync", data: fmt.Sprintf(`{"reason":%q}`, reason)})
	return nil
}

func (w *recordingFrameWriter) WriteComment(text string) error {
	w.record(recordedFrame{event: "comment", data: text})
	return nil
}

func (w *recordingFrameWriter) record(frame recordedFrame) {
	w.mu.Lock()
	w.frames = append(w.frames, frame)
	w.mu.Unlock()
	w.written <- frame
}

func (w *recordingFrameWriter) snapshot() []recordedFrame {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedFrame(nil), w.frames...)
}

func TestHandler_RegistersBeforeActivationAndReturnsActivationErrors(t *testing.T) {
	t.Run("registration precedes activation", func(t *testing.T) {
		hub := NewHub()
		go hub.Run()
		t.Cleanup(hub.Stop)
		writer := newRecordingFrameWriter()
		h := NewHandler(HandlerConfig{
			Hub: hub,
			OnAuthenticated: func(context.Context, string) (string, error) {
				if got := hub.ClientCount(); got != 1 {
					t.Fatalf("client count during activation = %d, want 1", got)
				}
				return "c2.head", nil
			},
			WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
		})
		h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
		h.heartbeatInterval = time.Hour

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx))
			close(done)
		}()
		<-writer.connected
		cancel()
		<-done
	})

	t.Run("readiness error returns 503 before streaming", func(t *testing.T) {
		hub := NewHub()
		go hub.Run()
		t.Cleanup(hub.Stop)
		h := NewHandler(HandlerConfig{
			Hub: hub,
			OnAuthenticated: func(context.Context, string) (string, error) {
				return "", errors.New("head drain capped")
			},
			WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
		})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["error"] != "subscription_unavailable" {
			t.Fatalf("body = %q, want subscription_unavailable JSON", rr.Body.String())
		}
	})
}

func TestHandler_CatchUpPagesAndDeduplicatesEveryQueuedCursor(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	writer := newRecordingFrameWriter()
	var calls int
	h := NewHandler(HandlerConfig{
		Hub: hub,
		OnAuthenticated: func(context.Context, string) (string, error) {
			return "c2.head", nil
		},
		OpenMutationSource: openFixtureMutationSource(func(_ context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			if since != "$" || limit != 1 {
				t.Errorf("invalid head request")
			}
			if calls < 2 {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.YzIuYg"}, nil
			}
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.live"}, nil
		}, func(_ context.Context, wsID, since, through string, limit int) (backend.MutationPage, error) {
			if wsID != "ws-1" || limit != catchUpPageLimit {
				t.Fatalf("catch-up request workspace/limit = %q/%d", wsID, limit)
			}
			calls++
			switch calls {
			case 1:
				if since != "c2.YzIuc3RhcnQ" {
					t.Fatalf("first since = %q, want c2.start", since)
				}
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ",
					Events:  []backend.MutationData{{Cursor: "c2.YzIuYQ", Type: backend.MutationUpdate, IssueID: "catch-a"}},
					Cursor:  "c2.YzIuYQ",
					HasMore: true,
				}, nil
			case 2:
				if since != "c2.YzIuYQ" {
					t.Fatalf("second since = %q, want c2.a", since)
				}
				for _, mutation := range []*MutationPayload{
					{Cursor: "c2.YzIuYQ", Type: backend.MutationUpdate, IssueID: "live-duplicate-a", WorkspaceID: "ws-1"},
					{Type: "terminal_session_change", IssueID: "interleaved-terminal", WorkspaceID: "ws-1"},
					{Cursor: "c2.YzIuYg", Type: backend.MutationUpdate, IssueID: "live-duplicate-b", WorkspaceID: "ws-1"},
					{Cursor: "c2.live", Type: backend.MutationUpdate, IssueID: "live-sentinel", WorkspaceID: "ws-1"},
				} {
					hub.Broadcast(mutation)
				}
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ",
					Events: []backend.MutationData{{Cursor: "c2.YzIuYg", Type: backend.MutationUpdate, IssueID: "catch-b"}},
					Cursor: "c2.YzIuYg",
				}, nil
			case 3:
				if since != "c2.YzIuYg" {
					t.Fatalf("live since = %q", since)
				}
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{{Cursor: "c2.live", Type: backend.MutationUpdate, IssueID: "live-sentinel"}}, Cursor: "c2.live"}, nil
			default:
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: since}, nil
			}
		}),

		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/events?since=c2.YzIuc3RhcnQ", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	for {
		var frame recordedFrame
		select {
		case frame = <-writer.written:
		case <-time.After(3 * time.Second):
			t.Fatalf("missing live sentinel; frames=%+v", writer.snapshot())
		}
		if frame.id == "c2.live" {
			break
		}
	}
	cancel()
	<-done

	counts := make(map[string]int)
	for _, frame := range writer.snapshot() {
		if frame.event == "mutation" {
			counts[frame.id]++
		}
	}
	if counts["c2.YzIuYQ"] != 1 || counts["c2.YzIuYg"] != 1 {
		t.Fatalf("deduplicated catch-up cursor counts = %v, want c2.a=1 c2.b=1", counts)
	}
	if counts["c2.live"] != 1 {
		t.Fatalf("live sentinel count = %d, want 1", counts["c2.live"])
	}
}

func TestHandler_OverflowResyncDrainsToHighestOfferedFrameAndKeepsClient(t *testing.T) {
	hub := NewHub()
	dispatchStarted := make(chan struct{})
	releaseDispatch := make(chan struct{})
	hub.dispatchBarrier = func(kind hubDispatchKind) {
		if kind == hubDispatchBroadcast {
			dispatchStarted <- struct{}{}
			<-releaseDispatch
		}
	}
	go hub.Run()
	t.Cleanup(hub.Stop)

	client := NewClient(1, ClientSendBuf, "c2.YzIuc3RhcnQ", nil, "ws-1")
	if err := hub.RegisterClient(context.Background(), client); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	writer := newBlockingFrameWriter()
	h := NewHandler(HandlerConfig{Hub: hub})
	h.heartbeatInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = h.streamLoop(writer, client, ctx, nil)
		close(done)
	}()

	dispatch := func(n int) {
		t.Helper()
		hub.Broadcast(&MutationPayload{
			Cursor:      testScopedCursor(fmt.Sprintf("c2.%03d", n)),
			Type:        "update",
			IssueID:     fmt.Sprintf("F%d", n),
			WorkspaceID: "ws-1",
		})
		<-dispatchStarted
		releaseDispatch <- struct{}{}
	}
	dispatch(1)
	<-writer.blocked
	for n := 2; n <= 300; n++ {
		dispatch(n)
	}

	// Holding the next hub dispatch proves F300 has completed fan-out without
	// polling or sleeping, while ensuring F301 cannot enter the client queue.
	hub.Broadcast(&MutationPayload{Cursor: "c2.YzIuMzAx", Type: "update", IssueID: "F301", WorkspaceID: "ws-1"})
	<-dispatchStarted
	client.resyncMu.Lock()
	dropped := client.dropped
	client.resyncMu.Unlock()
	if !client.pendingResync.Load() || dropped.seq != 300 || dropped.cursor != testScopedCursor("c2.300") {
		t.Fatalf("pending drop = (%v, %d, %q), want (true, 300, c2.300)", client.pendingResync.Load(), dropped.seq, dropped.cursor)
	}
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("client count during burst = %d, want 1", got)
	}

	close(writer.release)
	for frame := range writer.written {
		if frame.event == "resync" {
			if frame.id != testScopedCursor("c2.300") || frame.data != `{"reason":"overflow"}` {
				t.Fatalf("resync frame = %#v, want overflow at c2.300", frame)
			}
			break
		}
	}
	releaseDispatch <- struct{}{}
	for frame := range writer.written {
		if frame.id == "c2.YzIuMzAx" {
			break
		}
	}
	cancel()
	<-done

	frames := writer.snapshot()
	if got := countRecordedEvent(frames, "resync"); got != 1 {
		t.Fatalf("resync frame count = %d, want exactly 1; frames=%#v", got, frames)
	}
	resyncIndex := -1
	for i, frame := range frames {
		if frame.event == "resync" {
			resyncIndex = i
			continue
		}
		if resyncIndex >= 0 && frame.event == "mutation" && frame.id != "c2.YzIuMzAx" {
			t.Fatalf("stale frame written after resync: %#v", frame)
		}
	}
}

func countRecordedEvent(frames []recordedFrame, event string) int {
	count := 0
	for _, frame := range frames {
		if frame.event == event {
			count++
		}
	}
	return count
}

type timeoutResponseController struct {
	mu        sync.Mutex
	flushes   int
	deadlines []time.Time
	flushed   chan int
}

func (c *timeoutResponseController) Flush() error {
	c.mu.Lock()
	c.flushes++
	flushes := c.flushes
	c.mu.Unlock()
	c.flushed <- flushes
	if flushes == 3 {
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (c *timeoutResponseController) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadlines = append(c.deadlines, deadline)
	c.mu.Unlock()
	return nil
}

func TestHandler_WriterDeadlineTimeoutEndsStreamAndUnregisters(t *testing.T) {
	hub := NewHub()
	unregisterStarted := make(chan struct{})
	releaseUnregister := make(chan struct{})
	var unregisterOnce sync.Once
	hub.dispatchBarrier = func(kind hubDispatchKind) {
		if kind == hubDispatchUnregister {
			unregisterOnce.Do(func() { close(unregisterStarted) })
			<-releaseUnregister
		}
	}
	go hub.Run()
	t.Cleanup(hub.Stop)

	controller := &timeoutResponseController{flushed: make(chan int, 4)}
	var output bytes.Buffer
	h := NewHandler(HandlerConfig{
		Hub:              hub,
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	h.heartbeatInterval = time.Hour
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) {
		writer, err := newWriter(&output, controller)
		if writer != nil {
			writer.now = func() time.Time { return time.Unix(123, 0) }
		}
		return writer, err
	}

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events", nil))
		close(done)
	}()
	if got := <-controller.flushed; got != 1 {
		t.Fatalf("first flush = %d, want retry frame", got)
	}
	if got := <-controller.flushed; got != 2 {
		t.Fatalf("second flush = %d, want connected frame", got)
	}
	hub.Broadcast(&MutationPayload{Cursor: "c2.YzIudGltZW91dA", Type: "update", IssueID: "timeout", WorkspaceID: "ws-1"})
	if got := <-controller.flushed; got != 3 {
		t.Fatalf("third flush = %d, want live mutation", got)
	}
	<-done
	<-unregisterStarted
	close(releaseUnregister)

	dummy := NewClient(2, 1, "", nil, "ws-1")
	if err := hub.RegisterClient(context.Background(), dummy); err != nil {
		t.Fatalf("register ordering client: %v", err)
	}
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("client count after timed-out handler cleanup = %d, want only ordering client", got)
	}
	controller.mu.Lock()
	deadlines := append([]time.Time(nil), controller.deadlines...)
	controller.mu.Unlock()
	if len(deadlines) != 6 {
		t.Fatalf("deadline calls = %v, want set and clear around each of 3 frames", deadlines)
	}
	for i := 0; i < len(deadlines); i += 2 {
		if want := time.Unix(123, 0).Add(frameWriteTimeout); !deadlines[i].Equal(want) || !deadlines[i+1].IsZero() {
			t.Fatalf("frame %d deadlines = (%v, %v), want (%v, zero)", i/2+1, deadlines[i], deadlines[i+1], want)
		}
	}
}

func TestHandler_SourceFailuresResyncWithoutAdvancingOrConnected(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			hub := NewHub()
			go hub.Run()
			t.Cleanup(hub.Stop)
			reason := "error"
			if expired {
				reason = "expired"
			}
			h := NewHandler(HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return "ws-1" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, "c2.head"), func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				if expired {
					err := backend.NewBackendError(backend.KindValidation, "catchup", "expired", backend.ErrMutationCursorExpired)
					err.Meta = map[string]string{"cursor": "c2.YzIuZmxvb3I"}
					return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, err
				}
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, errors.New("unavailable")
			})})
			writer := newRecordingFrameWriter()
			h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events?since=c2.YzIub2xk", nil).WithContext(ctx))
			frames := writer.snapshot()
			if len(frames) != 1 || frames[0] != (recordedFrame{event: "resync", data: fmt.Sprintf(`{"reason":%q}`, reason)}) {
				t.Fatalf("source failure must preserve cursor and never report connected: %#v", frames)
			}
		})
	}
}

func TestHandler_PageBudgetSchedulesRemainingSource(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	calls := 0
	h := NewHandler(HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return "ws-1" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, testScopedCursor("budget-12")), func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		calls++
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: testScopedCursor(fmt.Sprintf("budget-%d", calls)), HasMore: calls < 12}, nil
	})})
	writer := newRecordingFrameWriter()
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events?since=c2.YzIuc3RhcnQ", nil).WithContext(ctx))
	}()
	select {
	case <-writer.connected:
	case <-ctx.Done():
		t.Fatal("missing connected after page budget")
	}
	cancel()
	<-done
	if calls != 12 {
		t.Fatalf("calls=%d", calls)
	}
	for _, frame := range writer.snapshot() {
		if frame.event == "resync" {
			t.Fatal("page budget skipped source via resync")
		}
	}
}

func TestHandler_NumericHeaderAndQueryFailBeforeSourceRead(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	var gotSince string
	h := NewHandler(HandlerConfig{
		Hub:             hub,
		OnAuthenticated: func(context.Context, string) (string, error) { return "1000", nil },
		OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, testScopedCursor("300")), func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
			gotSince = since
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{}, Cursor: since}, nil
		}),
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	writer := newRecordingFrameWriter()
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events?since=200", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", "300")
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	defer cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("numeric resume did not close")
	}
	if gotSince != "" {
		t.Fatalf("numeric resume reached source: %q", gotSince)
	}
	frames := writer.snapshot()
	if len(frames) != 1 || frames[0].event != "resync" || frames[0].id != "" {
		t.Fatalf("unexpected rejection frames: %+v", frames)
	}
}

func TestHandler_NumericLastEventIDFailsClosedForBoundedFleetReplay(t *testing.T) {
	const (
		numericCursor = "1700000000000"
		opaqueCursor  = "c2.MTcwMDAwMDAwMDAwMC0w"
	)
	requestedSince := make(chan string, 1)
	fleetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "$" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"source_identity": "s1.Zml4dHVyZQ", "events": []any{}, "cursor": opaqueCursor, "has_more": false})
			return
		}
		if r.URL.Query().Get("through") != opaqueCursor {
			t.Errorf("missing bounded fence")
		}
		requestedSince <- since
		w.Header().Set("Content-Type", "application/json")
		if since != opaqueCursor {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": "invalid_parameter", "message": "invalid since parameter: expected opaque cursor token",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"source_identity": "s1.Zml4dHVyZQ",
			"events":          []any{}, "cursor": opaqueCursor, "has_more": false,
		})
	}))
	t.Cleanup(fleetServer.Close)

	fleetBackend, err := backendfleet.New(backendfleet.Config{BaseURL: fleetServer.URL, WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("fleet.New: %v", err)
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	writer := newRecordingFrameWriter()
	h := NewHandler(HandlerConfig{
		Hub: hub,
		OpenMutationSource: openFixtureMutationSource(func(ctx context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			return fleetBackend.GetMutationsAfter(ctx, since, limit)
		}, func(ctx context.Context, _ string, since, through string, limit int) (backend.MutationPage, error) {
			return fleetBackend.GetMutationsThrough(ctx, since, through, limit)
		}),

		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", numericCursor)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("numeric resume did not fail closed")
	}
	select {
	case since := <-requestedSince:
		t.Fatalf("noncanonical numeric resume reached bounded Fleet HTTP: %q", since)
	default:
	}
	frames := writer.snapshot()
	if len(frames) != 1 || frames[0] != (recordedFrame{event: "resync", data: `{"reason":"error"}`}) {
		t.Fatalf("numeric resume must not advance checkpoint or connect: %#v", frames)
	}
	cancel()
}
