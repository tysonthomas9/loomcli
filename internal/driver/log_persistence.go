package driver

import "github.com/tysonthomas9/loomcli/internal/runlog"

func persistTaskRunLogs(taskRunID, logs string) error {
	return runlog.WriteTask(runlog.ResolveRuntimeDir(), taskRunID, logs)
}

func persistDriverRunLogs(runID, stdout, stderr string) error {
	return runlog.WriteDriver(runlog.ResolveRuntimeDir(), runID, stdout, stderr)
}
