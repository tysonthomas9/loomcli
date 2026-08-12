package leadcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"nhooyr.io/websocket"
)

func TestCodexThreadWithTurnsUnmarshalAndPlainText(t *testing.T) {
	raw := []byte(`{
		"thread": {
			"id": "thread-1",
			"preview": "hello",
			"cwd": "/tmp/repo",
			"status": {"type": "idle"},
			"turns": [
				{
					"id": "turn-1",
					"startedAt": "2026-07-09T01:02:03Z",
					"completedAt": "2026-07-09T01:02:04Z",
					"durationMs": 1000,
					"status": "completed",
					"items": [
						{
							"type": "userMessage",
							"id": "item-user",
							"content": [
								{"type": "text", "text": "hello", "text_elements": []},
								{"type": "image", "text": "ignored"}
							]
						},
						{
							"type": "agentMessage",
							"id": "item-agent",
							"text": "hi there",
							"phase": "final_answer",
							"memoryCitation": null
						}
					],
					"itemsView": "expanded"
				}
			]
		}
	}`)
	var result struct {
		Thread CodexThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal thread/read result: %v", err)
	}
	thread := result.Thread
	if thread.ID != "thread-1" || len(thread.Turns) != 1 || len(thread.Turns[0].Items) != 2 {
		t.Fatalf("thread = %+v, want one turn with two items", thread)
	}
	user := thread.Turns[0].Items[0]
	if got := user.PlainText(); got != "hello" {
		t.Fatalf("user PlainText() = %q, want hello", got)
	}
	agent := thread.Turns[0].Items[1]
	if got := agent.PlainText(); got != "hi there" {
		t.Fatalf("agent PlainText() = %q, want hi there", got)
	}
	if agent.Phase != "final_answer" {
		t.Fatalf("agent phase = %q, want final_answer", agent.Phase)
	}
	if got := (CodexTurnItem{Type: "toolCall", Text: "ignored"}).PlainText(); got != "" {
		t.Fatalf("unknown PlainText() = %q, want empty", got)
	}
}

func TestIsRPCMethodNotFoundMatchesExactRPCCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "wrapped method not found",
			err: fmt.Errorf(
				"list turns: %w",
				&rpcError{Code: -32601, Message: "Method not found"},
			),
			want: true,
		},
		{
			name: "different rpc error",
			err:  &rpcError{Code: -32000, Message: "Server error"},
		},
		{
			name: "matching text without rpc code",
			err:  fmt.Errorf("method not found"),
		},
		{
			name: "nil",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRPCMethodNotFound(test.err); got != test.want {
				t.Fatalf("isRPCMethodNotFound() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRetainCodexTextClipsAtUTF8Boundaries(t *testing.T) {
	const text = "ab界cd"
	tests := []struct {
		name     string
		maxBytes int
		want     string
		complete bool
	}{
		{
			name:     "split multibyte rune",
			maxBytes: 4,
			want:     "ab",
		},
		{
			name:     "exact multibyte rune boundary",
			maxBytes: 5,
			want:     "ab界",
		},
		{
			name:     "complete input",
			maxBytes: len(text),
			want:     text,
			complete: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, complete := retainCodexText(text, test.maxBytes)
			if got != test.want || complete != test.complete {
				t.Fatalf(
					"retainCodexText(%q, %d) = (%q, %t), want (%q, %t)",
					text,
					test.maxBytes,
					got,
					complete,
					test.want,
					test.complete,
				)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("retainCodexText() returned invalid UTF-8 bytes: %x", []byte(got))
			}
			if len(got) > test.maxBytes {
				t.Fatalf("retained bytes = %d, exceeds limit %d", len(got), test.maxBytes)
			}
		})
	}
}

func TestReadThreadWithTurnsUsesOneSnapshotPerLivePoll(t *testing.T) {
	const turnCount = 1000
	turns := make([]CodexTurn, 0, turnCount)
	for i := range turnCount {
		turnID := fmt.Sprintf("turn-%d", i)
		turns = append(turns, CodexTurn{
			ID:     turnID,
			Status: "completed",
			Items: []CodexTurnItem{{
				Type: "agentMessage",
				ID:   "item-" + turnID,
				Text: "review update",
			}},
		})
	}

	var snapshotReads atomic.Int32
	var turnListReads atomic.Int32
	serverErr := make(chan error, 1)
	reportServerErr := func(err error) {
		select {
		case serverErr <- err:
		default:
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			reportServerErr(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		for {
			_, msg, readErr := conn.Read(r.Context())
			if readErr != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				reportServerErr(err)
				return
			}
			if req.Method == "initialized" {
				continue
			}

			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{}
			case "thread/read":
				if includeTurns, _ := req.Params["includeTurns"].(bool); !includeTurns {
					reportServerErr(fmt.Errorf("live snapshot omitted turns"))
					return
				}
				snapshotReads.Add(1)
				result = map[string]any{"thread": CodexThread{
					ID:     "thread-long-live",
					Status: CodexThreadStatus{Type: "active"},
					Turns:  turns,
				}}
			case "thread/turns/list":
				turnListReads.Add(1)
				reportServerErr(fmt.Errorf("live snapshot replayed paginated turns"))
				return
			default:
				reportServerErr(fmt.Errorf("unexpected method %q", req.Method))
				return
			}
			response, err := json.Marshal(map[string]any{"id": req.ID, "result": result})
			if err != nil {
				reportServerErr(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := DialCodexAppServer(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"))
	if err != nil {
		t.Fatalf("DialCodexAppServer() error = %v", err)
	}
	defer func() { _ = client.Close("test complete") }()
	for poll := range 2 {
		thread, err := client.ReadThreadWithTurns(ctx, "thread-long-live")
		if err != nil {
			t.Fatalf("ReadThreadWithTurns() poll %d error = %v", poll, err)
		}
		if len(thread.Turns) != turnCount {
			t.Fatalf("poll %d turns = %d, want %d", poll, len(thread.Turns), turnCount)
		}
	}
	if got := snapshotReads.Load(); got != 2 {
		t.Fatalf("thread/read calls = %d, want one per live poll", got)
	}
	if got := turnListReads.Load(); got != 0 {
		t.Fatalf("thread/turns/list calls = %d, want zero on live polls", got)
	}
	select {
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	default:
	}
}

func TestReadThreadTranscriptPaginatesBeyondLegacySingleMessageLimit(t *testing.T) {
	const (
		pageTextBytes          = 5 << 20
		legacySingleMessageMax = 8 << 20
	)
	serverErr := make(chan error, 1)
	reportServerErr := func(err error) {
		select {
		case serverErr <- err:
		default:
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			reportServerErr(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		for {
			_, msg, readErr := conn.Read(r.Context())
			if readErr != nil {
				return
			}
			var req struct {
				ID     json.RawMessage        `json:"id"`
				Method string                 `json:"method"`
				Params map[string]interface{} `json:"params"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				reportServerErr(err)
				return
			}
			if req.Method == "initialized" {
				continue
			}
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{}
			case "thread/read":
				if include, _ := req.Params["includeTurns"].(bool); include {
					reportServerErr(fmt.Errorf("thread/read requested full turns"))
					return
				}
				result = map[string]any{"thread": map[string]any{
					"id": "thread-long", "status": map[string]any{"type": "idle"}, "turns": []any{},
				}}
			case "thread/turns/list":
				if req.Params["sortDirection"] != "asc" || req.Params["itemsView"] != "summary" {
					reportServerErr(fmt.Errorf("unexpected pagination params: %#v", req.Params))
					return
				}
				if req.Params["limit"] != float64(1) {
					reportServerErr(fmt.Errorf("unexpected transcript page limit: %#v", req.Params["limit"]))
					return
				}
				cursor, _ := req.Params["cursor"].(string)
				text := strings.Repeat("x", pageTextBytes)
				nextCursor := any(nil)
				turnID := "turn-2"
				if cursor == "" {
					nextCursor = "page-2"
					turnID = "turn-1"
				}
				result = map[string]any{
					"data": []any{map[string]any{
						"id": turnID, "status": "completed",
						"items": []any{map[string]any{
							"type": "agentMessage", "id": "item-" + turnID, "text": text,
						}},
					}},
					"nextCursor": nextCursor,
				}
			default:
				reportServerErr(fmt.Errorf("unexpected method %q", req.Method))
				return
			}
			response, err := json.Marshal(map[string]any{
				"id": req.ID, "result": result,
			})
			if err != nil {
				reportServerErr(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	client, err := DialCodexAppServer(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"))
	if err != nil {
		t.Fatalf("DialCodexAppServer() error = %v", err)
	}
	defer func() { _ = client.Close("test complete") }()
	thread, err := client.ReadThreadTranscript(ctx, "thread-long")
	if err != nil {
		t.Fatalf("ReadThreadTranscript() error = %v", err)
	}
	if len(thread.Turns) != 2 {
		t.Fatalf("turns = %d, want two paginated turns", len(thread.Turns))
	}
	if got := len(thread.Turns[0].Items[0].Text) + len(thread.Turns[1].Items[0].Text); got <= legacySingleMessageMax {
		t.Fatalf("aggregate transcript bytes = %d, want > legacy single-message limit %d", got, legacySingleMessageMax)
	}
	select {
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	default:
	}
}

func TestReadThreadTranscriptFallsBackToBoundedSnapshotWhenTurnsListIsUnsupported(t *testing.T) {
	var metadataReads atomic.Int32
	var snapshotReads atomic.Int32
	var turnListReads atomic.Int32
	serverErr := make(chan error, 1)
	reportServerErr := func(err error) {
		select {
		case serverErr <- err:
		default:
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			reportServerErr(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		for {
			_, msg, readErr := conn.Read(r.Context())
			if readErr != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				reportServerErr(err)
				return
			}
			if req.Method == "initialized" {
				continue
			}

			var response any
			switch req.Method {
			case "initialize":
				response = map[string]any{"id": req.ID, "result": map[string]any{}}
			case "thread/read":
				includeTurns, _ := req.Params["includeTurns"].(bool)
				thread := CodexThread{
					ID:     "thread-legacy",
					Status: CodexThreadStatus{Type: "idle"},
				}
				if includeTurns {
					snapshotReads.Add(1)
					for i := range 3 {
						turnID := fmt.Sprintf("turn-%d", i+1)
						thread.Turns = append(thread.Turns, CodexTurn{
							ID:     turnID,
							Status: "completed",
							Items: []CodexTurnItem{{
								Type: "agentMessage",
								ID:   "item-" + turnID,
								Text: "legacy snapshot message",
							}},
						})
					}
				} else {
					metadataReads.Add(1)
				}
				response = map[string]any{"id": req.ID, "result": map[string]any{"thread": thread}}
			case "thread/turns/list":
				turnListReads.Add(1)
				response = map[string]any{
					"id": req.ID,
					"error": map[string]any{
						"code":    -32601,
						"message": "Method not found: thread/turns/list",
					},
				}
			default:
				reportServerErr(fmt.Errorf("unexpected method %q", req.Method))
				return
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				reportServerErr(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, encoded); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := DialCodexAppServer(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"))
	if err != nil {
		t.Fatalf("DialCodexAppServer() error = %v", err)
	}
	defer func() { _ = client.Close("test complete") }()
	thread, err := client.readThreadWithTurnsLimits(ctx, "thread-legacy", 1<<20, 2)
	if err != nil {
		t.Fatalf("readThreadWithTurnsLimits() error = %v", err)
	}
	if len(thread.Turns) != 2 {
		t.Fatalf("fallback turns = %d, want bounded prefix of 2", len(thread.Turns))
	}
	if !thread.TranscriptTruncated ||
		thread.TranscriptTruncationCause != transcriptSourceCauseCodexEvents ||
		thread.TranscriptSourceLimitEvents != 2 {
		t.Fatalf("fallback truncation provenance = %+v", thread)
	}
	if got := metadataReads.Load(); got != 1 {
		t.Fatalf("metadata thread/read calls = %d, want 1", got)
	}
	if got := turnListReads.Load(); got != 1 {
		t.Fatalf("thread/turns/list calls = %d, want one compatibility probe", got)
	}
	if got := snapshotReads.Load(); got != 1 {
		t.Fatalf("snapshot thread/read calls = %d, want one fallback", got)
	}
	select {
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	default:
	}
}

func TestReadThreadTranscriptRejectsRepeatedCursorAfterSecondPage(t *testing.T) {
	var pageCalls atomic.Int32
	serverErr := make(chan error, 1)
	reportServerErr := func(err error) {
		select {
		case serverErr <- err:
		default:
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			reportServerErr(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		for {
			_, msg, readErr := conn.Read(r.Context())
			if readErr != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				reportServerErr(err)
				return
			}
			if req.Method == "initialized" {
				continue
			}

			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{}
			case "thread/read":
				result = map[string]any{"thread": map[string]any{
					"id": "thread-repeated-cursor", "status": map[string]any{"type": "idle"},
				}}
			case "thread/turns/list":
				call := pageCalls.Add(1)
				result = map[string]any{
					"data": []any{map[string]any{
						"id": fmt.Sprintf("turn-%d", call), "status": "completed",
						"items": []any{map[string]any{
							"type": "reasoning", "id": fmt.Sprintf("item-%d", call), "text": "ignored",
						}},
					}},
					"nextCursor": "stuck-cursor",
				}
			default:
				reportServerErr(fmt.Errorf("unexpected method %q", req.Method))
				return
			}
			response, err := json.Marshal(map[string]any{"id": req.ID, "result": result})
			if err != nil {
				reportServerErr(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := DialCodexAppServer(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"))
	if err != nil {
		t.Fatalf("DialCodexAppServer() error = %v", err)
	}
	defer func() { _ = client.Close("test complete") }()

	_, err = client.readThreadWithTurnsLimits(ctx, "thread-repeated-cursor", 1<<20, 10)
	if err == nil || !strings.Contains(err.Error(), `repeated cursor "stuck-cursor"`) {
		t.Fatalf("readThreadWithTurnsLimits() error = %v, want repeated-cursor rejection", err)
	}
	if got := pageCalls.Load(); got != 2 {
		t.Fatalf("turn page calls = %d, want exactly 2 before repeated-cursor rejection", got)
	}
	select {
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	default:
	}
}

func TestReadThreadTranscriptStopsAtAggregateSourceTextLimit(t *testing.T) {
	const (
		pageTextBytes  = 1024
		textLimit      = 2500
		availablePages = 10
	)
	var pageCalls atomic.Int32
	serverErr := make(chan error, 1)
	reportServerErr := func(err error) {
		select {
		case serverErr <- err:
		default:
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			reportServerErr(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		for {
			_, msg, readErr := conn.Read(r.Context())
			if readErr != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				reportServerErr(err)
				return
			}
			if req.Method == "initialized" {
				continue
			}

			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{}
			case "thread/read":
				threadID, _ := req.Params["threadId"].(string)
				result = map[string]any{"thread": map[string]any{
					"id": threadID, "status": map[string]any{"type": "idle"},
				}}
			case "thread/turns/list":
				page := int(pageCalls.Add(1))
				nextCursor := any(nil)
				if page < availablePages {
					nextCursor = fmt.Sprintf("page-%d", page+1)
				}
				itemType := "agentMessage"
				if req.Params["threadId"] == "thread-empty-pages" {
					itemType = "reasoning"
				}
				result = map[string]any{
					"data": []any{map[string]any{
						"id": fmt.Sprintf("turn-%d", page), "status": "completed",
						"items": []any{map[string]any{
							"type": itemType,
							"id":   fmt.Sprintf("item-%d", page),
							"text": strings.Repeat("x", pageTextBytes),
						}},
					}},
					"nextCursor": nextCursor,
				}
			default:
				reportServerErr(fmt.Errorf("unexpected method %q", req.Method))
				return
			}
			response, err := json.Marshal(map[string]any{
				"id": req.ID, "result": result,
			})
			if err != nil {
				reportServerErr(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := DialCodexAppServer(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"))
	if err != nil {
		t.Fatalf("DialCodexAppServer() error = %v", err)
	}
	defer func() { _ = client.Close("test complete") }()

	thread, err := client.readThreadWithTurnsTextLimit(ctx, "thread-many-pages", textLimit)
	if err != nil {
		t.Fatalf("readThreadWithTurnsTextLimit() error = %v", err)
	}
	if !thread.TranscriptTruncated {
		t.Fatal("thread was not marked truncated at the source-text limit")
	}
	if thread.TranscriptTruncationCause != transcriptSourceCauseCodexText ||
		thread.TranscriptSourceLimitBytes != textLimit ||
		thread.TranscriptSourceLimitEvents != 0 ||
		thread.TranscriptSourceLimitPages != 0 {
		t.Fatalf("source-text truncation provenance = %+v", thread)
	}
	if got := pageCalls.Load(); got != 3 {
		t.Fatalf("turn page calls = %d, want 3 before the remaining 7 pages", got)
	}
	totalTextBytes := 0
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			totalTextBytes += len(item.PlainText())
		}
	}
	if totalTextBytes != textLimit {
		t.Fatalf("retained text bytes = %d, want bounded prefix of %d", totalTextBytes, textLimit)
	}
	if got := len(thread.Turns[len(thread.Turns)-1].Items[0].Text); got != textLimit-(2*pageTextBytes) {
		t.Fatalf("final clipped item bytes = %d", got)
	}
	encoded, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshal bounded thread: %v", err)
	}
	if strings.Contains(string(encoded), "TranscriptTruncated") ||
		strings.Contains(string(encoded), "transcriptTruncated") ||
		strings.Contains(string(encoded), "codex_history_") {
		t.Fatalf("local truncation state leaked into Codex JSON: %s", encoded)
	}

	pageCalls.Store(0)
	eventBounded, err := client.readThreadWithTurnsLimits(
		ctx,
		"thread-many-pages",
		1<<20,
		2,
	)
	if err != nil {
		t.Fatalf("readThreadWithTurnsLimits() error = %v", err)
	}
	if !eventBounded.TranscriptTruncated || len(eventBounded.Turns) != 2 {
		t.Fatalf("event-bounded thread = %+v, want two retained turns plus truncation state", eventBounded)
	}
	if eventBounded.TranscriptTruncationCause != transcriptSourceCauseCodexEvents ||
		eventBounded.TranscriptSourceLimitEvents != 2 ||
		eventBounded.TranscriptSourceLimitBytes != 0 ||
		eventBounded.TranscriptSourceLimitPages != 0 {
		t.Fatalf("event-bound truncation provenance = %+v", eventBounded)
	}
	if got := pageCalls.Load(); got != 2 {
		t.Fatalf("event-bounded turn page calls = %d, want 2", got)
	}

	pageCalls.Store(0)
	emptyBounded, err := client.readThreadWithTurnsLimits(
		ctx,
		"thread-empty-pages",
		1<<20,
		2,
	)
	if err != nil {
		t.Fatalf("readThreadWithTurnsLimits() empty pages error = %v", err)
	}
	if !emptyBounded.TranscriptTruncated || len(emptyBounded.Turns) != 0 {
		t.Fatalf("empty-page bounded thread = %+v, want no retained turns plus truncation state", emptyBounded)
	}
	if emptyBounded.TranscriptTruncationCause != transcriptSourceCauseCodexPages ||
		emptyBounded.TranscriptSourceLimitPages != 2 ||
		emptyBounded.TranscriptSourceLimitBytes != 0 ||
		emptyBounded.TranscriptSourceLimitEvents != 0 {
		t.Fatalf("empty-page truncation provenance = %+v", emptyBounded)
	}
	if got := pageCalls.Load(); got != 2 {
		t.Fatalf("empty-page turn page calls = %d, want scan bound of 2", got)
	}

	select {
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	default:
	}
}
