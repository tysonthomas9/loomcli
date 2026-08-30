package workflows

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
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
	if !workflowdefs.RunNeedsLocalTaskRunnerPreflight(workflowName, payload) {
		return nil
	}
	return runtimepreflight.RequireLocalTaskRunner(ctx, m.store, runtimepreflight.Request{WorkspaceKey: ws})
}
