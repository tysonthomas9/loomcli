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

func TestBatch_EmptyOpsReturnsEmpty(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for empty ops")
	})
	defer ts.Close()

	got, err := fb.Batch(context.Background(), nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestBatch_Creates_Aggregated(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{
			"issues": []map[string]interface{}{
				{"id": "new-1", "title": "One", "status": "open", "type": "task", "priority": 2, "created_at": now, "updated_at": now},
				{"id": "new-2", "title": "Two", "status": "open", "type": "task", "priority": 2, "created_at": now, "updated_at": now},
			},
			"count": 2,
		})
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"One","issue_type":"task","priority":2}`)},
		{Operation: "create", Args: json.RawMessage(`{"title":"Two","issue_type":"task","priority":2}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/batch") {
		t.Errorf("path = %q, want suffix /issues/batch", gotPath)
	}
	issues, ok := gotBody["issues"].([]interface{})
	if !ok || len(issues) != 2 {
		t.Errorf("body.issues len = %d, want 2 (body=%+v)", len(issues), gotBody)
	}
	firstIssue, _ := issues[0].(map[string]interface{})
	if _, exists := firstIssue["issue_type"]; exists {
		t.Errorf("body.issues[0] contains issue_type; fleet-db batch API expects type (body=%+v)", firstIssue)
	}
	if firstIssue["type"] != "task" {
		t.Errorf("body.issues[0].type = %v, want task", firstIssue["type"])
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d].Success = false, error = %q", i, r.Error)
		}
		if len(r.Data) == 0 {
			t.Errorf("results[%d].Data is empty", i)
		}
		var data backend.IssueData
		if err := json.Unmarshal(r.Data, &data); err != nil {
			t.Fatalf("results[%d].Data unmarshal: %v", i, err)
		}
		if data.IssueType != "task" {
			t.Errorf("results[%d].Data.issue_type = %q, want task", i, data.IssueType)
		}
	}
}
