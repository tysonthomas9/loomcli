package serve

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/runtimecomposition"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
)

const issueBridgeCursorFileName = "issue-bridge-cursor.json"

func buildServeRuntimeConfig() runtimecomposition.Config {
	workspaceScope := driverAutomationWorkspaceScope()
	return runtimecomposition.Config{
		WorkspaceScope:        workspaceScope,
		AwaitSweepInterval:    time.Duration(boundedIntEnv(envLoomAwaitSweepInterval, 30, 3600)) * time.Second,
		AwaitSweepBatch:       boundedIntEnv(envLoomAwaitSweepBatch, driverexecutor.DefaultAwaitTimeoutSweepBatch, 500),
		DriverExecutorEnabled: driverExecutorEnabled(),
		TaskWorkerConcurrency: driverTaskWorkerConcurrency(),
		TaskWorkerRunnerID:    os.Getenv("LOOM_DRIVER_TASK_WORKER_RUNNER_ID"),
		TaskRunMaxAttempts:    driverTaskRunMaxAttempts(),
		DriverAPIBaseURL:      driverAPIBaseURL(),
		LocalSettingsDir:      bootstrap.LoomDir(),
		BuildDriverExecutor:   buildDriverExecutor,
		IssueJournal: runtimecomposition.IssueJournalConfig{
			WorkspaceScope: workspaceScope,
			Interval:       issueBridgeInterval(),
			StatePath:      issueBridgeStatePath(),
			Disabled:       issueBridgeDisabled(),
			EmitTaskReady:  taskReadyEventsEnabled(),
			EmitTaskReview: taskReviewEventsEnabled(),
		},
	}
}

func driverStaleTaskMaxAge() time.Duration {
	defaultSeconds := int(driverexecutor.DefaultStaleTaskRunMaxAge / time.Second)
	return time.Duration(
		boundedIntEnv(envLoomDriverStaleTaskMaxAge, defaultSeconds, 86400),
	) * time.Second
}

func issueBridgeInterval() time.Duration {
	return time.Duration(
		boundedIntEnv(envLoomIssueBridgeInterval, 2, 3600),
	) * time.Second
}

func issueBridgeDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomIssueBridgeDisabled))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func taskReadyEventsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomTaskReadyEvents))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func taskReviewEventsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomTaskReviewEvents))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func issueBridgeStatePath() string {
	if path := strings.TrimSpace(os.Getenv(envLoomIssueBridgeStatePath)); path != "" {
		return path
	}
	dir := bootstrap.LoomDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, issueBridgeCursorFileName)
}
