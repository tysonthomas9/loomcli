package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Operator-attribution coverage for Update: a non-empty UpdateParams.Actor
// overrides X-Actor for the request; empty preserves the process identity.
// Lives in its own file to keep fleet_test.go under the LOC ratchet.

func TestUpdate_ActorOverrideAndProcessFallback(t *testing.T) {
	tests := []struct {
		name      string
		actor     string
		wantActor string
	}{
		{name: "operator override", actor: "operator@local", wantActor: "operator@local"},
		{name: "non-operator write", wantActor: "process@local"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotActor string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotActor = r.Header.Get("X-Actor")
				respondOK(w, json.RawMessage(`{}`))
			}))
			defer ts.Close()

			fb, err := New(Config{
				BaseURL:     ts.URL,
				WorkspaceID: "test-ws",
				Actor:       "process@local",
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			title := "Some Title"
			if err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
				Actor: tt.actor,
				Title: &title,
			}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if gotActor != tt.wantActor {
				t.Errorf("X-Actor = %q, want %q", gotActor, tt.wantActor)
			}
		})
	}
}
