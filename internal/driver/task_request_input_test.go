package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func TestRequestTaskRunInjectsCurrentRolePromptIntoCronPayload(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	workspaceDir := t.TempDir()
	prompt := "edited scout analysis prompt"
	promptFile, err := roleprompts.Publish(workspaceDir, scriptedroles.ScoutRoleName, prompt)
	if err != nil {
		t.Fatalf("publish prompt: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS", Name: scriptedroles.ScoutRoleName,
		Kind: string(domain.RoleKindWorker), PromptFile: promptFile,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: scriptedroles.ScoutWorkflowName, Name: "Scout",
		Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "scout-v1", DriverID: scriptedroles.ScoutWorkflowName, Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "scout", Name: "Scout",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: scriptedroles.ScoutRoleName,
	}); err != nil {
		t.Fatalf("create agent service: %v", err)
	}
	parent, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "cron-run", DriverID: scriptedroles.ScoutWorkflowName,
		DriverVersionID: "scout-v1", SourceKind: "cron", AgentServiceID: "scout",
		Payload: json.RawMessage(`{"tick":"2026-08-15T00:00:00Z"}`),
	})
	if err != nil {
		t.Fatalf("create parent run: %v", err)
	}
	parent, err = st.DriverRuns().Claim(ctx, "WS", parent.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("claim parent run: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey: "WS", NodeID: parent.NodeID, RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities: []string{"driver-runner", "task-runner"}, DrainState: domain.NodeDrainActive, TTL: time.Minute,
	}); err != nil {
		t.Fatalf("create task worker node: %v", err)
	}
	executor := &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted}}
	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey: "WS", WorkspaceDir: workspaceDir,
		DriverRunID: parent.RunID, TaskRunID: "scout-analyze", TaskID: "scout-analyze",
		ParentNodeID: parent.NodeID, ParentLeaseID: parent.LeaseID, ParentFence: parent.FencingToken,
		Input: json.RawMessage(`{"phase":"analyze","tick":"2026-08-15T00:00:00Z"}`),
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRunWithResult: %v", err)
	}
	if outcome.Run == nil {
		t.Fatal("task run is nil")
	}
	var input map[string]any
	if err := json.Unmarshal(executor.req.Input, &input); err != nil {
		t.Fatalf("decode injected input: %v", err)
	}
	if input["role_prompt"] != prompt || input["phase"] != "analyze" || input["tick"] != "2026-08-15T00:00:00Z" {
		t.Fatalf("injected input = %#v", input)
	}
}

func TestWithRolePromptClampsUTF8ToPayloadBudget(t *testing.T) {
	prompt := strings.Repeat("x", maxInjectedRolePromptBytes-1) + "🙂tail"
	clamped := clampUTF8Bytes(prompt, maxInjectedRolePromptBytes)
	if len(clamped) > maxInjectedRolePromptBytes || !json.Valid(mustMarshalString(t, clamped)) {
		t.Fatalf("clamped prompt bytes=%d", len(clamped))
	}
}

func mustMarshalString(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	return encoded
}
