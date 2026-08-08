package backends

import (
	"os"
	"strings"
)

// taskRunDataEnvelopeNames is the existing lease-authenticated Loom task-data
// contract consumed by `loom data` inside a TaskRun. Driver owns and appends
// these values when it launches the runner; the backend child receives only
// this exact projection, never FleetDB or other control-plane credentials.
//
// Preserve individual non-empty members even when the envelope is incomplete.
// `loom data` treats any one of these markers as TaskRun mode and fails closed
// on a partial tuple instead of falling back to a local or general backend.
var taskRunDataEnvelopeNames = [...]string{
	"LOOM_TASK_RUN_API_URL",
	"LOOM_WORKSPACE",
	"LOOM_DRIVER_WORKSPACE",
	"LOOM_TASK_RUN_ID",
	"LOOM_TASK_ID",
	"LOOM_TASK_RUN_NODE_ID",
	"LOOM_TASK_RUN_LEASE_ID",
	"LOOM_TASK_RUN_LEASE_TOKEN",
	"LOOM_RUNNER_LEASE_TOKEN",
	"LOOM_TASK_RUN_FENCING_TOKEN",
}

// appendTaskRunDataEnvelope projects the exact owner-injected TaskRun facade
// into a model backend after the ambient subprocess filter has run. The API
// validates the lease token, identity tuple, workspace, and fence; copying a
// forged or stale tuple cannot mint authority. Sensitive task-run tokens are
// deliberately re-added here because the generic ambient filter rejects every
// token-bearing variable.
func appendTaskRunDataEnvelope(env []string) []string {
	names := taskRunDataEnvelopeNames[:]
	env = removeBackendEnvValues(env, names...)
	for _, name := range names {
		value, present := os.LookupEnv(name)
		if !present || strings.TrimSpace(value) == "" {
			continue
		}
		env = append(env, name+"="+value)
	}
	return env
}
