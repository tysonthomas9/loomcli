package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
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

func TestClose_ActorOverrideAndProcessFallback(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			var mutationActors []string
			fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/test-1/close"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, types.Issue{ID: "test-1", Status: types.StatusClosed})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			defer ts.Close()

			if _, err := fb.Close(context.Background(), "test-1", backend.CloseParams{Actor: tt.actor}); err != nil {
				t.Fatalf("Close: %v", err)
			}
			assertOperatorMutationActors(t, mutationActors, tt.wantActor)
		})
	}
}

func TestReopen_ActorOverrideAndProcessFallback(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			var mutationActors []string
			fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/test-1/reopen"),
					r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"),
					r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, json.RawMessage(`{}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			defer ts.Close()

			if err := fb.Reopen(context.Background(), "test-1", backend.ReopenParams{Actor: tt.actor, Reason: "needs work"}); err != nil {
				t.Fatalf("Reopen: %v", err)
			}
			assertOperatorMutationActors(t, mutationActors, tt.wantActor)
		})
	}
}

func TestAddDependency_ActorOverrideAndProcessFallback(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			var mutationActors []string
			fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a"):
					respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "a"}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
					respondOK(w, map[string]any{"dependencies": []map[string]string{{"issue_id": "a", "depends_on_id": "b", "type": "blocks"}}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/b"):
					respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "b"}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a/comments"):
					respondOK(w, map[string]any{"comments": []any{}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			defer ts.Close()

			if err := fb.AddDependency(context.Background(), backend.DepAddParams{Actor: tt.actor, FromID: "a", ToID: "b", DepType: "blocks"}); err != nil {
				t.Fatalf("AddDependency: %v", err)
			}
			assertOperatorMutationActors(t, mutationActors, tt.wantActor)
		})
	}
}

func TestRemoveDependency_ActorOverrideAndProcessFallback(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			var mutationActors []string
			fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/issues/a/deps/b"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/a"):
					mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a"):
					respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "a", Status: string(types.StatusBlocked)}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
					respondOK(w, map[string]any{"dependencies": []any{}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/a/comments"):
					respondOK(w, map[string]any{"comments": []any{}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			defer ts.Close()

			if err := fb.RemoveDependency(context.Background(), backend.DepRemoveParams{Actor: tt.actor, FromID: "a", ToID: "b"}); err != nil {
				t.Fatalf("RemoveDependency: %v", err)
			}
			assertOperatorMutationActors(t, mutationActors, tt.wantActor)
		})
	}
}

func TestAddComment_ActorOverrideAndProcessFallback(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			var mutationActors []string
			fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/issues/test-1/comments") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
				respondOK(w, fleetCommentWire{
					ID:        json.RawMessage(`1`),
					IssueID:   "test-1",
					Author:    "web-ui",
					Body:      "FEEDBACK: revise",
					CreatedAt: time.Now().UTC(),
				})
			})
			defer ts.Close()

			if _, err := fb.AddComment(context.Background(), backend.CommentAddParams{Actor: tt.actor, IssueID: "test-1", Text: "FEEDBACK: revise"}); err != nil {
				t.Fatalf("AddComment: %v", err)
			}
			assertOperatorMutationActors(t, mutationActors, tt.wantActor)
		})
	}
}

func operatorActorCases() []struct {
	name      string
	actor     string
	wantActor string
} {
	return []struct {
		name      string
		actor     string
		wantActor string
	}{
		{name: "operator override", actor: "operator@local", wantActor: "operator@local"},
		{name: "non-operator write", wantActor: "process@local"},
	}
}

func newOperatorActorTestBackend(t *testing.T, handler http.HandlerFunc) (*FleetBackend, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	fb, err := New(Config{
		BaseURL:     ts.URL,
		WorkspaceID: "test-ws",
		Actor:       "process@local",
	})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return fb, ts
}

func assertOperatorMutationActors(t *testing.T, actors []string, want string) {
	t.Helper()
	if len(actors) == 0 {
		t.Fatal("expected at least one mutation")
	}
	for i, got := range actors {
		if got != want {
			t.Errorf("mutation %d X-Actor = %q, want %q", i, got, want)
		}
	}
}
