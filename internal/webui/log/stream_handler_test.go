package log

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	testStreamWorkspace = "test-ws"
	testStreamAgent     = "ember"
)

type agentStreamHarness struct {
	server  *httptest.Server
	logFile string
}

func newAgentStreamHarness(t *testing.T, tokenStore *realtime.TokenStore) agentStreamHarness {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)

	logFile, err := GetAgentLogPath(testStreamWorkspace, testStreamAgent)
	if err != nil {
		t.Fatalf("GetAgentLogPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		t.Fatalf("failed to create agent log directory: %v", err)
	}

	mux := http.NewServeMux()
	NewModule(nil, tokenStore).Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return agentStreamHarness{server: server, logFile: logFile}
}

func (h agentStreamHarness) streamURL(query string) string {
	streamURL := fmt.Sprintf(
		"%s/api/workspaces/%s/agents/%s/logs/stream",
		h.server.URL,
		testStreamWorkspace,
		testStreamAgent,
	)
	if query != "" {
		streamURL += "?" + query
	}
	return streamURL
}

func connectAgentStream(t *testing.T, streamURL string) (*streamSSEClient, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	client := connectTestSSE(t, ctx, streamURL)
	t.Cleanup(func() {
		client.close()
		cancel()
	})
	return client, cancel
}

func assertRetryPrelude(t *testing.T, scanner *bufio.Scanner) {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("missing retry directive: %v", scanner.Err())
	}
	if got := scanner.Text(); got != "retry: 5000" {
		t.Fatalf("first stream line = %q, want %q", got, "retry: 5000")
	}
	if !scanner.Scan() {
		t.Fatalf("missing retry frame terminator: %v", scanner.Err())
	}
	if got := scanner.Text(); got != "" {
		t.Fatalf("retry frame terminator = %q, want blank line", got)
	}
}

func readLogChunk(t *testing.T, client *streamSSEClient) LogChunkPayload {
	t.Helper()
	event, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read log-chunk event: %v", err)
	}
	if event.Event != "log-chunk" {
		t.Fatalf("event = %q, want log-chunk", event.Event)
	}
	var payload LogChunkPayload
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		t.Fatalf("failed to decode log-chunk payload: %v", err)
	}
	return payload
}

func TestAgentLogStream_RetryPreludeBeforeReplay(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	if err := os.WriteFile(h.logFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	client, _ := connectAgentStream(t, h.streamURL(""))
	assertRetryPrelude(t, client.scanner)
	if got := decodeChunkB64(t, readLogChunk(t, client)); got != "hello" {
		t.Fatalf("replay = %q, want %q", got, "hello")
	}
}

func TestAgentLogStream_ReplayFromOffset(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	if err := os.WriteFile(h.logFile, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	client, _ := connectAgentStream(t, h.streamURL("offset=2&tail_bytes=1"))
	payload := readLogChunk(t, client)
	if got := decodeChunkB64(t, payload); got != "cdef" {
		t.Fatalf("replay = %q, want %q", got, "cdef")
	}
	if payload.ByteOffset != 6 {
		t.Fatalf("byte_offset = %d, want 6", payload.ByteOffset)
	}
}

func TestAgentLogStream_TailBytesWindow(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	if err := os.WriteFile(h.logFile, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	client, _ := connectAgentStream(t, h.streamURL("tail_bytes=3"))
	if got := decodeChunkB64(t, readLogChunk(t, client)); got != "def" {
		t.Fatalf("replay = %q, want %q", got, "def")
	}
}

func TestAgentLogStream_LiveAppend(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	if err := os.WriteFile(h.logFile, []byte("seed"), 0o644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	client, _ := connectAgentStream(t, h.streamURL("tail_bytes=0"))
	assertRetryPrelude(t, client.scanner)
	file, err := os.OpenFile(h.logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open log: %v", err)
	}
	if _, err := file.WriteString("-live"); err != nil {
		_ = file.Close()
		t.Fatalf("failed to append log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close log: %v", err)
	}

	payload := readLogChunk(t, client)
	if got := decodeChunkB64(t, payload); got != "-live" {
		t.Fatalf("live chunk = %q, want %q", got, "-live")
	}
}

func TestAgentLogStream_TruncatedEvent(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	if err := os.WriteFile(h.logFile, []byte("long-content"), 0o644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	client, _ := connectAgentStream(t, h.streamURL(""))
	_ = readLogChunk(t, client)
	if err := os.WriteFile(h.logFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to truncate log: %v", err)
	}

	event, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read truncated event: %v", err)
	}
	if event.Event != "truncated" {
		t.Fatalf("event = %q, want truncated", event.Event)
	}
}

func TestAgentLogStream_RequiresFreshToken(t *testing.T) {
	tokenStore, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	t.Cleanup(tokenStore.Stop)
	h := newAgentStreamHarness(t, tokenStore)

	resp, err := http.Get(h.streamURL("")) //nolint:gosec // httptest URL
	if err != nil {
		t.Fatalf("request without token failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}

	token, err := tokenStore.Generate("user-1", testStreamWorkspace)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	streamURL := h.streamURL("token=" + url.QueryEscape(token))
	client, cancel := connectAgentStream(t, streamURL)
	assertRetryPrelude(t, client.scanner)
	client.close()
	cancel()

	resp, err = http.Get(streamURL) //nolint:gosec // httptest URL
	if err != nil {
		t.Fatalf("request with burned token failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with burned token = %d, want 401", resp.StatusCode)
	}
}

func TestAgentLogStream_RejectsEncodedSlashInAgentName(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	requestURL := fmt.Sprintf(
		"%s/api/workspaces/%s/agents/bad%%2Fname/logs/stream",
		h.server.URL,
		testStreamWorkspace,
	)
	resp, err := http.Get(requestURL) //nolint:gosec // httptest URL
	if err != nil {
		t.Fatalf("traversal-shaped request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
}

func TestAgentLogStream_ConnectThenCreate(t *testing.T) {
	h := newAgentStreamHarness(t, nil)
	client, _ := connectAgentStream(t, h.streamURL("tail_bytes=128"))
	assertRetryPrelude(t, client.scanner)

	if err := os.WriteFile(h.logFile, []byte("created-live"), 0o644); err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	if got := decodeChunkB64(t, readLogChunk(t, client)); got != "created-live" {
		t.Fatalf("live chunk = %q, want %q", got, "created-live")
	}
}

func TestAgentLogStream_ClearsServerWriteDeadline(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	logFile, err := GetAgentLogPath(testStreamWorkspace, testStreamAgent)
	if err != nil {
		t.Fatalf("GetAgentLogPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		t.Fatalf("failed to create agent log directory: %v", err)
	}

	mux := http.NewServeMux()
	NewModule(nil, nil).Register(mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/workspaces/"+testStreamWorkspace+"/agents/"+testStreamAgent+"/logs/stream",
		nil,
	).WithContext(ctx)
	recorder := &writeDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	mux.ServeHTTP(recorder, req)

	if len(recorder.deadlines) != 1 || !recorder.deadlines[0].IsZero() {
		t.Fatalf("write deadlines = %v, want one zero deadline", recorder.deadlines)
	}
}

type writeDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *writeDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
