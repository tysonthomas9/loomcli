package driver

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// taskExecRequest copies the claimed TaskRun's optional Input payload onto the
// TaskExecRequest, and that request marshals to the LOOM_TASK_RUN_REQUEST_JSON
// the runner receives. A claim without Input round-trips with no "input" key
// (back-compat).
func TestTaskExecRequestCarriesInput(t *testing.T) {
	reviewInput := json.RawMessage(`{"kind":"github_review","prNumber":7,"diff":"@@ -1 +1 @@","rubric":["clarity"]}`)
	cases := []struct {
		name          string
		input         json.RawMessage
		wantInput     json.RawMessage
		wantInJSONKey bool
	}{
		{name: "review payload delivered to runner", input: reviewInput, wantInput: reviewInput, wantInJSONKey: true},
		{name: "no payload omits input key", input: nil, wantInput: nil, wantInJSONKey: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claimed := &domain.TaskRun{
				WorkspaceKey: "WS",
				TaskRunID:    "task-run-1",
				TaskID:       "TASK-1",
				Input:        tc.input,
			}
			opts := executeClaimedTaskRunOptions{LeaseToken: "scoped-task-token"}
			refs := claimedTaskRunRefsFromOptions(claimed, opts)

			req := taskExecRequest(claimed, opts, refs)
			if tc.wantInput == nil {
				if req.Input != nil {
					t.Fatalf("req.Input = %q, want nil", req.Input)
				}
			} else if !bytes.Equal(req.Input, tc.wantInput) {
				t.Fatalf("req.Input = %q, want %q", req.Input, tc.wantInput)
			}

			// What the host bridge marshals into LOOM_TASK_RUN_REQUEST_JSON.
			encoded, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			var decoded struct {
				Input json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}
			hasKey := bytes.Contains(encoded, []byte(`"input"`))
			if hasKey != tc.wantInJSONKey {
				t.Fatalf("input key present = %v, want %v (json=%s)", hasKey, tc.wantInJSONKey, encoded)
			}
			if tc.wantInJSONKey && !bytes.Equal(decoded.Input, tc.wantInput) {
				t.Fatalf("decoded input = %q, want %q", decoded.Input, tc.wantInput)
			}
		})
	}
}
