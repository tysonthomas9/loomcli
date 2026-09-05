package workflows

import (
	"encoding/json"
	"testing"
)

func TestRunNeedsLocalTaskRunnerPreflight(t *testing.T) {
	cases := []struct {
		name         string
		workflowName string
		payload      string
		want         bool
	}{
		{"epic runner absent runner defaults local", BuiltinEpicRunnerWorkflowName, `{"epicId":"E1"}`, true},
		{"epic runner empty payload defaults local", BuiltinEpicRunnerWorkflowName, ``, true},
		{"epic runner empty object defaults local", BuiltinEpicRunnerWorkflowName, `{}`, true},
		{"epic runner malformed payload defaults local", BuiltinEpicRunnerWorkflowName, `{not-json`, true},
		{"epic runner explicit local", BuiltinEpicRunnerWorkflowName, `{"runner":"local-task-runner"}`, true},
		{"epic runner whitespace local", `  ` + BuiltinEpicRunnerWorkflowName + `  `, `{"runner":" local-task-runner "}`, true},
		{"epic runner explicit remote", BuiltinEpicRunnerWorkflowName, `{"runner":"daytona-task-runner"}`, false},
		{"other workflow absent runner", "custom-workflow", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RunNeedsLocalTaskRunnerPreflight(tc.workflowName, json.RawMessage(tc.payload)); got != tc.want {
				t.Fatalf("RunNeedsLocalTaskRunnerPreflight(%q, %q) = %v, want %v", tc.workflowName, tc.payload, got, tc.want)
			}
		})
	}
}
