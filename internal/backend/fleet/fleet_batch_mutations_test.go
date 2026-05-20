package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBatchCreateIssueReq_MapsSourceRepoToRepo(t *testing.T) {
	args, err := json.Marshal(backend.CreateParams{
		Title:      "Repo scoped",
		IssueType:  "task",
		Priority:   2,
		SourceRepo: "repo-a",
	})
	if err != nil {
		t.Fatalf("Marshal CreateParams: %v", err)
	}

	req, err := batchCreateIssueReq(backend.BatchOp{
		Operation: "create",
		Args:      args,
	})
	if err != nil {
		t.Fatalf("batchCreateIssueReq: %v", err)
	}

	if req.Repo != "repo-a" {
		t.Fatalf("Repo = %q, want repo-a", req.Repo)
	}
}

func TestFleetBatchAndWorkflowSmallHelperBranches(t *testing.T) {
	if _, err := batchCreateIssueReq(backend.BatchOp{Args: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid create args error = nil")
	}

	results := make([]backend.BatchResult, 3)
	results[1] = backend.BatchResult{Success: false, Error: "local parse error"}
	failPendingBatchCreates([]int{0, 1, 2}, results, "remote failed")
	if results[0].Error != "remote failed" || results[1].Error != "local parse error" || results[2].Error != "remote failed" {
		t.Fatalf("failPendingBatchCreates results = %+v", results)
	}

	results = make([]backend.BatchResult, 3)
	results[1] = backend.BatchResult{Success: false, Error: "skip"}
	assignBatchCreateResults([]int{0, 1, 2}, results, []fleetIssueWire{{ID: "NEW-1", Title: "new", Status: "open", Priority: 2, Type: "task"}})
	if !results[0].Success || len(results[0].Data) == 0 {
		t.Fatalf("first assigned result = %+v", results[0])
	}
	if results[1].Error != "skip" {
		t.Fatalf("existing error was overwritten: %+v", results[1])
	}
	if !results[2].Success || len(results[2].Data) != 0 {
		t.Fatalf("missing response slot result = %+v", results[2])
	}

	fb := &FleetBackend{actor: "default-actor"}
	assignee := "explicit"
	if got := fb.claimActor(&assignee, &backend.IssueDetailData{IssueData: backend.IssueData{Assignee: "current"}}); got != "explicit" {
		t.Fatalf("claimActor explicit = %q", got)
	}
	if got := fb.claimActor(nil, &backend.IssueDetailData{IssueData: backend.IssueData{Assignee: "current"}}); got != "current" {
		t.Fatalf("claimActor current = %q", got)
	}
	empty := ""
	if got := fb.claimActor(&empty, nil); got != "default-actor" {
		t.Fatalf("claimActor backend actor = %q", got)
	}

	blank := "  "
	if got, err := parseOptionalFleetTime(&blank); err != nil || !got.IsZero() {
		t.Fatalf("blank parseOptionalFleetTime = %v err=%v", got, err)
	}
	raw := time.Now().UTC().Format(time.RFC3339Nano)
	if got, err := parseOptionalFleetTime(&raw); err != nil || got.IsZero() {
		t.Fatalf("valid parseOptionalFleetTime = %v err=%v", got, err)
	}
	bad := "tomorrow"
	if _, err := parseOptionalFleetTime(&bad); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("invalid parseOptionalFleetTime err = %v", err)
	}
}

func TestFleetBatchSingleOpLocalValidationBranches(t *testing.T) {
	fb := &FleetBackend{}
	if got := fb.runSingleUpdate(t.Context(), backend.BatchOp{Args: json.RawMessage(`{`)}); got.Success || !strings.Contains(got.Error, "unmarshal update args") {
		t.Fatalf("invalid update batch result = %+v", got)
	}
	if got := fb.runSingleUpdate(t.Context(), backend.BatchOp{Args: json.RawMessage(`{"params":{}}`)}); got.Success || !strings.Contains(got.Error, "missing id") {
		t.Fatalf("missing update id result = %+v", got)
	}
	if got := fb.runSingleDelete(t.Context(), backend.BatchOp{Args: json.RawMessage(`{`)}); got.Success || !strings.Contains(got.Error, "unmarshal delete args") {
		t.Fatalf("invalid delete batch result = %+v", got)
	}
	if got := fb.runSingleDelete(t.Context(), backend.BatchOp{Args: json.RawMessage(`{"force":true}`)}); got.Success || !strings.Contains(got.Error, "missing id/ids") {
		t.Fatalf("missing delete id result = %+v", got)
	}

	if normalizeFleetCursor("") != "0" || normalizeFleetCursor("123") != "123-0" || normalizeFleetCursor("bad") != "0" {
		t.Fatalf("normalizeFleetCursor basic branches failed")
	}
	if !isFleetStreamID("123-4") || isFleetStreamID("123") || isFleetStreamID("a-b") {
		t.Fatalf("isFleetStreamID branches failed")
	}
	encoded := normalizeFleetCursorForV2("123-4")
	if !strings.HasPrefix(encoded, fleetOpaqueCursorPrefix) || normalizeFleetCursor(encoded) != "123-4" {
		t.Fatalf("opaque cursor roundtrip = %q", encoded)
	}
}

func TestFleetBatchMixedSuccessPaths(t *testing.T) {
	title := "updated"
	createArgs, err := json.Marshal(backend.CreateParams{
		Title:      "new issue",
		IssueType:  "task",
		Priority:   2,
		SourceRepo: "api",
	})
	if err != nil {
		t.Fatalf("marshal create args: %v", err)
	}
	updateArgs, err := json.Marshal(struct {
		ID     string                `json:"id"`
		Params *backend.UpdateParams `json:"params"`
	}{
		ID:     "UP-1",
		Params: &backend.UpdateParams{Title: &title},
	})
	if err != nil {
		t.Fatalf("marshal update args: %v", err)
	}

	var sawBatchCreate, sawBatchClose, sawUpdate, sawDelete bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/test-ws/issues/batch":
			sawBatchCreate = true
			var req fleetBatchCreateReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode batch create: %v", err)
			}
			if len(req.Issues) != 1 || req.Issues[0].Repo != "api" {
				t.Fatalf("batch create req = %+v", req)
			}
			respondOK(w, fleetBatchCreateResp{Issues: []fleetIssueWire{{
				ID: "NEW-1", Title: "new issue", Status: "open", Type: "task", Priority: 2,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/test-ws/issues/batch/close":
			sawBatchClose = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/test-ws/issues/UP-1":
			sawUpdate = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/test-ws/issues/DEL-1":
			sawDelete = true
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	results, err := fb.Batch(context.Background(), []backend.BatchOp{
		{Operation: "create", Args: createArgs},
		{Operation: "close", Args: json.RawMessage(`{"id":"OLD-1","reason":"merged"}`)},
		{Operation: "update", Args: updateArgs},
		{Operation: "delete", Args: json.RawMessage(`{"id":"DEL-1","force":true}`)},
		{Operation: "noop", Args: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !sawBatchCreate || !sawBatchClose || !sawUpdate || !sawDelete {
		t.Fatalf("saw create=%t close=%t update=%t delete=%t", sawBatchCreate, sawBatchClose, sawUpdate, sawDelete)
	}
	for i := 0; i < 4; i++ {
		if !results[i].Success || results[i].Error != "" {
			t.Fatalf("result[%d] = %+v, want success", i, results[i])
		}
	}
	if results[4].Success || !strings.Contains(results[4].Error, "unsupported batch operation") {
		t.Fatalf("unsupported result = %+v", results[4])
	}
	if len(results[0].Data) == 0 {
		t.Fatalf("create result data is empty: %+v", results[0])
	}
}

func TestFleetBatchMutationAdditionalBranches(t *testing.T) {
	t.Run("list events no data and bad data", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
		})
		defer ts.Close()
		events, err := fb.ListEvents(context.Background(), "ISSUE-1", 0)
		if err != nil || len(events) != 0 {
			t.Fatalf("ListEvents no data = %+v, %v", events, err)
		}

		fb, ts = newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondOK(w, "not-history")
		})
		defer ts.Close()
		_, err = fb.ListEvents(context.Background(), "ISSUE-1", 0)
		if err == nil {
			t.Fatal("ListEvents bad data error = nil")
		}
	})

	t.Run("batch create local-only and response errors", func(t *testing.T) {
		fb := &FleetBackend{}
		results, err := fb.Batch(context.Background(), []backend.BatchOp{
			{Operation: "create", Args: json.RawMessage(`{`)},
		})
		if err != nil {
			t.Fatalf("Batch local create: %v", err)
		}
		if len(results) != 1 || results[0].Success || !strings.Contains(results[0].Error, "unmarshal create args") {
			t.Fatalf("local create results = %+v", results)
		}

		createArgs, err := json.Marshal(backend.CreateParams{Title: "new", IssueType: "task", Priority: 1})
		if err != nil {
			t.Fatalf("marshal create args: %v", err)
		}
		for _, tt := range []struct {
			name string
			body any
			code int
			want string
		}{
			{name: "remote error", body: apiResponse{Success: false, Error: "batch failed"}, code: http.StatusInternalServerError, want: "batch failed"},
			{name: "bad response", body: apiResponse{Success: true, Data: json.RawMessage(`"not-a-response"`)}, want: "unmarshal batch response"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
					if tt.code != 0 {
						w.WriteHeader(tt.code)
					}
					_ = json.NewEncoder(w).Encode(tt.body)
				})
				defer ts.Close()
				results, err := fb.Batch(context.Background(), []backend.BatchOp{
					{Operation: "create", Args: json.RawMessage(`{`)},
					{Operation: "create", Args: createArgs},
				})
				if err != nil {
					t.Fatalf("Batch: %v", err)
				}
				if !strings.Contains(results[1].Error, tt.want) {
					t.Fatalf("results = %+v, want error containing %q", results, tt.want)
				}
			})
		}
	})

	t.Run("batch close local and remote errors", func(t *testing.T) {
		fb := &FleetBackend{}
		results, err := fb.Batch(context.Background(), []backend.BatchOp{
			{Operation: "close", Args: json.RawMessage(`{`)},
			{Operation: "close", Args: json.RawMessage(`{"reason":"missing id"}`)},
		})
		if err != nil {
			t.Fatalf("Batch local close: %v", err)
		}
		if !strings.Contains(results[0].Error, "unmarshal close args") || !strings.Contains(results[1].Error, "missing id") {
			t.Fatalf("local close results = %+v", results)
		}

		fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			respondErr(w, http.StatusInternalServerError, "close failed")
		})
		defer ts.Close()
		results, err = fb.Batch(context.Background(), []backend.BatchOp{
			{Operation: "close", Args: json.RawMessage(`{`)},
			{Operation: "close", Args: json.RawMessage(`{"id":"OLD-1","reason":"done"}`)},
		})
		if err != nil {
			t.Fatalf("Batch remote close: %v", err)
		}
		if !strings.Contains(results[1].Error, "close failed") {
			t.Fatalf("remote close results = %+v", results)
		}
	})

	t.Run("single update flat success and failure", func(t *testing.T) {
		title := "flat"
		var sawUpdate bool
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				respondOK(w, backend.IssueDetailData{IssueData: backend.IssueData{ID: "UP-1", Title: "old", Status: "open"}})
				return
			}
			if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/UP-1") {
				sawUpdate = true
				respondOK(w, json.RawMessage(`{}`))
				return
			}
			respondOK(w, json.RawMessage(`{}`))
		})
		defer ts.Close()
		result := fb.runSingleUpdate(context.Background(), backend.BatchOp{Args: mustJSON(t, struct {
			ID    string  `json:"id"`
			Title *string `json:"title"`
		}{ID: "UP-1", Title: &title})})
		if !result.Success || !sawUpdate {
			t.Fatalf("flat update result=%+v sawUpdate=%t", result, sawUpdate)
		}

		fb, ts = newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			respondErr(w, http.StatusInternalServerError, "update failed")
		})
		defer ts.Close()
		result = fb.runSingleUpdate(context.Background(), backend.BatchOp{Args: mustJSON(t, struct {
			ID    string  `json:"id"`
			Title *string `json:"title"`
		}{ID: "UP-1", Title: &title})})
		if result.Success || !strings.Contains(result.Error, "update failed") {
			t.Fatalf("failed update result = %+v", result)
		}
	})

	t.Run("single delete ids success and failure", func(t *testing.T) {
		var deleted []string
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			deleted = append(deleted, r.URL.Path)
			respondOK(w, json.RawMessage(`{}`))
		})
		defer ts.Close()
		result := fb.runSingleDelete(context.Background(), backend.BatchOp{Args: json.RawMessage(`{"ids":["A","B"],"force":true}`)})
		if !result.Success || len(deleted) != 2 {
			t.Fatalf("delete result=%+v deleted=%v", result, deleted)
		}

		fb, ts = newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			respondErr(w, http.StatusInternalServerError, "delete failed")
		})
		defer ts.Close()
		result = fb.runSingleDelete(context.Background(), backend.BatchOp{Args: json.RawMessage(`{"id":"A"}`)})
		if result.Success || !strings.Contains(result.Error, "delete failed") {
			t.Fatalf("failed delete result = %+v", result)
		}
	})
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
