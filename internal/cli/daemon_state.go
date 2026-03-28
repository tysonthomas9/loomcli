package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// resolveDaemonPath resolves a path relative to projectDir, or returns as-is if absolute
func resolveDaemonPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectDir, path)
}

// validateDaemonPaths warns if LogDir or PIDFile paths resolve outside expected boundaries.
// Expected boundaries: within projectDir or within the loom config directory (~/.loom/).
func validateDaemonPaths(projectDir, pidFilePath, logDir string) {
	configDir := GetConfigDir()

	for _, entry := range []struct{ name, path string }{
		{"pid_file", pidFilePath},
		{"log_dir", logDir},
	} {
		resolved, err := filepath.Abs(entry.path)
		if err != nil {
			slog.Warn("cannot resolve path", "entry", entry.name, "path", entry.path, "err", err)
			continue
		}
		absProject, _ := filepath.Abs(projectDir)
		absConfig, _ := filepath.Abs(configDir)

		if strings.HasPrefix(resolved, absProject+string(filepath.Separator)) || resolved == absProject {
			continue // within project dir
		}
		if configDir != "" && (strings.HasPrefix(resolved, absConfig+string(filepath.Separator)) || resolved == absConfig) {
			continue // within ~/.loom/
		}
		slog.Warn("path resolves outside project and config directories", "entry", entry.name, "path", entry.path)
	}
}

// isLoomDaemonRunning checks if a loom daemon is running by reading PID file and checking process
func isLoomDaemonRunning(pidFilePath string) (int, bool) {
	data, err := os.ReadFile(pidFilePath) //nolint:gosec // pidFilePath constructed from known .loom directory
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}

	// Validate PID is positive (negative PIDs are invalid)
	if pid <= 0 {
		return 0, false
	}

	return pid, lockfile.IsProcessRunning(pid)
}

// writePIDFile atomically writes the PID file
func writePIDFile(path string, pid int) error {
	// Write to temp file first, then rename for atomicity
	// Include PID in temp filename to avoid race conditions with concurrent daemons
	tempFile := fmt.Sprintf("%s.%d.tmp", path, pid)
	if err := os.WriteFile(tempFile, []byte(strconv.Itoa(pid)+"\n"), 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile) // Clean up temp file on rename failure
		return err
	}
	return nil
}

// readStateFile reads the daemon-agents.json state file
func readStateFile(path string) (*DaemonState, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known .loom directory
	if err != nil {
		return nil, err
	}

	var state DaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// writeStateFile writes the daemon-agents.json state file
func writeStateFile(path string, startedAt time.Time, agents []SupervisedAgentStatus, maxRetries int) error {
	state := DaemonState{
		PID:       os.Getpid(),
		StartedAt: startedAt,
		Agents:    make([]DaemonAgentStatus, len(agents)),
	}

	for i, ap := range agents {
		das := DaemonAgentStatus{
			Worktree:       ap.Worktree,
			Role:           ap.Role,
			Repo:           ap.Repo,
			PID:            ap.PID,
			Status:         computeAgentStatus(ap, maxRetries),
			EpicID:         ap.AssignedEpicID,
			CurrentBackend: ap.CurrentBackend,
			RestartCount:   ap.RestartCount,
			LastStart:      ap.LastStart,
			LastExit:       ap.LastExit,
			LastExitCode:   ap.LastExitCode,
			StopReason:     string(ap.StopReason),
			WorktreePath:   ap.WorktreePath,
			LastErrorClass: ap.LastErrorClass,
			NoWorkCount:    ap.NoWorkCount,
			BackoffUntil:   ap.BackoffUntil,
			RemoteBranch:   ap.RemoteBranch,
		}
		// Set StoppedAt from LastExit when agent is stopped with a reason
		if ap.StopReason != "" && ap.PID == 0 {
			if !ap.LastExit.IsZero() {
				das.StoppedAt = ap.LastExit
			} else {
				das.StoppedAt = time.Now()
			}
		}
		state.Agents[i] = das
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write via temp file (include PID to avoid race conditions)
	tempFile := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile) // Clean up temp file on rename failure
		return err
	}
	return nil
}

// computeAgentStatus determines the status string based on agent state
func computeAgentStatus(ap SupervisedAgentStatus, maxRetries int) string {
	if ap.PID > 0 && lockfile.IsProcessRunning(ap.PID) {
		return "running"
	}
	// Not running - check if it failed via stop reason or restart count
	if ap.StopReason == StopReasonFatalError || ap.StopReason == StopReasonMaxRetries {
		return "failed"
	}
	// Backward compatibility: high restart count without stop reason still means failed
	if ap.RestartCount > maxRetries {
		return "failed"
	}
	return "stopped"
}
