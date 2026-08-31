package issues

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
	fleetbackend "github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Every write route must stamp the operator identity as the advisory actor on
// the context it hands the service. A handler that resolves the actor but
// forwards r.Context() instead of the stamped one keeps 403ing against an ACL
// that does not know the operator — which is the whole bug. Driving the
// requests through a registered mux means route wiring is covered too.
func TestOperatorWriteRoutes_StampAdvisoryActorOnServiceContext(t *testing.T) {
	routes := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{name: "patch", method: http.MethodPatch, path: "/api/workspaces/test-ws/issues/issue-1", body: `{"title":"Updated"}`, wantStatus: http.StatusOK},
		{name: "close", method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/close", body: `{}`, wantStatus: http.StatusOK},
		{name: "reopen", method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/reopen", body: `{}`, wantStatus: http.StatusOK},
		{name: "comment", method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/comments", body: `{"text":"hi"}`, wantStatus: http.StatusCreated},
		{name: "add dependency", method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/dependencies", body: `{"depends_on_id":"issue-2"}`, wantStatus: http.StatusOK},
		{name: "remove dependency", method: http.MethodDelete, path: "/api/workspaces/test-ws/issues/issue-1/dependencies/issue-2", wantStatus: http.StatusOK},
	}

	identities := []struct {
		name     string
		identity *middleware.UserIdentity
		want     string
	}{
		{name: "open mode fallback", want: defaultOperatorActor},
		{
			name:     "verified session",
			identity: &middleware.UserIdentity{UserID: "user-123", Email: "operator@example.com"},
			want:     "operator@example.com",
		},
	}

	for _, id := range identities {
		t.Run(id.name, func(t *testing.T) {
			t.Setenv(envOperatorActor, "")
			for _, route := range routes {
				t.Run(route.name, func(t *testing.T) {
					var gotStamped, gotActor string
					record := func(ctx context.Context, actor string) {
						gotStamped = advisoryactor.From(ctx)
						gotActor = actor
					}
					mux := http.NewServeMux()
					NewIssueModule(recordingIssueService(record), nil).Register(mux)

					var body io.Reader
					if route.body != "" {
						body = strings.NewReader(route.body)
					}
					req := httptest.NewRequest(route.method, route.path, body)
					if id.identity != nil {
						req = req.WithContext(middleware.WithUserIdentity(req.Context(), *id.identity))
					}
					resp := httptest.NewRecorder()

					mux.ServeHTTP(resp, req)

					if resp.Code != route.wantStatus {
						t.Fatalf("status = %d, want %d; body: %s", resp.Code, route.wantStatus, resp.Body.String())
					}
					if gotActor != id.want {
						t.Errorf("params.Actor = %q, want %q", gotActor, id.want)
					}
					if gotStamped != id.want {
						t.Errorf("advisoryactor.From(ctx) = %q, want %q (handler forwarded an unstamped context)", gotStamped, id.want)
					}
				})
			}
		})
	}
}

// recordingIssueService reports the context and actor of whichever write it
// receives, so one stub covers all six routes.
func recordingIssueService(record func(ctx context.Context, actor string)) *mockIssueService {
	return &mockIssueService{
		patchIssueFunc: func(ctx context.Context, p service.PatchIssueParams) error {
			record(ctx, p.Actor)
			return nil
		},
		getIssueFunc: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
		closeIssueFunc: func(ctx context.Context, p service.CloseIssueParams) (json.RawMessage, error) {
			record(ctx, p.Actor)
			return json.RawMessage(`{}`), nil
		},
		reopenIssueFunc: func(ctx context.Context, p service.ReopenIssueParams) error {
			record(ctx, p.Actor)
			return nil
		},
		addCommentFunc: func(ctx context.Context, p service.AddCommentParams) (*types.Comment, error) {
			record(ctx, p.Actor)
			return &types.Comment{ID: 1, IssueID: p.IssueID, Text: p.Text}, nil
		},
		addDependencyFunc: func(ctx context.Context, p service.AddDependencyParams) error {
			record(ctx, p.Actor)
			return nil
		},
		removeDependencyFunc: func(ctx context.Context, p service.RemoveDependencyParams) error {
			record(ctx, p.Actor)
			return nil
		},
	}
}

// End-to-end over the real fleet backend: an issue store whose ACL knows only
// the harness actor must still serve the board. Unblock (DELETE dependency) is
// the ticket's first acceptance criterion; Approve (PATCH) and close are the
// same shape.
func TestOperatorWriteRoutes_SurviveRoleLessOperatorActor(t *testing.T) {
	routes := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{name: "unblock", method: http.MethodDelete, path: "/api/workspaces/test-ws/issues/issue-1/dependencies/issue-2", wantStatus: http.StatusOK},
		{name: "approve", method: http.MethodPatch, path: "/api/workspaces/test-ws/issues/issue-1", body: `{"title":"Updated"}`, wantStatus: http.StatusOK},
		{name: "close", method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/close", body: `{}`, wantStatus: http.StatusOK},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			t.Setenv(envOperatorActor, "")
			var actors []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actor := r.Header.Get("X-Actor")
				if r.Method != http.MethodGet {
					actors = append(actors, actor)
					// The ACL knows only the process actor.
					if actor != processActorFixture {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusForbidden)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"error": map[string]any{"code": "forbidden", "message": "workspace access denied"},
						})
						return
					}
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/issues/issue-1/deps"),
					strings.HasSuffix(r.URL.Path, "/issues/issue-1/dependencies"):
					writeFleetJSON(w, map[string]any{"dependencies": []any{}})
				case strings.HasSuffix(r.URL.Path, "/issues/issue-1/comments"):
					writeFleetJSON(w, map[string]any{"comments": []any{}})
				case strings.HasSuffix(r.URL.Path, "/issues/issue-1/close"):
					writeFleetIssue(w, string(types.StatusClosed), nil, "")
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/issue-1"):
					writeFleetIssue(w, "open", nil, "")
				default:
					writeFleetJSON(w, map[string]any{"success": true})
				}
			}))
			defer server.Close()

			mux := http.NewServeMux()
			NewIssueModule(newFleetIssueService(t, server.URL), nil).Register(mux)

			var body io.Reader
			if route.body != "" {
				body = strings.NewReader(route.body)
			}
			resp := httptest.NewRecorder()
			mux.ServeHTTP(resp, httptest.NewRequest(route.method, route.path, body))

			if resp.Code != route.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", resp.Code, route.wantStatus, resp.Body.String())
			}
			if len(actors) < 2 {
				t.Fatalf("mutation actors = %v, want a denied operator attempt then a process-actor retry", actors)
			}
			if actors[0] != defaultOperatorActor {
				t.Errorf("first attempt X-Actor = %q, want %q", actors[0], defaultOperatorActor)
			}
			if actors[1] != processActorFixture {
				t.Errorf("retry X-Actor = %q, want %q", actors[1], processActorFixture)
			}
		})
	}
}

const processActorFixture = "local-mode-harness@fixture.local"

func newFleetIssueService(t *testing.T, baseURL string) service.IssueService {
	t.Helper()
	be, err := fleetbackend.New(fleetbackend.Config{
		BaseURL:     baseURL,
		WorkspaceID: "test-ws",
		Actor:       processActorFixture,
	})
	if err != nil {
		t.Fatalf("fleet backend: %v", err)
	}
	return service.NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend {
		return be
	})
}

// The stamp must travel with the handler, not with route middleware: this
// package's own tests (and any future embedded mount) call handlers directly.
func TestOperatorActorContext_StampsOnDirectHandlerCall(t *testing.T) {
	t.Setenv(envOperatorActor, "direct@example.com")
	var gotStamped, gotActor string
	h := HandleRemoveDependency(&mockIssueService{
		removeDependencyFunc: func(ctx context.Context, p service.RemoveDependencyParams) error {
			gotStamped = advisoryactor.From(ctx)
			gotActor = p.Actor
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/issues/issue-1/dependencies/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.Code, resp.Body.String())
	}
	if gotActor != "direct@example.com" || gotStamped != "direct@example.com" {
		t.Errorf("actor = %q, stamped = %q, want both %q", gotActor, gotStamped, "direct@example.com")
	}
}
