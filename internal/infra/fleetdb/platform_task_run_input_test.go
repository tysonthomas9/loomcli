package fleetdb

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// The fleetdb client sends the optional task-run Input payload as a verbatim
// JSON object on create and decodes it back from the fleet-db v1 (snake_case)
// response. A nil Input omits the field (back-compat).
func TestPlatformClientTaskRunInputRoundTrip(t *testing.T) {
	reviewInput := json.RawMessage(`{"kind":"github_review","prNumber":42,"diff":"@@ -1 +1 @@","rubric":["clarity"]}`)
	cases := []struct {
		name        string
		in          json.RawMessage
		wantSentKey bool
	}{
		{name: "with review payload", in: reviewInput, wantSentKey: true},
		{name: "nil payload back-compat", in: nil, wantSentKey: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sawInputKey bool
			var sentInput json.RawMessage
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/task-runs" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				raw, _ := io.ReadAll(r.Body)
				var body map[string]json.RawMessage
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				v, ok := body["input"]
				sawInputKey = ok
				sentInput = v
				// Echo a fleet-db v1 (snake_case) task run including input.
				resp := map[string]any{
					"workspace_key": "WS",
					"task_run_id":   "run-1",
					"task_id":       "WS-1",
					"status":        "queued",
					"created_at":    "2026-01-01T00:00:00Z",
					"updated_at":    "2026-01-01T00:00:00Z",
				}
				if ok {
					resp["input"] = v
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatalf("New client: %v", err)
			}
			run, err := client.TaskRuns().Create(t.Context(), store.TaskRunCreate{
				WorkspaceKey: "WS",
				TaskRunID:    "run-1",
				TaskID:       "WS-1",
				Status:       domain.TaskRunQueued,
				Input:        tc.in,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if sawInputKey != tc.wantSentKey {
				t.Fatalf("input key sent = %v, want %v", sawInputKey, tc.wantSentKey)
			}
			if tc.wantSentKey {
				if !bytes.Equal(sentInput, tc.in) {
					t.Fatalf("sent input = %q, want %q", sentInput, tc.in)
				}
				if !bytes.Equal(run.Input, tc.in) {
					t.Fatalf("decoded input = %q, want %q", run.Input, tc.in)
				}
			} else if run.Input != nil {
				t.Fatalf("decoded input = %q, want nil", run.Input)
			}
		})
	}
}
