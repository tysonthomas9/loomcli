package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// isolateProcessGroup creates a new process group so signals to the parent's
// group don't kill the daemon.
func isolateProcessGroup() {
	if err := syscall.Setpgid(0, 0); err != nil {
		if _, err2 := syscall.Setsid(); err2 != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not isolate process group (setpgid: %v, setsid: %v)\n", err, err2)
		}
	}
}

// prepareDaemonDirs creates the PID file and log directories.
func prepareDaemonDirs(pidFilePath, logDir string) {
	if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating PID directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating log directory: %v\n", err)
		os.Exit(1)
	}
}

// cleanupOnStartFailure cleans up resources when daemon.Start fails (os.Exit skips defers).
func cleanupOnStartFailure(pidFilePath, stateFilePath string, lockFile *os.File, lockFilePath string) {
	os.Remove(pidFilePath)
	os.Remove(stateFilePath)
	lockFile.Close()
	os.Remove(lockFilePath)
}

// statusToIcon returns a Unicode icon for the given agent status string.
func statusToIcon(status string) string {
	switch status {
	case "running":
		return "●"
	case "starting":
		return "◐"
	case "stopped":
		return "○"
	case "failed":
		return "✗"
	default:
		return "?"
	}
}

// targetDir is the directory a detected daemon's sidecar files live under.
// Detection normally fills DaemonRuntimeInfo.Dir; the fallback keeps older
// callers (and tests constructing the struct by hand) working.
func targetDir(rt cli.DaemonRuntimeInfo, cwd string) string {
	if rt.Dir != "" {
		return rt.Dir
	}
	return cwd
}

// statePathForTarget resolves daemon-agents.json for the DETECTED daemon
// rather than for the caller's cwd. This is the core of PUPPET-57: when the
// workspace-lock fallback proves a daemon in another directory alive, reading
// the cwd's state file describes a different — often dead — daemon.
func statePathForTarget(rt cli.DaemonRuntimeInfo, cwd string) string {
	return cfgpkg.ResolveDaemonStatePath(targetDir(rt, cwd))
}

// readStateForTarget loads the detected daemon's state file plus its mtime.
// Both results are best effort: a nil state means "no snapshot to trust", and
// a zero mtime means freshness cannot be judged.
func readStateForTarget(statePath string) (*DaemonState, time.Time) {
	state, err := ReadStateFile(statePath)
	if err != nil {
		return nil, time.Time{}
	}
	var mtime time.Time
	if fi, statErr := os.Stat(statePath); statErr == nil {
		mtime = fi.ModTime()
	}
	return state, mtime
}

// socketPathForDir resolves the daemon control socket for a target directory,
// mirroring resolveControlSocketFromCwd but without consulting the cwd.
func socketPathForDir(dir string) string {
	config, err := cfgpkg.LoadDaemonConfig(dir)
	if err != nil {
		// Same default the cwd-based resolver falls back to.
		config = &cfgpkg.DaemonConfig{
			Daemon: cfgpkg.DaemonSettings{PIDFile: ".loom/daemon.pid"},
		}
	}
	return resolveDaemonSocketPath(dir, config.Daemon.PIDFile)
}

// liveAgentCount asks the daemon itself how many agents are running. This is
// the only cwd-independent, always-current answer available locally, so it is
// preferred over a state-file snapshot whose identity cannot be confirmed.
//
// Returns agentCountUnknown on any failure: the socket is best effort, exactly
// like pendingInputsForDir. Status must still render when it is absent.
func liveAgentCount(rt cli.DaemonRuntimeInfo, cwd string) int {
	resp, err := sendDaemonControlRequest(socketPathForDir(targetDir(rt, cwd)), ctrlOpAgentList, "")
	if err != nil || !resp.Success {
		return agentCountUnknown
	}
	var entries []AgentListEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return agentCountUnknown
	}
	// handleAgentControlList enumerates every configured agent with a computed
	// status; only the running ones correspond to the state file's agent list.
	count := 0
	for _, e := range entries {
		if e.Status == "running" {
			count++
		}
	}
	return count
}
