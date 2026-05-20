package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

	t.Run("native success data", func(t *testing.T) {
		resp, err := parseFleetResponse([]byte(`{"id":"ISSUE-1"}`), http.StatusOK)
		if err != nil {
			t.Fatalf("parseFleetResponse: %v", err)
		}
		if !resp.Success || string(resp.Data) != `{"id":"ISSUE-1"}` {
			t.Fatalf("resp = %+v, want native data body", resp)
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

	t.Run("doRequest read and parse errors", func(t *testing.T) {
		fb, err := New(Config{BaseURL: "http://example.test", WorkspaceID: "ws", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: fleetErrBody{}, Header: make(http.Header)}, nil
			}),
		}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, _, err := fb.doRequest(context.Background(), http.MethodGet, "/issues", nil); err == nil || !strings.Contains(err.Error(), "read response body") {
			t.Fatalf("doRequest read err = %v", err)
		}
		if _, _, err := fb.doRequestURL(context.Background(), http.MethodGet, "http://example.test/issues", nil); err == nil || !strings.Contains(err.Error(), "read response body") {
			t.Fatalf("doRequestURL read err = %v", err)
		}

		fb, err = New(Config{BaseURL: "http://example.test", WorkspaceID: "ws", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("not-json")),
					Header:     make(http.Header),
				}, nil
			}),
		}})
		if err != nil {
			t.Fatalf("New parse backend: %v", err)
		}
		if _, _, err := fb.doRequest(context.Background(), http.MethodGet, "/issues", nil); err == nil || !strings.Contains(err.Error(), "non-JSON") {
			t.Fatalf("doRequest parse err = %v", err)
		}
		if _, _, err := fb.doRequestURL(context.Background(), http.MethodGet, "http://example.test/issues", nil); err == nil || !strings.Contains(err.Error(), "non-JSON") {
			t.Fatalf("doRequestURL parse err = %v", err)
		}
	})

	t.Run("execURL http error and execAsActor transport error", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondErr(w, http.StatusUnauthorized, "no token")
		})
		defer ts.Close()
		if _, err := fb.execURL(context.Background(), "List", http.MethodGet, ts.URL+"/api/v1/test-ws/issues", nil); !backend.IsKind(err, backend.KindUnavailable) {
			t.Fatalf("execURL err = %v, want unavailable auth failure", err)
		}

		fb, err := New(Config{BaseURL: "http://example.test", WorkspaceID: "ws", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			}),
		}})
		if err != nil {
			t.Fatalf("New transport backend: %v", err)
		}
		if err := fb.execAsActor(context.Background(), "ClaimIssue", "/issues/ISSUE/claim", nil, "actor"); err == nil {
			t.Fatal("execAsActor transport error returned nil")
		}
	})
}

func TestFleetBackendQueryErrorBranches(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func(*FleetBackend) error
	}{
		{
			name: "get no data",
			call: func(fb *FleetBackend) error {
				_, err := fb.Get(context.Background(), "ISSUE-1")
				return err
			},
		},
		{
			name: "get bad data",
			call: func(fb *FleetBackend) error {
				_, err := fb.Get(context.Background(), "ISSUE-1")
				return err
			},
		},
		{
			name: "ready bad data",
			call: func(fb *FleetBackend) error {
				_, err := fb.Ready(context.Background(), backend.ReadyOpts{})
				return err
			},
		},
		{
			name: "count bad data",
			call: func(fb *FleetBackend) error {
				_, err := fb.Count(context.Background(), backend.CountOpts{})
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch tt.name {
				case "get no data":
					_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
				case "get bad data":
					respondOK(w, "not an issue")
				case "ready bad data":
					respondOK(w, map[string]any{"issues": "not a list"})
				case "count bad data":
					respondOK(w, "not a count")
				}
			})
			defer ts.Close()
			if err := tt.call(fb); err == nil {
				t.Fatalf("%s error = nil", tt.name)
			}
		})
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
	})
	defer ts.Close()
	ready, err := fb.Ready(context.Background(), backend.ReadyOpts{})
	if err != nil || len(ready) != 0 {
		t.Fatalf("Ready empty data = %+v, %v", ready, err)
	}
}

func TestFleetBackendAdditionalWorkflowBranches(t *testing.T) {
	t.Run("list children search and create response errors", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			call func(*FleetBackend) error
			body any
			code int
		}{
			{name: "list server error", call: func(fb *FleetBackend) error {
				_, err := fb.List(context.Background(), backend.ListOpts{})
				return err
			}, body: apiResponse{Success: false, Error: "list failed"}, code: http.StatusInternalServerError},
			{name: "list bad data", call: func(fb *FleetBackend) error {
				_, err := fb.List(context.Background(), backend.ListOpts{})
				return err
			}, body: apiResponse{Success: true, Data: json.RawMessage(`"not-list"`)}},
			{name: "ready server error", call: func(fb *FleetBackend) error {
				_, err := fb.Ready(context.Background(), backend.ReadyOpts{})
				return err
			}, body: apiResponse{Success: false, Error: "ready failed"}, code: http.StatusInternalServerError},
			{name: "children server error", call: func(fb *FleetBackend) error {
				_, err := fb.GetChildren(context.Background(), "ISSUE-1")
				return err
			}, body: apiResponse{Success: false, Error: "children failed"}, code: http.StatusInternalServerError},
			{name: "search server error", call: func(fb *FleetBackend) error {
				_, err := fb.SearchIssues(context.Background(), "query", 0)
				return err
			}, body: apiResponse{Success: false, Error: "search failed"}, code: http.StatusInternalServerError},
			{name: "create bad data", call: func(fb *FleetBackend) error {
				_, err := fb.Create(context.Background(), backend.CreateParams{Title: "bad", IssueType: "task", Priority: 1})
				return err
			}, body: apiResponse{Success: true, Data: json.RawMessage(`"not-issue"`)}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
					if tt.code != 0 {
						w.WriteHeader(tt.code)
					}
					_ = json.NewEncoder(w).Encode(tt.body)
				})
				defer ts.Close()
				if err := tt.call(fb); err == nil {
					t.Fatalf("%s error = nil", tt.name)
				}
			})
		}
	})

	t.Run("stats dependent query errors", func(t *testing.T) {
		for _, failPath := range []string{"/issues/blocked", "/issues/deferred", "/issues/ready"} {
			t.Run(failPath, func(t *testing.T) {
				fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/test-ws/issues/count":
						respondOK(w, countIssuesResponse{Total: 1, Groups: map[string]int64{"open": 1}})
					case "/api/v1/test-ws" + failPath:
						respondErr(w, http.StatusInternalServerError, "dependent query failed")
					default:
						respondOK(w, map[string]any{"issues": []any{}})
					}
				})
				defer ts.Close()
				if _, err := fb.Stats(context.Background()); err == nil {
					t.Fatalf("Stats did not surface failure for %s", failPath)
				}
			})
		}
	})

	t.Run("create dependencies and deferred status branches", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/test-ws/issues":
				respondOK(w, types.Issue{ID: "NEW-1", Title: "new", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 1})
			case strings.HasSuffix(r.URL.Path, "/deps"):
				respondErr(w, http.StatusInternalServerError, "dependency failed")
			default:
				respondOK(w, json.RawMessage(`{}`))
			}
		})
		defer ts.Close()
		_, err := fb.Create(context.Background(), backend.CreateParams{
			Title: "new", IssueType: "task", Priority: 1, Dependencies: []string{" ", "DEP-1"},
		})
		if err == nil || !strings.Contains(err.Error(), "dependency failed") {
			t.Fatalf("Create dependency err = %v", err)
		}

		deferUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
		var sawDefer bool
		fb, ts = newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/test-ws/issues":
				respondOK(w, types.Issue{ID: "NEW-2", Title: "new", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 1})
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/NEW-2"):
				respondOK(w, types.Issue{ID: "NEW-2", Title: "new", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 1})
			case strings.HasSuffix(r.URL.Path, "/deps") || strings.HasSuffix(r.URL.Path, "/comments"):
				respondOK(w, json.RawMessage(`{}`))
			case strings.HasSuffix(r.URL.Path, "/defer"):
				sawDefer = true
				respondOK(w, json.RawMessage(`{}`))
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		})
		defer ts.Close()
		if _, err := fb.Create(context.Background(), backend.CreateParams{Title: "new", IssueType: "task", Priority: 1, Status: "deferred", DeferUntil: deferUntil}); err != nil {
			t.Fatalf("Create deferred: %v", err)
		}
		if !sawDefer {
			t.Fatal("defer endpoint was not called")
		}
	})

	t.Run("update assignment and open transition branches", func(t *testing.T) {
		if err := (&FleetBackend{}).transitionToOpen(context.Background(), "ISSUE-1", nil, true); !backend.IsKind(err, backend.KindNotFound) {
			t.Fatalf("nil transition err = %v, want not found", err)
		}

		assignee := "alice"
		status := "blocked"
		var sawAssign, sawPatch bool
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISSUE-1"):
				respondOK(w, types.Issue{ID: "ISSUE-1", Title: "issue", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 1})
			case strings.HasSuffix(r.URL.Path, "/deps") || strings.HasSuffix(r.URL.Path, "/comments"):
				respondOK(w, json.RawMessage(`{}`))
			case strings.HasSuffix(r.URL.Path, "/assign"):
				sawAssign = true
				respondOK(w, json.RawMessage(`{}`))
			case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/ISSUE-1"):
				sawPatch = true
				respondOK(w, json.RawMessage(`{}`))
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		})
		defer ts.Close()
		if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{Assignee: &assignee, Status: &status}); err != nil {
			t.Fatalf("Update assign-before-status: %v", err)
		}
		if !sawAssign || !sawPatch {
			t.Fatalf("sawAssign=%t sawPatch=%t", sawAssign, sawPatch)
		}
	})
}

type fleetErrBody struct{}

func (fleetErrBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (fleetErrBody) Close() error             { return nil }

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

func TestFleetUpdateSetLabelsReconcilesAddsAndRemoves(t *testing.T) {
	labels := []string{"old", "keep"}
	var paths []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISSUE-1"):
			respondOK(w, types.Issue{
				ID:        "ISSUE-1",
				Title:     "Issue",
				Status:    types.StatusOpen,
				IssueType: types.TypeTask,
				Labels:    append([]string(nil), labels...),
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/deps"):
			respondOK(w, map[string]any{"dependencies": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments"):
			respondOK(w, []any{})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/labels"):
			var body struct {
				Label string `json:"label"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode label body: %v", err)
			}
			labels = append(labels, body.Label)
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/"):
			label := strings.TrimPrefix(r.URL.Path, "/api/v1/test-ws/issues/ISSUE-1/labels/")
			next := labels[:0]
			for _, existing := range labels {
				if existing != label {
					next = append(next, existing)
				}
			}
			labels = next
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.Update(context.Background(), "ISSUE-1", backend.UpdateParams{SetLabels: []string{"keep", "new"}}); err != nil {
		t.Fatalf("Update SetLabels: %v", err)
	}
	if strings.Join(labels, ",") != "keep,new" {
		t.Fatalf("labels = %v, want keep,new", labels)
	}
	got := strings.Join(paths, "\n")
	for _, want := range []string{
		"GET /api/v1/test-ws/issues/ISSUE-1",
		"DELETE /api/v1/test-ws/issues/ISSUE-1/labels/old",
		"POST /api/v1/test-ws/issues/ISSUE-1/labels",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("paths missing %q:\n%s", want, got)
		}
	}
}

func TestFleetWaitForLabelStateCanceledContext(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondOK(w, types.Issue{
			ID:        "ISSUE-1",
			Title:     "Issue",
			Status:    types.StatusOpen,
			IssueType: types.TypeTask,
			Labels:    []string{"other"},
		})
	})
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fb.waitForLabelState(ctx, "ISSUE-1", "wanted", true); !backend.IsKind(err, backend.KindTimeout) {
		t.Fatalf("waitForLabelState canceled err = %v, want timeout kind", err)
	}
}

func TestIssueDataMatchesFilterMismatchBranches(t *testing.T) {
	priority := 2
	issue := backend.IssueData{
		ID:         "ISSUE-1",
		Assignee:   "nova",
		Priority:   1,
		IssueType:  "task",
		Parent:     "EPIC-1",
		Labels:     []string{"backend"},
		SourceRepo: "api",
	}
	for _, opts := range []issueDataFilter{
		{Assignee: "spark"},
		{Priority: &priority},
		{Type: "bug"},
		{ParentID: "EPIC-2"},
		{Labels: []string{"frontend"}},
		{LabelsAny: []string{"urgent"}},
	} {
		if issueDataMatches(issue, opts) {
			t.Fatalf("issueDataMatches(%+v) = true, want false", opts)
		}
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
