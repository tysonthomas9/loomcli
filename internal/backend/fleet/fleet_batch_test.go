package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestBatch_Creates_AllOrNothingError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 400, "validation failed on issue 0")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"Bad"}`)},
		{Operation: "create", Args: json.RawMessage(`{"title":"Good","issue_type":"task","priority":2}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v (transport error should not bubble for per-op failures)", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	for i, r := range results {
		if r.Success {
			t.Errorf("results[%d].Success = true, want false", i)
		}
		if r.Error == "" {
			t.Errorf("results[%d].Error should be non-empty", i)
		}
	}
}

func TestBatch_Closes_Aggregated(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		// fleet-db returns 204; simulate a wrapped-envelope success with nil data.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-1","reason":"done"}`)},
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-2"}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/batch/close") {
		t.Errorf("path = %q, want suffix /issues/batch/close", gotPath)
	}
	ids, _ := gotBody["issue_ids"].([]interface{})
	if len(ids) != 2 {
		t.Errorf("issue_ids len = %d, want 2", len(ids))
	}
	if gotBody["reason"] != "done" {
		t.Errorf("reason = %v, want %q (first non-empty reason should be propagated)", gotBody["reason"], "done")
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d].Success = false, error = %q", i, r.Error)
		}
	}
}

func TestBatch_Mixed_FanOut(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var calls []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/batch"):
			respondOK(w, map[string]interface{}{
				"issues": []testIssue{
					{ID: "new-1", Title: "Created", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now},
				},
				"count": 1,
			})
		case strings.HasSuffix(r.URL.Path, "/issues/batch/close"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
		case strings.Contains(r.URL.Path, "/issues/") && r.Method == "PATCH":
			respondOK(w, map[string]interface{}{"id": "loom-u"})
		case strings.Contains(r.URL.Path, "/issues/") && r.Method == "DELETE":
			respondOK(w, map[string]interface{}{})
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
			respondErr(w, 500, "unexpected path")
		}
	})
	defer ts.Close()

	newTitle := "New Title"
	updateArgs, _ := json.Marshal(map[string]interface{}{
		"id":    "loom-u",
		"title": newTitle,
	})
	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"Created","issue_type":"task","priority":2}`)},
		{Operation: "update", Args: updateArgs},
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-c"}`)},
		{Operation: "delete", Args: json.RawMessage(`{"id":"loom-d","force":true}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("len = %d, want 4", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d] not successful (err=%q)", i, r.Error)
		}
	}
	// Verify each endpoint was hit at least once.
	need := map[string]bool{"batch": false, "batch/close": false, "PATCH": false, "DELETE": false}
	for _, c := range calls {
		switch {
		case strings.Contains(c, "/issues/batch/close"):
			need["batch/close"] = true
		case strings.Contains(c, "/issues/batch"):
			need["batch"] = true
		case strings.HasPrefix(c, "PATCH"):
			need["PATCH"] = true
		case strings.HasPrefix(c, "DELETE"):
			need["DELETE"] = true
		}
	}
	for k, v := range need {
		if !v {
			t.Errorf("expected %s call, got none (calls=%v)", k, calls)
		}
	}
}

func TestBatch_UnknownOperation(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be hit for unknown op")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "teleport", Args: json.RawMessage(`{}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Errorf("results = %+v, want single failed result", results)
	}
	if !strings.Contains(results[0].Error, "unsupported batch operation") {
		t.Errorf("error = %q, want to mention unsupported batch operation", results[0].Error)
	}
}

func TestBatch_Update_MissingID(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be hit for missing id")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "update", Args: json.RawMessage(`{"title":"x"}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want single failure", results)
	}
	if !strings.Contains(results[0].Error, "missing id") {
		t.Errorf("error = %q, want to mention missing id", results[0].Error)
	}
}

func TestBatch_Update_NestedParamsShape(t *testing.T) {
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	// Nested params shape: {"id": "loom-1", "params": {...UpdateParams...}}
	args, _ := json.Marshal(map[string]interface{}{
		"id":     "loom-1",
		"params": map[string]interface{}{"title": "Nested Title"},
	})
	results, err := fb.Batch(context.Background(), []backend.BatchOp{
		{Operation: "update", Args: args},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !results[0].Success {
		t.Fatalf("result not successful: %q", results[0].Error)
	}
	if gotBody["title"] != "Nested Title" {
		t.Errorf("title = %v, want Nested Title (PATCH body should carry the nested params)", gotBody["title"])
	}
}
