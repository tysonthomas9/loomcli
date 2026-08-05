package workflows

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
)

// preflightRunnerForRun runs fail-closed checks before a workflow run is
// created. The epic-runner workflow routes child task runs to a runner; when
// that runner is the local task runner (explicit "local-task-runner" or the
// UI "Locally" default of an absent runner field), the run will shell out to
// the resolved backend CLI on the worker. If that CLI/auth is missing the run
// would fail deep in the worker — or fake-complete — so we resolve the backend
// and health-check it here, mirroring the CLI `loom epic run` preflight.
//
// Returns nil (no gate) for every non-local runner and for non-epic-runner
// workflows. Returns an actionable error string when the local runner cannot
// execute.
func (m *Module) preflightRunnerForRun(ctx context.Context, ws, workflowName string, payload json.RawMessage) error {
	if strings.TrimSpace(workflowName) != workflowcatalog.BuiltinEpicRunnerWorkflowName {
		return nil
	}
	if !runnerIsLocal(payload) {
		return nil
	}
	return runtimepreflight.PreflightLocalTaskRunner(ctx, ws)
}

// runnerIsLocal reports whether the run payload resolves to the local task
// runner. An absent/empty runner is the UI "Locally" default, which epic-runner
// resolves to "local-task-runner".
func runnerIsLocal(payload json.RawMessage) bool {
	runner := strings.TrimSpace(payloadRunner(payload))
	return runner == "" || runner == runtimepreflight.LocalTaskRunnerEntrypoint
}

// payloadRunner extracts the top-level "runner" string from the run payload,
// returning "" when absent or malformed (treated as the local default).
func payloadRunner(payload json.RawMessage) string {
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
