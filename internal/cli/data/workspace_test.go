package data

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestResolveWorkspaceID_FromEnv(t *testing.T) {
	withDataClientState(t, func() {
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

func TestResolveWorkspaceID_RequiresExplicitWorkspace(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_WORKSPACE", "")
		_, err := resolveWorkspaceID(context.Background(), http.DefaultClient, "http://unused.invalid")
		if err == nil {
			t.Fatal("expected error without explicit workspace")
		}
		if !strings.Contains(err.Error(), "workspace is required") {
			t.Errorf("error = %q, want one containing 'workspace is required'", err.Error())
		}
	})
}
