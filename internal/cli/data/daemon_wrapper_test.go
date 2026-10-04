package data

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type wrappedBackend struct{ backend.IssueBackend }

// P1.26: the HTTP issue backend is handed to the daemon-agent wrapper, so a
// daemon-managed agent's `loom data close` of its own task reaches the daemon.
func TestGetIssueBackendWrapsTheHTTPBackendForDaemonAgents(t *testing.T) {
	srv := fakeAuthConfigServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		serverURL = srv.URL
		t.Setenv("LOOM_WORKSPACE", "WS")
		var wrapped int
		SetDaemonAgentWrapper(func(ib backend.IssueBackend) backend.IssueBackend {
			wrapped++
			return wrappedBackend{ib}
		})
		t.Cleanup(func() { SetDaemonAgentWrapper(nil) })
		ib, err := getIssueBackend(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ib.(wrappedBackend); !ok || wrapped != 1 {
			t.Fatalf("backend %T wrapped %d times, want the daemon-agent wrapper applied once", ib, wrapped)
		}
	})
}
