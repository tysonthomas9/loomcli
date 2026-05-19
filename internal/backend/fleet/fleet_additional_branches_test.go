package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestParseFleetResponseNativeBranches(t *testing.T) {
	t.Run("empty success body", func(t *testing.T) {
		resp, err := parseFleetResponse([]byte(" \n\t"), http.StatusNoContent)
		if err != nil {
			t.Fatalf("parseFleetResponse: %v", err)
		}
		if !resp.Success || resp.Data != nil {
			t.Fatalf("resp = %+v, want success without data", resp)
		}
	})

	t.Run("native error envelope", func(t *testing.T) {
		resp, err := parseFleetResponse([]byte(`{"error":{"code":"bad","message":"not allowed"}}`), http.StatusForbidden)
		if err != nil {
			t.Fatalf("parseFleetResponse: %v", err)
		}
		if resp.Success || resp.Error != "not allowed" {
			t.Fatalf("resp = %+v, want native error message", resp)
		}
	})

	t.Run("json fallback error", func(t *testing.T) {
		resp, err := parseFleetResponse([]byte(`{"message":"plain"}`), http.StatusBadRequest)
		if err != nil {
			t.Fatalf("parseFleetResponse: %v", err)
		}
		if resp.Success || !strings.Contains(resp.Error, "plain") {
			t.Fatalf("resp = %+v, want raw JSON error", resp)
		}
	})

	t.Run("non json error", func(t *testing.T) {
		if _, err := parseFleetResponse([]byte(`not-json`), http.StatusBadGateway); err == nil {
			t.Fatal("non-JSON error body returned nil error")
		}
	})
}

func TestFleetBackendRequestBranches(t *testing.T) {
	t.Run("execURL transport error", func(t *testing.T) {
		fb, err := New(Config{BaseURL: "http://example.test", WorkspaceID: "ws", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			}),
		}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := fb.execURL(context.Background(), "Get", http.MethodGet, "http://example.test/issues", nil); err == nil {
			t.Fatal("execURL transport error returned nil")
		}
	})

	t.Run("execAsActor http error", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Actor"); got != "override" {
				t.Fatalf("X-Actor = %q, want override", got)
			}
			respondErr(w, http.StatusConflict, "busy")
		})
		defer ts.Close()

		err := fb.execAsActor(context.Background(), "ClaimIssue", "/issues/ISSUE/claim", nil, "override")
		if !backend.IsKind(err, backend.KindConflict) {
			t.Fatalf("err = %v, want conflict", err)
		}
	})
}

func TestFleetUpdateWorkflowBranches(t *testing.T) {
	tests := []struct {
		name            string
		currentStatus   string
		currentAssignee string
		update          backend.UpdateParams
		wantPaths       []string
	}{
		{
			name:            "open clears same-status assignee",
			currentStatus:   "open",
			currentAssignee: "worker",
			update:          backend.UpdateParams{Status: fleetStringPtr("open")},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"POST /api/v1/test-ws/issues/ISSUE-1/assign",
			},
		},
		{
			name:            "closed reopens then clears assignee",
			currentStatus:   "closed",
			currentAssignee: "worker",
			update:          backend.UpdateParams{Status: fleetStringPtr("open")},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"POST /api/v1/test-ws/issues/ISSUE-1/reopen",
				"POST /api/v1/test-ws/issues/ISSUE-1/assign",
			},
		},
		{
			name:            "in progress release uses current assignee",
			currentStatus:   "in_progress",
			currentAssignee: "worker",
			update:          backend.UpdateParams{Status: fleetStringPtr("open")},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"POST /api/v1/test-ws/issues/ISSUE-1/release",
			},
		},
		{
			name:          "closed target calls close",
			currentStatus: "open",
			update:        backend.UpdateParams{Status: fleetStringPtr("closed")},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"POST /api/v1/test-ws/issues/ISSUE-1/close",
			},
		},
		{
			name:          "blocked target patches status",
			currentStatus: "open",
			update:        backend.UpdateParams{Status: fleetStringPtr("blocked")},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"PATCH /api/v1/test-ws/issues/ISSUE-1",
			},
		},
		{
			name:          "deferred target posts workflow route",
			currentStatus: "open",
			update: backend.UpdateParams{
				Status:     fleetStringPtr("deferred"),
				DeferUntil: fleetStringPtr(time.Now().UTC().Format(time.RFC3339)),
			},
			wantPaths: []string{
				"GET /api/v1/test-ws/issues/ISSUE-1",
				"GET /api/v1/test-ws/issues/ISSUE-1/deps",
				"GET /api/v1/test-ws/issues/ISSUE-1/comments",
				"POST /api/v1/test-ws/issues/ISSUE-1/defer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			var sawReleaseActor bool
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISSUE-1"):
					respondOK(w, types.Issue{
						ID:        "ISSUE-1",
						Title:     "Issue",
						Status:    types.Status(tt.currentStatus),
						Assignee:  tt.currentAssignee,
						IssueType: types.TypeTask,
					})
				case strings.HasSuffix(r.URL.Path, "/release"):
					sawReleaseActor = r.Header.Get("X-Actor") == tt.currentAssignee
					respondOK(w, json.RawMessage(`{}`))
				default:
					respondOK(w, json.RawMessage(`{}`))
				}
			})
			defer ts.Close()

			if err := fb.Update(context.Background(), "ISSUE-1", tt.update); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got := strings.Join(paths, "\n"); got != strings.Join(tt.wantPaths, "\n") {
				t.Fatalf("paths:\n%s\nwant:\n%s", got, strings.Join(tt.wantPaths, "\n"))
			}
			if strings.Contains(tt.name, "release") && !sawReleaseActor {
				t.Fatal("release did not use current assignee as actor")
			}
		})
	}
}

func TestFleetUpdateValidationBranches(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			respondOK(w, types.Issue{ID: "ISSUE-1", Title: "Issue", Status: types.StatusOpen})
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.Update(context.Background(), "", backend.UpdateParams{}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("empty id err = %v, want validation", err)
	}
	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{Claim: true}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("claim err = %v, want validation", err)
	}
	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("empty params err = %v, want validation", err)
	}
	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{Status: fleetStringPtr(" ")}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("blank status err = %v, want validation", err)
	}
	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{Status: fleetStringPtr("unknown")}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("unknown status err = %v, want validation", err)
	}
	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{Status: fleetStringPtr("deferred"), DeferUntil: fleetStringPtr("tomorrow")}); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("bad defer time err = %v, want validation", err)
	}
}

func TestFleetCreateAndClaimBranches(t *testing.T) {
	t.Run("create applies non-open status", func(t *testing.T) {
		var paths []string
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
				respondOK(w, types.Issue{ID: "ISSUE-2", Title: "Issue", Status: types.StatusOpen, IssueType: types.TypeTask})
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISSUE-2"):
				respondOK(w, types.Issue{ID: "ISSUE-2", Title: "Issue", Status: types.StatusOpen, IssueType: types.TypeTask})
			default:
				respondOK(w, json.RawMessage(`{}`))
			}
		})
		defer ts.Close()

		created, err := fb.Create(context.Background(), backend.CreateParams{Title: "Issue", IssueType: "task", Status: "closed"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID != "ISSUE-2" {
			t.Fatalf("created ID = %q, want ISSUE-2", created.ID)
		}
		want := []string{
			"POST /api/v1/test-ws/issues",
			"GET /api/v1/test-ws/issues/ISSUE-2",
			"GET /api/v1/test-ws/issues/ISSUE-2/deps",
			"GET /api/v1/test-ws/issues/ISSUE-2/comments",
			"POST /api/v1/test-ws/issues/ISSUE-2/close",
		}
		if got := strings.Join(paths, "\n"); got != strings.Join(want, "\n") {
			t.Fatalf("paths:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
		}
	})

	t.Run("create empty response", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			respondOK(w, nil)
		})
		defer ts.Close()
		if _, err := fb.Create(context.Background(), backend.CreateParams{Title: "Issue"}); !backend.IsKind(err, backend.KindInternal) {
			t.Fatalf("Create err = %v, want internal", err)
		}
	})

	t.Run("claim as actor validation", func(t *testing.T) {
		fb, err := New(Config{BaseURL: "http://example.test", WorkspaceID: "ws"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := fb.ClaimIssueAsActor(context.Background(), "", 0, "agent"); !backend.IsKind(err, backend.KindValidation) {
			t.Fatalf("empty id err = %v, want validation", err)
		}
		if err := fb.ClaimIssueAsActor(context.Background(), "ISSUE-1", 0, ""); !backend.IsKind(err, backend.KindValidation) {
			t.Fatalf("empty actor err = %v, want validation", err)
		}
	})
}

func fleetStringPtr(s string) *string {
	return &s
}
