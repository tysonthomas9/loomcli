package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestHandleTerminalHistoryCacheBoundary(t *testing.T) {
	store := webuterminal.NewRecordingStore(t.TempDir(), nil)
	key := webuterminal.SessionKey{Workspace: "ws-1", Name: "term-1"}
	recorder, err := store.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	recorder.Append([]byte("one\r\ntwo\r\nthree"))
	firstScreen := recorder.FirstScreenLine()
	if firstScreen == 0 {
		t.Fatal("fixture did not commit a historical row")
	}
	meta, _, _, err := store.Meta(context.Background(), key)
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	generationQuery := "?generation=" + meta.Generation

	immutable := terminalHistoryRequest(t, store, "/"+generationQuery+"&from=0&count=1", key)
	if immutable.Code != http.StatusOK {
		t.Fatalf("immutable status = %d body=%s", immutable.Code, immutable.Body.String())
	}
	if immutable.Header().Get("ETag") == "" {
		t.Fatal("immutable response missing ETag")
	}
	if got := immutable.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("immutable Cache-Control = %q", got)
	}

	mutable := terminalHistoryRequest(t, store, "/"+generationQuery+"&from="+strconv.FormatUint(firstScreen, 10)+"&count=1", key)
	if got := mutable.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("mutable Cache-Control = %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(mutable.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode mutable response: %v", err)
	}
	lines, _ := payload["lines"].([]any)
	if got, _ := payload["generation"].(string); got != meta.Generation {
		t.Fatalf("history range generation = %q, want %q", got, meta.Generation)
	}
	for _, mutableField := range []string{"totalLines", "firstScreenLine", "upToDate", "closed", "cols"} {
		if _, exposed := payload[mutableField]; exposed {
			t.Fatalf("history range exposed mutable field %q: %s", mutableField, mutable.Body.String())
		}
	}
	if len(lines) > 0 {
		if line, ok := lines[0].(map[string]any); ok {
			if _, exposed := line["off"]; exposed {
				t.Fatal("history response exposed an internal raw offset")
			}
		}
	}
}

func TestHandleTerminalHistoryMetaIncludesUnhandledSequenceDiagnostics(t *testing.T) {
	store := webuterminal.NewRecordingStore(t.TempDir(), nil)
	key := webuterminal.SessionKey{Workspace: "ws-1", Name: "term-meta"}
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	// OSC 0 and DECCOLM (?3) count as unhandled; ?25 (cursor visibility) is
	// a benign render hint that must NOT count.
	recorder.Append([]byte("\x1b]0;title\x07\x1b[?25l\x1b[?3h"))
	_ = recorder.FirstScreenLine()
	t.Cleanup(func() { _ = recorder.Close() })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("session", key.Name)
	req = req.WithContext(middleware.WithWorkspace(context.Background(), key.Workspace))
	response := httptest.NewRecorder()
	HandleTerminalHistoryMeta(store).ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		UnhandledSequences webuterminal.RecordingUnhandledSequences `json:"unhandledSequences"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if payload.UnhandledSequences.Count != 2 || payload.UnhandledSequences.Prefixes["OSC 0"] != 1 || payload.UnhandledSequences.Prefixes["CSI ?3h"] != 1 {
		t.Fatalf("unhandledSequences = %#v", payload.UnhandledSequences)
	}
	if _, counted := payload.UnhandledSequences.Prefixes["CSI ?25l"]; counted {
		t.Fatalf("benign cursor-visibility hint was counted: %#v", payload.UnhandledSequences)
	}
}

func TestHandleTerminalHistoryGenerationSeparatesImmutableCacheIdentity(t *testing.T) {
	store := webuterminal.NewRecordingStore(t.TempDir(), nil)
	key := webuterminal.SessionKey{Workspace: "ws-1", Name: "reused-term"}
	// The started hook fires asynchronously on the recorder worker once a
	// durable line commits, so guard the slice and poll for each entry.
	var startsMu sync.Mutex
	var starts []webuterminal.RecordingMeta
	store.SetLifecycleHooks(
		func(_ webuterminal.SessionKey, _ string, meta webuterminal.RecordingMeta) {
			startsMu.Lock()
			starts = append(starts, meta)
			startsMu.Unlock()
		},
		nil,
	)
	waitForStarts := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			startsMu.Lock()
			n := len(starts)
			startsMu.Unlock()
			if n == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("recording starts = %d, want %d", n, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Each generation scrolls its marker line off the 2-row screen so a
	// durable line commits; the deferred started hook only fires (and the
	// generation is only kept) once the recording proves non-trivial.
	first, err := store.StartRecording(key, 80, 2)
	if err != nil {
		t.Fatalf("start generation A: %v", err)
	}
	first.Append([]byte("generation-a-only\r\npad-a\r\npad-a2\r\n"))
	if err := first.Close(); err != nil {
		t.Fatalf("close generation A: %v", err)
	}
	waitForStarts(1)
	second, err := store.StartRecording(key, 80, 2)
	if err != nil {
		t.Fatalf("start generation B: %v", err)
	}
	second.Append([]byte("generation-b-only\r\npad-b\r\npad-b2\r\n"))
	if err := second.Close(); err != nil {
		t.Fatalf("close generation B: %v", err)
	}
	waitForStarts(2)

	generationA := regressionGenerationID(t, starts[0], "legacy-generation-a")
	generationB := regressionGenerationID(t, starts[1], "legacy-generation-b")
	targetA := "/?generation=" + generationA + "&from=0&count=10"
	targetB := "/?generation=" + generationB + "&from=0&count=10"
	if targetA == targetB {
		t.Fatalf("generation range URLs match: %q", targetA)
	}

	responseA := terminalHistoryRequest(t, store, targetA, key)
	if responseA.Code != http.StatusOK {
		t.Fatalf("generation A status = %d body=%s", responseA.Code, responseA.Body.String())
	}
	etagA := responseA.Header().Get("ETag")
	if etagA == "" {
		t.Fatal("generation A response missing ETag")
	}
	if got := responseA.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("generation A Cache-Control = %q", got)
	}

	requestB := terminalHistoryHTTPRequest(t, targetB, key)
	requestB.Header.Set("If-None-Match", etagA)
	responseB := httptest.NewRecorder()
	HandleTerminalHistory(store).ServeHTTP(responseB, requestB)
	if responseB.Code != http.StatusOK {
		t.Fatalf("generation B with generation A ETag status = %d body=%s, want 200", responseB.Code, responseB.Body.String())
	}
	if etagB := responseB.Header().Get("ETag"); etagB == "" || etagB == etagA {
		t.Fatalf("generation cache identities match: A=%q B=%q", etagA, etagB)
	}
	if !strings.Contains(responseA.Body.String(), "generation-a-only") || strings.Contains(responseA.Body.String(), "generation-b-only") {
		t.Fatalf("generation A response mixed recordings: %s", responseA.Body.String())
	}
	if !strings.Contains(responseB.Body.String(), "generation-b-only") || strings.Contains(responseB.Body.String(), "generation-a-only") {
		t.Fatalf("generation B response mixed recordings: %s", responseB.Body.String())
	}
}

// regressionGenerationID keeps this regression behaviorally runnable before
// RecordingMeta grows the generation field. Once present, its JSON value is
// the exact opaque generation accepted by the handler.
func regressionGenerationID(t *testing.T, meta webuterminal.RecordingMeta, fallback string) string {
	t.Helper()
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("encode recording metadata: %v", err)
	}
	var identity struct {
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		t.Fatalf("decode recording generation: %v", err)
	}
	if identity.Generation == "" {
		return fallback
	}
	return identity.Generation
}

func terminalHistoryRequest(t *testing.T, store *webuterminal.RecordingStore, target string, key webuterminal.SessionKey) *httptest.ResponseRecorder {
	t.Helper()
	req := terminalHistoryHTTPRequest(t, target, key)
	recorder := httptest.NewRecorder()
	HandleTerminalHistory(store).ServeHTTP(recorder, req)
	return recorder
}

func terminalHistoryHTTPRequest(t *testing.T, target string, key webuterminal.SessionKey) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("session", key.Name)
	return req.WithContext(middleware.WithWorkspace(context.Background(), key.Workspace))
}
