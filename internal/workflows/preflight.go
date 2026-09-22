package workflows

import (
	"encoding/json"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/localbackend"
)

// RunNeedsLocalTaskRunnerPreflight reports whether a workflow run resolves to
// the local task runner and must be readiness-gated before queueing.
func RunNeedsLocalTaskRunnerPreflight(workflowName string, payload json.RawMessage) bool {
	if strings.TrimSpace(workflowName) != BuiltinEpicRunnerWorkflowName {
		return false
	}
	runner := strings.TrimSpace(runPayloadRunner(payload))
	return runner == "" || runner == localbackend.LocalTaskRunnerEntrypoint
}

func runPayloadRunner(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var fields struct {
		Runner string `json:"runner"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	return fields.Runner
}
