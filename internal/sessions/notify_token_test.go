package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestNotifyWebUI_WithToken(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	NotifyWebUI(context.Background(), srv.URL, "task-1", "sess-1", StatusCompleted, "my-secret-token")

	mu.Lock()
	defer mu.Unlock()
	want := "Bearer my-secret-token"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
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

	NotifyWebUI(context.Background(), srv.URL, "task-1", "sess-1", StatusCompleted, "")

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "" {
		t.Errorf("Authorization header = %q, want empty (no token provided)", gotAuth)
	}
}
