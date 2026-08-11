package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const localTaskRunnerEntrypoint = "local-task-runner"

// BackendHealthQuery is the Workflows delivery adapter's consumer-owned port
// for checking exactly the configured local runtime provider. Serve
// composition supplies the CLI implementation; WebUI never imports CLI.
type BackendHealthQuery interface {
	BackendHealth(name string) (BackendHealth, bool)
}

type BackendHealth struct {
	Available bool
	Installed bool
	APIKeySet bool
	Message   string
}

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
func (m *Module) preflightRunnerForRun(_ context.Context, ws, workflowName string, payload json.RawMessage) error {
	if strings.TrimSpace(workflowName) != workflowcatalog.BuiltinEpicRunnerWorkflowName {
		return nil
	}
	if !runnerIsLocal(payload) {
		return nil
	}
	return m.preflightLocalTaskRunner(ws)
}

// runnerIsLocal reports whether the run payload resolves to the local task
// runner. An absent/empty runner is the UI "Locally" default, which epic-runner
// resolves to "local-task-runner".
func runnerIsLocal(payload json.RawMessage) bool {
	runner := strings.TrimSpace(payloadRunner(payload))
	return runner == "" || runner == localTaskRunnerEntrypoint
}

func (m *Module) preflightLocalTaskRunner(workspace string) error {
	backend := platformruntime.ProviderCodex
	if configured, err := bootstrap.RuntimeProvider(workspace); err == nil && configured != "" {
		backend = configured
	}
	if m == nil || m.backendHealth == nil {
		return fmt.Errorf("local task runner backend health is unavailable; configure serve backend operations")
	}
	status, ok := m.backendHealth.BackendHealth(backend)
	if !ok {
		return fmt.Errorf("local task runner backend %q is not available for health checks; "+
			"set a supported Project Default Backend (claude, codex, opencode, gemini, cursor)", backend)
	}
	if status.Available {
		return nil
	}
	detail := strings.TrimSpace(status.Message)
	if detail == "" {
		detail = "no detail reported"
	}
	switch {
	case !status.Installed:
		return fmt.Errorf("local task runner cannot start: backend %q CLI is not installed (%s); "+
			"install it or switch the Project Default Backend (local_backend_unavailable)", backend, detail)
	case !status.APIKeySet:
		return fmt.Errorf("local task runner cannot start: backend %q is missing auth (%s); "+
			"set the provider credentials or switch the Project Default Backend (local_backend_auth_missing)", backend, detail)
	default:
		return fmt.Errorf("local task runner cannot start: backend %q is not ready (%s)", backend, detail)
	}
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
