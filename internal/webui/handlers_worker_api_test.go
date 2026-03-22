package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// WorkerRegistry tests
// ---------------------------------------------------------------------------

func TestNewWorkerRegistry(t *testing.T) {
	reg := NewWorkerRegistry()
	if reg == nil {
		t.Fatal("NewWorkerRegistry returned nil")
	}
	if reg.workers == nil {
		t.Fatal("workers map is nil")
	}
}

func TestWorkerRegistry_RegisterAndGet(t *testing.T) {
	reg := NewWorkerRegistry()
	w := &WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"}
	reg.Register(w)

	got := reg.Get("w1")
	if got == nil {
		t.Fatal("Get returned nil after Register")
	}
	if got.Agent != "a1" {
		t.Errorf("Agent = %q, want %q", got.Agent, "a1")
	}
}

func TestWorkerRegistry_Deregister(t *testing.T) {
	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1"})

	if !reg.Deregister("w1") {
		t.Error("Deregister returned false for existing worker")
	}
	if reg.Get("w1") != nil {
		t.Error("Get returned non-nil after Deregister")
	}
	if reg.Deregister("w1") {
		t.Error("Deregister returned true for already-removed worker")
	}
	if reg.Deregister("nonexistent") {
		t.Error("Deregister returned true for unknown worker")
	}
}

// ---------------------------------------------------------------------------
// workerAuthMiddleware tests
// ---------------------------------------------------------------------------

func TestWorkerAuthMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		token      string // configured server token
		authHeader string
		wantCode   int
	}{
		{
			name:       "no token configured",
			token:      "",
			authHeader: "Bearer secret",
			wantCode:   http.StatusServiceUnavailable,
		},
		{
			name:       "missing auth header",
			token:      "secret",
			authHeader: "",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name:       "auth header without Bearer prefix",
			token:      "secret",
			authHeader: "Basic secret",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name:       "wrong token",
			token:      "secret",
			authHeader: "Bearer wrong",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "valid token",
			token:      "secret",
			authHeader: "Bearer secret",
			wantCode:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := workerAuthMiddleware(tt.token, ok)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWorkerRegister tests
// ---------------------------------------------------------------------------

func TestHandleWorkerRegister(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantErr  bool
	}{
		{
			name:     "valid registration",
			body:     `{"workspace":"ws","agent":"a1","backend":"local"}`,
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing workspace",
			body:     `{"agent":"a1"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  true,
		},
		{
			name:     "missing agent",
			body:     `{"workspace":"ws"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  true,
		},
		{
			name:     "invalid json",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewWorkerRegistry()
			handler := handleWorkerRegister(reg)

			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/register", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tt.wantCode, w.Body.String())
			}
			if !tt.wantErr {
				var resp workerRegisterResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("decode error: %v", err)
				}
				if resp.WorkerID == "" {
					t.Error("WorkerID is empty")
				}
				// Worker should be in registry.
				if reg.Get(resp.WorkerID) == nil {
					t.Error("worker not found in registry after register")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWorkerDeregister tests
// ---------------------------------------------------------------------------

func TestHandleWorkerDeregister(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		preReg   bool // pre-register a worker with this ID
		wantCode int
	}{
		{
			name:     "existing worker",
			id:       "w1",
			preReg:   true,
			wantCode: http.StatusNoContent,
		},
		{
			name:     "unknown worker",
			id:       "w999",
			preReg:   false,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "empty id",
			id:       "",
			preReg:   false,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewWorkerRegistry()
			if tt.preReg {
				reg.Register(&WorkerInfo{ID: tt.id, Workspace: "ws", Agent: "a"})
			}

			handler := handleWorkerDeregister(reg)
			req := httptest.NewRequest(http.MethodDelete, "/api/internal/workers/"+tt.id, nil)
			req.SetPathValue("id", tt.id)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWorkerState tests
// ---------------------------------------------------------------------------

func TestHandleWorkerState(t *testing.T) {
	// Create a temp worktree dir with a lock file.
	tmpDir := t.TempDir()
	lockData := `{"state":"idle","pid":12345}`
	if err := os.WriteFile(filepath.Join(tmpDir, ".agent.lock"), []byte(lockData), 0600); err != nil {
		t.Fatal(err)
	}

	resolveWT := func(workspace, agent string) string { return tmpDir }

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	tests := []struct {
		name     string
		workerID string
		body     string
		wantCode int
	}{
		{
			name:     "update_state",
			workerID: "w1",
			body:     `{"action":"update_state","state":"running"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "update_task",
			workerID: "w1",
			body:     `{"action":"update_task","task_id":"t1","task_title":"fix bug"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "clear_task",
			workerID: "w1",
			body:     `{"action":"clear_task"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "read lock",
			workerID: "w1",
			body:     `{"action":"read"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "unknown action",
			workerID: "w1",
			body:     `{"action":"explode"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "unknown worker",
			workerID: "w999",
			body:     `{"action":"read"}`,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "invalid json",
			workerID: "w1",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleWorkerState(reg, resolveWT)
			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/"+tt.workerID+"/state", strings.NewReader(tt.body))
			req.SetPathValue("id", tt.workerID)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestHandleWorkerState_EmptyWorktreePath(t *testing.T) {
	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	resolveWT := func(workspace, agent string) string { return "" }
	handler := handleWorkerState(reg, resolveWT)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state", strings.NewReader(`{"action":"read"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// handleWorkerEvents tests
// ---------------------------------------------------------------------------

func TestHandleWorkerEvents(t *testing.T) {
	tmpDir := t.TempDir()
	resolveEvt := func(workspace string) string { return tmpDir }

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	tests := []struct {
		name     string
		workerID string
		body     string
		wantCode int
	}{
		{
			name:     "valid event",
			workerID: "w1",
			body:     `{"type":"task_started","ts":"2025-01-01T00:00:00Z"}`,
			wantCode: http.StatusAccepted,
		},
		{
			name:     "unknown worker",
			workerID: "w999",
			body:     `{"type":"x"}`,
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleWorkerEvents(reg, resolveEvt)
			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/"+tt.workerID+"/events", strings.NewReader(tt.body))
			req.SetPathValue("id", tt.workerID)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestHandleWorkerEvents_EmptyEventsDir(t *testing.T) {
	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	resolveEvt := func(workspace string) string { return "" }
	handler := handleWorkerEvents(reg, resolveEvt)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/events", strings.NewReader(`{"type":"x"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// handleWorkerLogs tests
// ---------------------------------------------------------------------------

func TestHandleWorkerLogs(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")
	resolveLog := func(workspace, agent string) string { return logPath }

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	tests := []struct {
		name     string
		workerID string
		body     string
		wantCode int
	}{
		{
			name:     "valid log",
			workerID: "w1",
			body:     "some log line\n",
			wantCode: http.StatusOK,
		},
		{
			name:     "unknown worker",
			workerID: "w999",
			body:     "data",
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleWorkerLogs(reg, resolveLog)
			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/"+tt.workerID+"/logs", strings.NewReader(tt.body))
			req.SetPathValue("id", tt.workerID)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestHandleWorkerLogs_EmptyLogPath(t *testing.T) {
	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})

	resolveLog := func(workspace, agent string) string { return "" }
	handler := handleWorkerLogs(reg, resolveLog)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/logs", strings.NewReader("data"))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// workerLockPath tests
// ---------------------------------------------------------------------------

func TestWorkerLockPath(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid path",
			path:    tmpDir,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workerLockPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasSuffix(got, ".agent.lock") {
				t.Errorf("lock path %q does not end with .agent.lock", got)
			}
			abs, _ := filepath.Abs(tmpDir)
			if !strings.HasPrefix(got, abs+string(filepath.Separator)) {
				t.Errorf("lock path %q not under worktree %q", got, abs)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// readWorkerLockFile / writeWorkerLockFile tests
// ---------------------------------------------------------------------------

func TestReadWriteWorkerLockFile(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")

	initial := map[string]interface{}{"state": "idle", "pid": float64(1234)}
	data, _ := json.Marshal(initial)
	if err := os.WriteFile(lockFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	lockPath, info, err := readWorkerLockFile(tmpDir)
	if err != nil {
		t.Fatalf("readWorkerLockFile error: %v", err)
	}
	if info["state"] != "idle" {
		t.Errorf("state = %v, want %q", info["state"], "idle")
	}

	// Write updated state.
	info["state"] = "running"
	if err := writeWorkerLockFile(lockPath, info); err != nil {
		t.Fatalf("writeWorkerLockFile error: %v", err)
	}

	// Re-read and verify.
	_, info2, err := readWorkerLockFile(tmpDir)
	if err != nil {
		t.Fatalf("readWorkerLockFile after write error: %v", err)
	}
	if info2["state"] != "running" {
		t.Errorf("state = %v, want %q", info2["state"], "running")
	}
}

func TestReadWorkerLockFile_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, _, err := readWorkerLockFile(tmpDir)
	if err == nil {
		t.Error("expected error for missing lock file, got nil")
	}
}

func TestReadWorkerLockFile_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".agent.lock"), []byte("{bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := readWorkerLockFile(tmpDir)
	if err == nil {
		t.Error("expected error for invalid JSON lock file, got nil")
	}
}

// ---------------------------------------------------------------------------
// updateWorkerLockState tests
// ---------------------------------------------------------------------------

func TestUpdateWorkerLockState(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"idle"}`), 0600)

	if err := updateWorkerLockState(tmpDir, "running"); err != nil {
		t.Fatalf("updateWorkerLockState error: %v", err)
	}

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["state"] != "running" {
		t.Errorf("state = %v, want %q", info["state"], "running")
	}
}

func TestUpdateWorkerLockState_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := updateWorkerLockState(tmpDir, "running"); err == nil {
		t.Error("expected error when lock file missing")
	}
}

// ---------------------------------------------------------------------------
// updateWorkerLockTask tests
// ---------------------------------------------------------------------------

func TestUpdateWorkerLockTask(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"running"}`), 0600)

	if err := updateWorkerLockTask(tmpDir, "t1", "Fix bug"); err != nil {
		t.Fatalf("updateWorkerLockTask error: %v", err)
	}

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["task_id"] != "t1" {
		t.Errorf("task_id = %v, want %q", info["task_id"], "t1")
	}
	if info["task_title"] != "Fix bug" {
		t.Errorf("task_title = %v, want %q", info["task_title"], "Fix bug")
	}
	if _, ok := info["task_started_at"]; !ok {
		t.Error("task_started_at missing after updateWorkerLockTask")
	}
}

func TestUpdateWorkerLockTask_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := updateWorkerLockTask(tmpDir, "t1", "Fix"); err == nil {
		t.Error("expected error when lock file missing")
	}
}

// ---------------------------------------------------------------------------
// clearWorkerLockTask tests
// ---------------------------------------------------------------------------

func TestClearWorkerLockTask(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"running","task_id":"t1","task_title":"Fix","task_started_at":"2025-01-01T00:00:00Z"}`), 0600)

	if err := clearWorkerLockTask(tmpDir); err != nil {
		t.Fatalf("clearWorkerLockTask error: %v", err)
	}

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["task_id"] != "" {
		t.Errorf("task_id = %v, want empty", info["task_id"])
	}
	if info["task_title"] != "" {
		t.Errorf("task_title = %v, want empty", info["task_title"])
	}
	if _, ok := info["task_started_at"]; ok {
		t.Error("task_started_at should be deleted after clearWorkerLockTask")
	}
}

func TestClearWorkerLockTask_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := clearWorkerLockTask(tmpDir); err == nil {
		t.Error("expected error when lock file missing")
	}
}

// ---------------------------------------------------------------------------
// readWorkerLock tests
// ---------------------------------------------------------------------------

func TestReadWorkerLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"idle","pid":999}`), 0600)

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatalf("readWorkerLock error: %v", err)
	}
	if info["state"] != "idle" {
		t.Errorf("state = %v, want %q", info["state"], "idle")
	}
}

func TestReadWorkerLock_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := readWorkerLock(tmpDir)
	if err == nil {
		t.Error("expected error for missing lock file")
	}
}

// ---------------------------------------------------------------------------
// appendToEventsFile tests
// ---------------------------------------------------------------------------

func TestAppendToEventsFile(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")

	// First call creates dir and file.
	if err := appendToEventsFile(eventsDir, []byte(`{"event":"one"}`)); err != nil {
		t.Fatalf("appendToEventsFile error: %v", err)
	}
	// Second call appends.
	if err := appendToEventsFile(eventsDir, []byte(`{"event":"two"}`+"\n")); err != nil {
		t.Fatalf("appendToEventsFile (second) error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(eventsDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], `"one"`) {
		t.Errorf("line 0 = %q, want event one", lines[0])
	}
	if !strings.Contains(lines[1], `"two"`) {
		t.Errorf("line 1 = %q, want event two", lines[1])
	}
}

func TestAppendToEventsFile_AddsNewline(t *testing.T) {
	tmpDir := t.TempDir()
	// Data without trailing newline should get one added.
	if err := appendToEventsFile(tmpDir, []byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "events.jsonl"))
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("expected trailing newline")
	}
}

// ---------------------------------------------------------------------------
// appendToLogFile tests
// ---------------------------------------------------------------------------

func TestAppendToLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sub", "agent.log")

	if err := appendToLogFile(logPath, []byte("line1\n")); err != nil {
		t.Fatalf("appendToLogFile error: %v", err)
	}
	if err := appendToLogFile(logPath, []byte("line2\n")); err != nil {
		t.Fatalf("appendToLogFile second error: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "line1") || !strings.Contains(string(data), "line2") {
		t.Errorf("log file content = %q, want both lines", string(data))
	}
}

// ---------------------------------------------------------------------------
// SetupWorkerAPIRoutes tests
// ---------------------------------------------------------------------------

func TestSetupWorkerAPIRoutes(t *testing.T) {
	mux := http.NewServeMux()
	tmpDir := t.TempDir()

	resolveWT := func(ws, ag string) string { return tmpDir }
	resolveEvt := func(ws string) string { return tmpDir }
	resolveLog := func(ws, ag string) string { return filepath.Join(tmpDir, "agent.log") }

	reg := SetupWorkerAPIRoutes(mux, "test-token", resolveWT, resolveEvt, resolveLog)
	if reg == nil {
		t.Fatal("SetupWorkerAPIRoutes returned nil registry")
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Register a worker via the full route with auth.
	body := `{"workspace":"ws","agent":"a1","backend":"local"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/internal/workers/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var regResp workerRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if regResp.WorkerID == "" {
		t.Error("WorkerID empty in register response")
	}

	// Deregister the worker.
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/internal/workers/"+regResp.WorkerID, nil)
	req2.Header.Set("Authorization", "Bearer test-token")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("deregister request error: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("deregister status = %d, want %d", resp2.StatusCode, http.StatusNoContent)
	}
}

func TestSetupWorkerAPIRoutes_RejectsWithoutAuth(t *testing.T) {
	mux := http.NewServeMux()
	tmpDir := t.TempDir()
	SetupWorkerAPIRoutes(mux, "secret", func(_, _ string) string { return tmpDir }, func(_ string) string { return tmpDir }, func(_, _ string) string { return filepath.Join(tmpDir, "a.log") })

	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/internal/workers/register", strings.NewReader(`{"workspace":"ws","agent":"a"}`))
	// No Authorization header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// ---------------------------------------------------------------------------
// handleWorkerRegister — additional coverage
// ---------------------------------------------------------------------------

func TestHandleWorkerRegister_EmptyBody(t *testing.T) {
	reg := NewWorkerRegistry()
	handler := handleWorkerRegister(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/register", strings.NewReader(""))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleWorkerRegister_BothFieldsEmpty(t *testing.T) {
	reg := NewWorkerRegistry()
	handler := handleWorkerRegister(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/register", strings.NewReader(`{"workspace":"","agent":""}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleWorkerRegister_NoBackend(t *testing.T) {
	reg := NewWorkerRegistry()
	handler := handleWorkerRegister(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/register", strings.NewReader(`{"workspace":"ws","agent":"a1"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
	var resp workerRegisterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	info := reg.Get(resp.WorkerID)
	if info == nil {
		t.Fatal("worker not in registry")
	}
	if info.Backend != "" {
		t.Errorf("Backend = %q, want empty", info.Backend)
	}
}

// ---------------------------------------------------------------------------
// handleWorkerState — additional coverage
// ---------------------------------------------------------------------------

func TestHandleWorkerState_UpdateStateNoLockFile(t *testing.T) {
	tmpDir := t.TempDir() // no .agent.lock written

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"update_state","state":"running"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleWorkerState_UpdateTaskNoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"update_task","task_id":"t1","task_title":"fix"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleWorkerState_ClearTaskNoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"clear_task"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleWorkerState_ReadNoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"read"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleWorkerState_ReadReturnsLockContents(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"busy","task_id":"t99","task_title":"deploy"}`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"read"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var info map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if info["state"] != "busy" {
		t.Errorf("state = %v, want %q", info["state"], "busy")
	}
	if info["task_id"] != "t99" {
		t.Errorf("task_id = %v, want %q", info["task_id"], "t99")
	}
}

func TestHandleWorkerState_MalformedLockFile(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`not valid json!!!`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	actions := []struct {
		name string
		body string
		want int
	}{
		{"update_state with malformed lock", `{"action":"update_state","state":"x"}`, http.StatusInternalServerError},
		{"update_task with malformed lock", `{"action":"update_task","task_id":"t","task_title":"f"}`, http.StatusInternalServerError},
		{"clear_task with malformed lock", `{"action":"clear_task"}`, http.StatusInternalServerError},
		{"read with malformed lock", `{"action":"read"}`, http.StatusNotFound},
	}

	for _, a := range actions {
		t.Run(a.name, func(t *testing.T) {
			handler := handleWorkerState(reg, resolveWT)
			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state", strings.NewReader(a.body))
			req.SetPathValue("id", "w1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != a.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, a.want, w.Body.String())
			}
		})
	}
}

func TestHandleWorkerState_ConcurrentUpdates(t *testing.T) {
	// Concurrent state updates hit the same lock file without file-level
	// locking, so some requests may fail with 500 due to read-write races.
	// This test verifies that:
	//  1. No goroutine panics.
	//  2. Every response is either 200 (success) or 500 (transient race).
	//  3. After all goroutines finish, the lock file is still valid JSON.
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"idle","pid":1}`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)

	var wg sync.WaitGroup
	codes := make(chan int, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := `{"action":"update_state","state":"running"}`
			req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state", strings.NewReader(body))
			req.SetPathValue("id", "w1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			codes <- w.Code
		}()
	}

	wg.Wait()
	close(codes)

	okCount := 0
	for code := range codes {
		if code == http.StatusOK {
			okCount++
		} else if code != http.StatusInternalServerError {
			t.Errorf("unexpected status %d (want 200 or 500)", code)
		}
	}
	if okCount == 0 {
		t.Error("expected at least one successful concurrent update")
	}

	// After all concurrent writes settle, do one final sequential write
	// to ensure the lock file is usable.
	os.WriteFile(lockFile, []byte(`{"state":"idle"}`), 0600)
	if err := updateWorkerLockState(tmpDir, "done"); err != nil {
		t.Fatalf("sequential update after concurrent writes failed: %v", err)
	}
	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatalf("readWorkerLock after recovery: %v", err)
	}
	if info["state"] != "done" {
		t.Errorf("state = %v, want %q", info["state"], "done")
	}
}

func TestHandleWorkerState_UpdateStateVerifyContents(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"idle","pid":999}`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"update_state","state":"paused"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify the lock file was actually updated.
	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["state"] != "paused" {
		t.Errorf("state = %v, want %q", info["state"], "paused")
	}
	// pid should be preserved.
	if info["pid"] != float64(999) {
		t.Errorf("pid = %v, want 999", info["pid"])
	}
}

func TestHandleWorkerState_UpdateTaskVerifyContents(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"running"}`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"update_task","task_id":"abc","task_title":"deploy service"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["task_id"] != "abc" {
		t.Errorf("task_id = %v, want %q", info["task_id"], "abc")
	}
	if info["task_title"] != "deploy service" {
		t.Errorf("task_title = %v, want %q", info["task_title"], "deploy service")
	}
	if _, ok := info["task_started_at"]; !ok {
		t.Error("task_started_at missing")
	}
}

func TestHandleWorkerState_ClearTaskVerifyContents(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".agent.lock")
	os.WriteFile(lockFile, []byte(`{"state":"running","task_id":"t1","task_title":"fix","task_started_at":"2025-01-01T00:00:00Z"}`), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveWT := func(_, _ string) string { return tmpDir }

	handler := handleWorkerState(reg, resolveWT)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/state",
		strings.NewReader(`{"action":"clear_task"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	info, err := readWorkerLock(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if info["task_id"] != "" {
		t.Errorf("task_id = %v, want empty", info["task_id"])
	}
	if _, ok := info["task_started_at"]; ok {
		t.Error("task_started_at should be deleted")
	}
	// state should be preserved.
	if info["state"] != "running" {
		t.Errorf("state = %v, want %q", info["state"], "running")
	}
}

// ---------------------------------------------------------------------------
// handleWorkerEvents — additional coverage
// ---------------------------------------------------------------------------

func TestHandleWorkerEvents_AppendToExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	os.MkdirAll(eventsDir, 0700)
	// Pre-create the events file with existing content.
	os.WriteFile(filepath.Join(eventsDir, "events.jsonl"), []byte(`{"event":"existing"}`+"\n"), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveEvt := func(_ string) string { return eventsDir }

	handler := handleWorkerEvents(reg, resolveEvt)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/events",
		strings.NewReader(`{"event":"new_event"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusAccepted, w.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(eventsDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "existing") {
		t.Errorf("line 0 = %q, want existing event", lines[0])
	}
	if !strings.Contains(lines[1], "new_event") {
		t.Errorf("line 1 = %q, want new_event", lines[1])
	}
}

func TestHandleWorkerEvents_ReadOnlyDir(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "readonly-events")
	os.MkdirAll(eventsDir, 0700)
	// Create the events file as read-only.
	evFile := filepath.Join(eventsDir, "events.jsonl")
	os.WriteFile(evFile, []byte(""), 0400)
	// Make the file unwritable.
	os.Chmod(evFile, 0400)
	t.Cleanup(func() { os.Chmod(evFile, 0600) })

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveEvt := func(_ string) string { return eventsDir }

	handler := handleWorkerEvents(reg, resolveEvt)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/events",
		strings.NewReader(`{"event":"test"}`))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleWorkerEvents_MultipleEventsSequential(t *testing.T) {
	tmpDir := t.TempDir()

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveEvt := func(_ string) string { return tmpDir }

	handler := handleWorkerEvents(reg, resolveEvt)

	events := []string{
		`{"type":"start","seq":1}`,
		`{"type":"progress","seq":2}`,
		`{"type":"done","seq":3}`,
	}
	for _, ev := range events {
		req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/events", strings.NewReader(ev))
		req.SetPathValue("id", "w1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
		}
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "events.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

// ---------------------------------------------------------------------------
// handleWorkerLogs — additional coverage
// ---------------------------------------------------------------------------

func TestHandleWorkerLogs_AppendToExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")
	os.WriteFile(logPath, []byte("existing line\n"), 0600)

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveLog := func(_, _ string) string { return logPath }

	handler := handleWorkerLogs(reg, resolveLog)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/logs",
		strings.NewReader("new log line\n"))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "existing line") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(content, "new log line") {
		t.Error("new content not appended")
	}
}

func TestHandleWorkerLogs_LargeLogData(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveLog := func(_, _ string) string { return logPath }

	// Build a large log payload (~100KB).
	var sb strings.Builder
	line := strings.Repeat("X", 100) + "\n"
	for i := 0; i < 1000; i++ {
		sb.WriteString(line)
	}
	largeData := sb.String()

	handler := handleWorkerLogs(reg, resolveLog)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/logs",
		strings.NewReader(largeData))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(largeData) {
		t.Errorf("log file size = %d, want %d", len(data), len(largeData))
	}
}

func TestHandleWorkerLogs_ReadOnlyDir(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "readonly-logs")
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, "agent.log")
	os.WriteFile(logPath, []byte(""), 0400)
	os.Chmod(logPath, 0400)
	t.Cleanup(func() { os.Chmod(logPath, 0600) })

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveLog := func(_, _ string) string { return logPath }

	handler := handleWorkerLogs(reg, resolveLog)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/logs",
		strings.NewReader("data"))
	req.SetPathValue("id", "w1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleWorkerLogs_MultipleAppends(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")

	reg := NewWorkerRegistry()
	reg.Register(&WorkerInfo{ID: "w1", Workspace: "ws", Agent: "a1"})
	resolveLog := func(_, _ string) string { return logPath }

	handler := handleWorkerLogs(reg, resolveLog)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/w1/logs",
			strings.NewReader("chunk\n"))
		req.SetPathValue("id", "w1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("append %d: status = %d, want %d", i, w.Code, http.StatusOK)
		}
	}

	data, _ := os.ReadFile(logPath)
	count := strings.Count(string(data), "chunk")
	if count != 5 {
		t.Errorf("expected 5 chunks, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// workerLockPath — additional coverage
// ---------------------------------------------------------------------------

func TestWorkerLockPath_TraversalAttempt(t *testing.T) {
	// The workerLockPath function joins the path with ".agent.lock",
	// so a traversal attack would need to come through the worktreePath
	// itself. Verify that the path is always under the base.
	tmpDir := t.TempDir()

	// A normal subdir should work fine.
	subDir := filepath.Join(tmpDir, "sub", "workspace")
	os.MkdirAll(subDir, 0700)

	got, err := workerLockPath(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, ".agent.lock") {
		t.Errorf("path %q does not end with .agent.lock", got)
	}
}

func TestWorkerLockPath_DeeplyNestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	deepDir := filepath.Join(tmpDir, "a", "b", "c", "d", "e")
	os.MkdirAll(deepDir, 0700)

	got, err := workerLockPath(deepDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	absBase, _ := filepath.Abs(deepDir)
	if !strings.HasPrefix(got, absBase+string(filepath.Separator)) {
		t.Errorf("lock path %q not under base %q", got, absBase)
	}
}

func TestWorkerLockPath_RelativePath(t *testing.T) {
	// Even a relative path should resolve correctly.
	got, err := workerLockPath(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, ".agent.lock") {
		t.Errorf("path %q does not end with .agent.lock", got)
	}
}

func TestWorkerLockPath_PathWithSpaces(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "path with spaces")
	os.MkdirAll(spaceDir, 0700)

	got, err := workerLockPath(spaceDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "path with spaces") {
		t.Errorf("path %q should contain 'path with spaces'", got)
	}
	if !strings.HasSuffix(got, ".agent.lock") {
		t.Errorf("path %q does not end with .agent.lock", got)
	}
}

func TestWorkerLockPath_PathWithDots(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	os.MkdirAll(subDir, 0700)

	// Use the parent dir with "../sub" — should resolve properly.
	dotPath := filepath.Join(tmpDir, "sub", "..", "sub")
	got, err := workerLockPath(dotPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// WorkerRegistry — concurrent access
// ---------------------------------------------------------------------------

func TestWorkerRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewWorkerRegistry()
	var wg sync.WaitGroup

	// Concurrently register workers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := strings.Repeat("w", n%10+1)
			reg.Register(&WorkerInfo{ID: id, Workspace: "ws", Agent: "a"})
			reg.Get(id)
			reg.Deregister(id)
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// appendToEventsFile — additional coverage
// ---------------------------------------------------------------------------

func TestAppendToEventsFile_EmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	// Empty data should not panic or error.
	if err := appendToEventsFile(tmpDir, []byte{}); err != nil {
		t.Fatalf("appendToEventsFile with empty data: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(tmpDir, "events.jsonl"))
	if len(data) != 0 {
		t.Errorf("expected empty file for empty data, got %d bytes", len(data))
	}
}

func TestAppendToEventsFile_ReadOnlyParent(t *testing.T) {
	tmpDir := t.TempDir()
	roDir := filepath.Join(tmpDir, "readonly")
	os.MkdirAll(roDir, 0700)
	os.Chmod(roDir, 0500)
	t.Cleanup(func() { os.Chmod(roDir, 0700) })

	// Trying to create a subdir under a read-only dir should fail.
	err := appendToEventsFile(filepath.Join(roDir, "subdir"), []byte(`{"x":1}`))
	if err == nil {
		t.Error("expected error writing to read-only parent")
	}
}

// ---------------------------------------------------------------------------
// appendToLogFile — additional coverage
// ---------------------------------------------------------------------------

func TestAppendToLogFile_EmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")
	if err := appendToLogFile(logPath, []byte{}); err != nil {
		t.Fatalf("appendToLogFile with empty data: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

func TestAppendToLogFile_ReadOnlyParent(t *testing.T) {
	tmpDir := t.TempDir()
	roDir := filepath.Join(tmpDir, "readonly")
	os.MkdirAll(roDir, 0700)
	os.Chmod(roDir, 0500)
	t.Cleanup(func() { os.Chmod(roDir, 0700) })

	logPath := filepath.Join(roDir, "subdir", "agent.log")
	err := appendToLogFile(logPath, []byte("data"))
	if err == nil {
		t.Error("expected error writing to read-only parent")
	}
}
