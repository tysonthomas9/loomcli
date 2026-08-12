package execution

import (
	"crypto/sha256"
	"encoding/hex"
)

// FleetDB command identities are capped at 128 bytes.
const requestTaskRunRequestIDMaxLength = 128

// RequestedTaskRunID derives the stable child-run identity used when a
// workflow omits taskRunId. Replaying the same parent/work-item request after
// a lost response therefore reaches the same queued TaskRun.
func RequestedTaskRunID(parentRunID, workItemID string) string {
	digest := sha256.Sum256([]byte("loom-task-run-request\x00" + parentRunID + "\x00" + workItemID))
	return "task-run-" + hex.EncodeToString(digest[:16])
}

// RequestedDriverStepID binds the structured DriverStep link to the exact
// parent and TaskRun identities. Caller-supplied TaskRun IDs remain supported
// while the step identity stays deterministic across retries.
func RequestedDriverStepID(parentRunID, taskRunID string) string {
	digest := sha256.Sum256([]byte("loom-driver-step-request\x00" + parentRunID + "\x00" + taskRunID))
	return "step-" + hex.EncodeToString(digest[:16])
}

func RequestTaskRunRequestID(parentRunID, taskRunID string) string {
	digest := sha256.Sum256([]byte("loom-exec-task-request\x00" + parentRunID + "\x00" + taskRunID))
	return "exec-task:sha256:" + hex.EncodeToString(digest[:])
}
