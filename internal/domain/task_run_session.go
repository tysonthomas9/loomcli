package domain

import "strings"

// FlueTaskSessionIDPrefix preserves the public task-session identity that the
// historical Flue AgentSession projection exposed.
const FlueTaskSessionIDPrefix = "flue-"

// PublicTaskRunSessionID returns the session identity exposed by task-session
// and agent-history APIs for one canonical TaskRun.
//
// Flue TaskRuns keep the legacy "flue-" route identity so existing transcript
// and diff links remain stable. Other TaskRuns use their durable TaskRun ID.
func PublicTaskRunSessionID(run *TaskRun) string {
	if run == nil {
		return ""
	}
	taskRunID := strings.TrimSpace(run.TaskRunID)
	if taskRunID == "" {
		return ""
	}
	if strings.TrimSpace(run.RunnerKind) == "flue-workflow" ||
		strings.TrimSpace(run.RuntimeMetadata["runtime"]) == "flue" {
		return FlueTaskSessionIDPrefix + taskRunID
	}
	return taskRunID
}
