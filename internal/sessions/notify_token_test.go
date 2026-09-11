package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestNotifyWebUI_WithToken(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string
	var gotPayload map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Errorf("decode notification: %v", err)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	NotifyWebUI(context.Background(), srv.URL, "workspace-1", "task-1", "sess-1", StatusCompleted, "my-secret-token")

	mu.Lock()
	defer mu.Unlock()
	want := "Bearer my-secret-token"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
	wantPayload := map[string]string{
		"workspace_id": "workspace-1",
		"task_id":      "task-1",
		"session_id":   "sess-1",
		"status":       string(StatusCompleted),
	}
	if len(gotPayload) != len(wantPayload) {
		t.Errorf("notification fields = %v, want exactly %v", gotPayload, wantPayload)
	}
	for key, want := range wantPayload {
		if gotPayload[key] != want {
			t.Errorf("%s = %q, want %q", key, gotPayload[key], want)
		}
	}
}

func TestNotifyWebUI_WithoutToken(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	NotifyWebUI(context.Background(), srv.URL, "workspace-1", "task-1", "sess-1", StatusCompleted, "")

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "" {
		t.Errorf("Authorization header = %q, want empty (no token provided)", gotAuth)
	}
}
