package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveWorkspaceID_FlagWins(t *testing.T) {
	withDataClientState(t, func() {
		workspaceID = "ws-flag"
		t.Setenv("LOOM_WORKSPACE", "ws-env")
		got, err := resolveWorkspaceID(context.Background(), http.DefaultClient, "http://unused.invalid")
		if err != nil {
			t.Fatalf("resolveWorkspaceID: %v", err)
		}
		if got != "ws-flag" {
			t.Errorf("got %q, want %q", got, "ws-flag")
		}
	})
}

func TestResolveWorkspaceID_EnvWins(t *testing.T) {
	withDataClientState(t, func() {
		workspaceID = ""
		t.Setenv("LOOM_WORKSPACE", "ws-env")
		got, err := resolveWorkspaceID(context.Background(), http.DefaultClient, "http://unused.invalid")
		if err != nil {
			t.Fatalf("resolveWorkspaceID: %v", err)
		}
		if got != "ws-env" {
			t.Errorf("got %q, want %q", got, "ws-env")
		}
	})
}

func TestResolveWorkspaceID_Discovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/active" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "default"})
	}))
	defer srv.Close()

	withDataClientState(t, func() {
		workspaceID = ""
		t.Setenv("LOOM_WORKSPACE", "")
		got, err := resolveWorkspaceID(context.Background(), srv.Client(), srv.URL)
		if err != nil {
			t.Fatalf("resolveWorkspaceID: %v", err)
		}
		if got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})
}

func TestResolveWorkspaceID_Discovery404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	withDataClientState(t, func() {
		workspaceID = ""
		t.Setenv("LOOM_WORKSPACE", "")
		_, err := resolveWorkspaceID(context.Background(), srv.Client(), srv.URL)
		if err == nil {
			t.Fatal("expected error for 404 no active workspace")
		}
		if !strings.Contains(err.Error(), "no active workspace") {
			t.Errorf("error = %q, want one containing 'no active workspace'", err.Error())
		}
	})
}
