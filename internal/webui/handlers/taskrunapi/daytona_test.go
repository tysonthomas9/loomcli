package taskrunapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type daytonaProviderFake struct {
	calls  []execution.DaytonaProviderCommand
	result execution.DaytonaProviderResult
	err    error
}

func (fake *daytonaProviderFake) ExecuteDaytona(
	_ context.Context,
	command execution.DaytonaProviderCommand,
) (execution.DaytonaProviderResult, error) {
	fake.calls = append(fake.calls, command)
	return fake.result, fake.err
}

func validDaytonaIntent() map[string]any {
	return map[string]any{
		"schemaVersion": execution.DaytonaProviderSchemaV1,
		"repositoryUrl": "https://github.com/octocat/Hello-World.git",
		"baseRef":       "main",
		"taskPrompt":    "Make the smallest safe change.",
		"backend":       "codex",
		"delivery": map[string]any{
			"openPullRequest": false,
		},
	}
}

func successfulDaytonaResult() execution.DaytonaProviderResult {
	return execution.DaytonaProviderResult{
		SchemaVersion: execution.DaytonaProviderSchemaV1,
		Status:        "completed",
		ExitCode:      0,
		Logs:          "provider execution completed\n",
		TranscriptEntries: []execution.DaytonaTranscriptEntry{{
			Sequence: 1, Timestamp: "2026-07-30T00:00:00Z",
			Role: "assistant", Type: "text", Text: "done",
		}},
		Usage: execution.DaytonaProviderUsage{InputTokens: 10, OutputTokens: 2},
		Sandbox: execution.DaytonaSandboxReceipt{
			Provider: "daytona",
			ID:       "sandbox-opaque",
			WorkDir:  "/home/daytona",
			CWD:      "/tmp/loom-daytona-task-repo",
			RepoRef:  "abc123",
		},
		Patch: &execution.DaytonaPatchReceipt{Content: "diff --git a/a b/a\n", BaseRef: "main", HeadSHA: "abc123"},
	}
}

func TestDaytonaExecuteUsesVerifiedTaskRunIdentityAndOpaqueResult(t *testing.T) {
	h := newHarnessWithRunner(t, "daytona-task-runner")
	broker := &daytonaProviderFake{result: successfulDaytonaResult()}
	h.module.daytonaProvider = broker

	resp, decoded := h.postOp(t, "daytona-execute", validDaytonaIntent(), identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("daytona-execute status = %d: %v", resp.StatusCode, decoded)
	}
	if len(broker.calls) != 1 {
		t.Fatalf("broker calls = %d, want 1", len(broker.calls))
	}
	command := broker.calls[0]
	if command.WorkspaceKey != "WS" || command.TaskRunID != h.taskRunID ||
		command.WorkItemID != "TASK-1" || command.Intent.RepositoryURL != "https://github.com/octocat/Hello-World.git" {
		t.Fatalf("broker command = %+v", command)
	}
	if decoded["schemaVersion"] != execution.DaytonaProviderSchemaV1 || decoded["status"] != "completed" {
		t.Fatalf("Daytona result = %v", decoded)
	}
	serialized := fmt.Sprint(decoded)
	for _, forbidden := range []string{"credential", "leasetoken", "apikey", "headers", "environment"} {
		if strings.Contains(strings.ToLower(serialized), forbidden) {
			t.Fatalf("opaque response contains forbidden capability-shaped field %q: %v", forbidden, decoded)
		}
	}
}

func TestDaytonaExecuteRejectsWrongRunnerBeforeBroker(t *testing.T) {
	h := newHarnessWithRunner(t, "local-task-runner")
	broker := &daytonaProviderFake{result: successfulDaytonaResult()}
	h.module.daytonaProvider = broker

	resp, decoded := h.postOp(t, "daytona-execute", validDaytonaIntent(), identity{})
	if resp.StatusCode != http.StatusForbidden || errorCode(t, decoded) != "not_owner" {
		t.Fatalf("wrong-runner daytona-execute = %d %v, want 403 not_owner", resp.StatusCode, decoded)
	}
	if len(broker.calls) != 0 {
		t.Fatalf("wrong runner reached broker: %+v", broker.calls)
	}
}

func TestDaytonaExecuteRejectsExpiredOrWrongLeaseBeforeBroker(t *testing.T) {
	h := newHarnessWithRunner(t, "daytona-task-runner")
	broker := &daytonaProviderFake{result: successfulDaytonaResult()}
	h.module.daytonaProvider = broker

	resp, decoded := h.postOp(t, "daytona-execute", validDaytonaIntent(), identity{fence: "41"})
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, decoded) != "lease_denied" {
		t.Fatalf("stale-lease daytona-execute = %d %v, want 401 lease_denied", resp.StatusCode, decoded)
	}
	if len(broker.calls) != 0 {
		t.Fatalf("stale lease reached broker: %+v", broker.calls)
	}
}

func TestDaytonaExecuteFailsClosedWithoutBrokerOrCredentials(t *testing.T) {
	h := newHarnessWithRunner(t, "daytona-task-runner")
	resp, decoded := h.postOp(t, "daytona-execute", validDaytonaIntent(), identity{})
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != "unavailable" {
		t.Fatalf("missing broker = %d %v, want 503 unavailable", resp.StatusCode, decoded)
	}
}

func TestDaytonaExecuteStrictRequestRejectsCapabilityFields(t *testing.T) {
	h := newHarnessWithRunner(t, "daytona-task-runner")
	broker := &daytonaProviderFake{result: successfulDaytonaResult()}
	h.module.daytonaProvider = broker
	for _, field := range []string{"env", "headers", "credentials", "providerOptions", "taskRunId"} {
		body := validDaytonaIntent()
		body[field] = map[string]any{"DAYTONA_API_KEY": "credential-sentinel"}
		resp, decoded := h.postOp(t, "daytona-execute", body, identity{})
		if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
			t.Fatalf("field %s = %d %v, want 400 invalid", field, resp.StatusCode, decoded)
		}
	}
	if len(broker.calls) != 0 {
		t.Fatalf("strictly rejected requests reached broker: %+v", broker.calls)
	}
}

func TestDaytonaExecuteRejectsMalformedProviderResult(t *testing.T) {
	for name, result := range map[string]execution.DaytonaProviderResult{
		"non-zero completed exit": {
			SchemaVersion: execution.DaytonaProviderSchemaV1,
			Status:        "completed",
			ExitCode:      9,
			Sandbox:       execution.DaytonaSandboxReceipt{Provider: "daytona"},
		},
		"incomplete materialization evidence": {
			SchemaVersion: execution.DaytonaProviderSchemaV1,
			Status:        "completed",
			ExitCode:      0,
			Sandbox: execution.DaytonaSandboxReceipt{
				Provider: "daytona",
				ID:       "sandbox-opaque",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithRunner(t, "daytona-task-runner")
			h.module.daytonaProvider = &daytonaProviderFake{result: result}
			resp, decoded := h.postOp(t, "daytona-execute", validDaytonaIntent(), identity{})
			if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != "unavailable" {
				t.Fatalf("malformed provider result = %d %v, want 503 unavailable", resp.StatusCode, decoded)
			}
		})
	}
}
