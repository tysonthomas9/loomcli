package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	fleetbackend "github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandlePatchIssue_FleetWriteUsesOperatorActor(t *testing.T) {
	tests := []struct {
		name      string
		envActor  string
		identity  *middleware.UserIdentity
		wantActor string
	}{
		{name: "open mode default", wantActor: defaultOperatorActor},
		{name: "open mode env override", envActor: "alice@example.com", wantActor: "alice@example.com"},
		{
			name:      "verified session email",
			envActor:  "fallback@example.com",
			identity:  &middleware.UserIdentity{UserID: "user-123", Email: "session@example.com"},
			wantActor: "session@example.com",
		},
		{
			name:      "verified session subject without email",
			identity:  &middleware.UserIdentity{UserID: "user-456"},
			wantActor: "user-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envOperatorActor, tt.envActor)
			var mutationActors []string
			server := newFleetIssueServer(t, func(r *http.Request) {
				mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
			})
			defer server.Close()

			handler := newFleetPatchHandler(t, server.URL)
			req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/issues/issue-1", strings.NewReader(`{"title":"Updated"}`))
			req.SetPathValue("id", "issue-1")
			if tt.identity != nil {
				req = req.WithContext(middleware.WithUserIdentity(req.Context(), *tt.identity))
			}
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", resp.Code, resp.Body.String())
			}
			if len(mutationActors) != 1 {
				t.Fatalf("mutation actors = %v, want one PATCH", mutationActors)
			}
			if mutationActors[0] != tt.wantActor {
				t.Errorf("X-Actor = %q, want %q", mutationActors[0], tt.wantActor)
			}
		})
	}
}

func TestHandlePatchIssue_HomeCompositeWritesUseOperatorActor(t *testing.T) {
	t.Setenv(envOperatorActor, "")
	var (
		mu             sync.Mutex
		mutationActors []string
		status         = string(types.StatusReview)
		labels         = []string{"needs-revision"}
		assignee       string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		path := r.URL.Path
		switch {
		case r.Method == http.MethodDelete && strings.HasSuffix(path, "/issues/issue-1/labels/needs-revision"):
			mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
			labels = nil
			writeFleetJSON(w, map[string]any{"success": true})
		case r.Method == http.MethodPatch && strings.HasSuffix(path, "/issues/issue-1"):
			mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
			status = "open"
			writeFleetJSON(w, map[string]any{"success": true})
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/issues/issue-1/assign"):
			mutationActors = append(mutationActors, r.Header.Get("X-Actor"))
			var body struct {
				Assignee string `json:"assignee"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode assign: %v", err)
			}
			assignee = body.Assignee
			writeFleetJSON(w, map[string]any{"success": true})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1"):
			writeFleetIssue(w, status, labels, assignee)
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1/deps"):
			writeFleetJSON(w, map[string]any{"dependencies": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1/comments"):
			writeFleetJSON(w, map[string]any{"comments": []any{}})
		default:
			t.Errorf("unexpected fleet request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := newFleetPatchHandler(t, server.URL)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/issues/issue-1", strings.NewReader(`{"status":"open","remove_labels":["needs-revision"],"assignee":"worker-1"}`))
	req.SetPathValue("id", "issue-1")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.Code, resp.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(mutationActors) != 3 {
		t.Fatalf("mutation actors = %v, want label/status/assign writes", mutationActors)
	}
	for i, actor := range mutationActors {
		if actor != defaultOperatorActor {
			t.Errorf("mutation %d X-Actor = %q, want %q", i, actor, defaultOperatorActor)
		}
	}
}

func newFleetPatchHandler(t *testing.T, baseURL string) http.HandlerFunc {
	t.Helper()
	be, err := fleetbackend.New(fleetbackend.Config{
		BaseURL:     baseURL,
		WorkspaceID: "test-ws",
		Actor:       "local-mode-harness@fixture.local",
	})
	if err != nil {
		t.Fatalf("fleet backend: %v", err)
	}
	svc := service.NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend {
		return be
	})
	return HandlePatchIssue(svc)
}

func newFleetIssueServer(t *testing.T, recordMutation func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPatch && strings.HasSuffix(path, "/issues/issue-1"):
			recordMutation(r)
			writeFleetJSON(w, map[string]any{"success": true})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1"):
			writeFleetIssue(w, "open", nil, "")
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1/deps"):
			writeFleetJSON(w, map[string]any{"dependencies": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/issues/issue-1/comments"):
			writeFleetJSON(w, map[string]any{"comments": []any{}})
		default:
			t.Errorf("unexpected fleet request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
}

func writeFleetIssue(w http.ResponseWriter, status string, labels []string, assignee string) {
	now := time.Now().UTC()
	writeFleetJSON(w, map[string]any{
		"id":         "issue-1",
		"title":      "Updated",
		"status":     status,
		"priority":   2,
		"type":       "task",
		"labels":     labels,
		"assignee":   assignee,
		"created_at": now,
		"updated_at": now,
	})
}

func writeFleetJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
