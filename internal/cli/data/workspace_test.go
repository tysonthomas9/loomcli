package data

import (
	"context"
	"net/http"
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

func TestResolveWorkspaceID_RequiresExplicitWorkspace(t *testing.T) {
	withDataClientState(t, func() {
		workspaceID = ""
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
