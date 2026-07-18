package driverapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// watchTestTimeout bounds every streaming test so a broken stream fails
// instead of hanging.
const watchTestTimeout = 10 * time.Second

// sseFrame is one parsed SSE frame (event or comment).
type sseFrame struct {
	id      string
	event   string
	data    string
	comment string
}

// sseStream wraps an open watch response for frame-at-a-time reading.
type sseStream struct {
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc
}

func (s *sseStream) close() {
	s.cancel()
	_ = s.resp.Body.Close()
}

// nextFrame reads one SSE frame (terminated by a blank line). io.EOF
// surfaces as ok=false so callers can assert stream termination.
func (s *sseStream) nextFrame(t *testing.T) (sseFrame, bool) {
	t.Helper()
	var frame sseFrame
	sawField := false
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if (err == io.EOF || strings.Contains(err.Error(), "closed")) && !sawField {
				return sseFrame{}, false
			}
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if sawField {
				return frame, true
			}
			continue
		}
		sawField = true
		switch {
		case strings.HasPrefix(line, ": "):
			frame.comment = strings.TrimPrefix(line, ": ")
		case strings.HasPrefix(line, "id: "):
			frame.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			frame.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			frame.data = strings.TrimPrefix(line, "data: ")
		default:
			t.Fatalf("unexpected SSE line %q", line)
		}
	}
}

// nextEvent skips comment frames (heartbeats) and returns the next named
// event frame.
func (s *sseStream) nextEvent(t *testing.T) (sseFrame, bool) {
	t.Helper()
	for {
		frame, ok := s.nextFrame(t)
		if !ok {
			return sseFrame{}, false
		}
		if frame.event != "" {
			return frame, true
		}
	}
}

// openWatch issues the watch GET and returns the open stream.
func openWatch(t *testing.T, h *testHarness, query string, headers map[string]string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	url := h.server.URL + "/api/workspaces/WS/driver/watch/epic"
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("new watch request: %v", err)
	}
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("do watch request: %v", err)
	}
	stream := &sseStream{resp: resp, reader: bufio.NewReader(resp.Body), cancel: cancel}
	t.Cleanup(stream.close)
	return stream
}

// appendWatchEvent appends one journal event for the harness epic/run and
// returns its store-assigned Seq.
func appendWatchEvent(t *testing.T, h *testHarness, taskRunID string, eventType domain.TaskRunEventType) int64 {
	t.Helper()
	event, err := h.store.TaskRunEvents().Append(context.Background(), store.TaskRunEventAppend{
		WorkspaceKey: "WS",
		EpicID:       "EPIC-1",
		DriverRunID:  h.runID,
		TaskID:       "TASK-1",
		TaskRunID:    taskRunID,
		Type:         eventType,
		Attempt:      1,
		OccurredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append journal event: %v", err)
	}
	return event.Seq
}

func decodeWatchData(t *testing.T, frame sseFrame) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(frame.data), &decoded); err != nil {
		t.Fatalf("decode %s frame data %q: %v", frame.event, frame.data, err)
	}
	return decoded
}

func TestWatchEpicAuth(t *testing.T) {
	tests := []struct {
		name     string
		apiToken string
		headers  func(h *testHarness) map[string]string
		query    string
		wantCode string
	}{
		{
			name:     "missing run id header",
			headers:  func(*testHarness) map[string]string { return nil },
			wantCode: "unauthenticated",
		},
		{
			name:     "missing bearer token",
			apiToken: "secret-token",
			headers:  func(h *testHarness) map[string]string { return h.ownerHeaders() },
			wantCode: "unauthenticated",
		},
		{
			name: "foreign owner credentials",
			headers: func(h *testHarness) map[string]string {
				headers := h.ownerHeaders()
				headers[HeaderDriverFencingToken] = "999999"
				return headers
			},
			wantCode: "not_owner",
		},
		{
			name:     "invalid cursor",
			headers:  func(h *testHarness) map[string]string { return h.ownerHeaders() },
			query:    "afterSeq=not-a-number",
			wantCode: "invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t, tt.apiToken)
			stream := openWatch(t, h, tt.query, tt.headers(h))
			if stream.resp.StatusCode == http.StatusOK {
				t.Fatalf("status = 200, want error for %s", tt.name)
			}
			var decoded map[string]any
			if err := json.NewDecoder(stream.resp.Body).Decode(&decoded); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if code := errorCode(t, decoded); code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestWatchEpicSnapshotFirstThenEvents(t *testing.T) {
	h := newTestHarness(t, "")
	h.module.watchPollInterval = 5 * time.Millisecond
	seq1 := appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventQueued)
	seq2 := appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventClaimed)

	stream := openWatch(t, h, "", h.ownerHeaders())
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", stream.resp.StatusCode)
	}
	if ct := stream.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Handshake must be the snapshot, id = the connect cursor (0).
	frame, ok := stream.nextEvent(t)
	if !ok || frame.event != "snapshot" {
		t.Fatalf("first frame = %+v, want event snapshot", frame)
	}
	if frame.id != "0" {
		t.Fatalf("snapshot id = %q, want 0", frame.id)
	}
	snapshot := decodeWatchData(t, frame)
	epic, _ := snapshot["epic"].(map[string]any)
	if epic["epicId"] != "EPIC-1" {
		t.Fatalf("snapshot epicId = %v, want EPIC-1", epic["epicId"])
	}
	if _, ok := snapshot["active"]; !ok {
		t.Fatalf("snapshot has no active task runs section: %v", snapshot)
	}

	// Pre-connect journal events stream next with id = Seq.
	wantSeqs := []int64{seq1, seq2}
	wantTypes := []string{string(domain.TaskRunEventQueued), string(domain.TaskRunEventClaimed)}
	for i, wantSeq := range wantSeqs {
		frame, ok := stream.nextEvent(t)
		if !ok || frame.event != "taskRun" {
			t.Fatalf("frame %d = %+v, want event taskRun", i, frame)
		}
		event := decodeWatchData(t, frame)
		if seq, _ := event["seq"].(float64); int64(seq) != wantSeq {
			t.Fatalf("event %d seq = %v, want %d", i, event["seq"], wantSeq)
		}
		if frame.id != strconvInt64(wantSeq) {
			t.Fatalf("event %d id line = %q, want %d", i, frame.id, wantSeq)
		}
		if event["type"] != wantTypes[i] {
			t.Fatalf("event %d type = %v, want %s", i, event["type"], wantTypes[i])
		}
		if _, leaked := event["leaseToken"]; leaked || strings.Contains(frame.data, "lease_token") {
			t.Fatalf("event %d exposed a task-run lease credential: %s", i, frame.data)
		}
	}

	// Events appended while connected are streamed too.
	seq3 := appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventCompleted)
	frame, ok = stream.nextEvent(t)
	if !ok || frame.event != "taskRun" {
		t.Fatalf("live frame = %+v, want event taskRun", frame)
	}
	if frame.id != strconvInt64(seq3) {
		t.Fatalf("live event id = %q, want %d", frame.id, seq3)
	}
	if event := decodeWatchData(t, frame); event["leaseToken"] != nil || strings.Contains(frame.data, "lease_token") {
		t.Fatalf("live event exposed a task-run lease credential: %s", frame.data)
	}
}

func TestWatchEpicResumeSkipsSeenSeqs(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		headers func(h *testHarness, cursor string) map[string]string
	}{
		{
			name: "Last-Event-ID header",
			headers: func(h *testHarness, cursor string) map[string]string {
				headers := h.ownerHeaders()
				headers["Last-Event-ID"] = cursor
				return headers
			},
		},
		{
			name:    "afterSeq query",
			query:   "afterSeq=%s",
			headers: func(h *testHarness, _ string) map[string]string { return h.ownerHeaders() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t, "")
			h.module.watchPollInterval = 5 * time.Millisecond
			appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventQueued)
			seq2 := appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventClaimed)
			seq3 := appendWatchEvent(t, h, "task-run-1", domain.TaskRunEventCompleted)

			cursor := strconvInt64(seq2)
			query := ""
			if tt.query != "" {
				query = strings.Replace(tt.query, "%s", cursor, 1)
			}
			stream := openWatch(t, h, query, tt.headers(h, cursor))
			if stream.resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", stream.resp.StatusCode)
			}

			frame, ok := stream.nextEvent(t)
			if !ok || frame.event != "snapshot" {
				t.Fatalf("first frame = %+v, want event snapshot", frame)
			}
			if frame.id != cursor {
				t.Fatalf("snapshot id = %q, want %q (resume cursor)", frame.id, cursor)
			}

			// Only the event after the cursor streams; seq1/seq2 are skipped.
			frame, ok = stream.nextEvent(t)
			if !ok || frame.event != "taskRun" {
				t.Fatalf("second frame = %+v, want event taskRun", frame)
			}
			if frame.id != strconvInt64(seq3) {
				t.Fatalf("resumed event id = %q, want %d", frame.id, seq3)
			}
		})
	}
}

func TestWatchEpicClosesWhenParentFinishes(t *testing.T) {
	h := newTestHarness(t, "")
	h.module.watchPollInterval = 5 * time.Millisecond
	h.module.watchReconcileInterval = 20 * time.Millisecond

	stream := openWatch(t, h, "", h.ownerHeaders())
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", stream.resp.StatusCode)
	}
	if frame, ok := stream.nextEvent(t); !ok || frame.event != "snapshot" {
		t.Fatalf("first frame = %+v, want event snapshot", frame)
	}

	if _, err := h.store.DriverRuns().Finish(context.Background(), "WS", h.runID, store.DriverRunFinish{
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		FencingToken: h.fence,
		Status:       domain.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("finish driver run: %v", err)
	}

	// The next reconciliation tick must notice and close the stream.
	for {
		frame, ok := stream.nextEvent(t)
		if !ok {
			t.Fatal("stream ended without an explicit closed event")
		}
		if frame.event == "snapshot" || frame.event == "taskRun" {
			continue
		}
		if frame.event != "closed" {
			t.Fatalf("frame = %+v, want event closed", frame)
		}
		closed := decodeWatchData(t, frame)
		if closed["code"] != "parent_not_running" {
			t.Fatalf("closed code = %v, want parent_not_running", closed["code"])
		}
		break
	}
	if frame, ok := stream.nextFrame(t); ok {
		t.Fatalf("frame after closed = %+v, want EOF", frame)
	}
}

func strconvInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
