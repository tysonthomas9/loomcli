package webui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultWorkspaceDir is the sentinel directory name used when no workspace ID
// is available. This prevents log files from colliding with workspace-scoped
// directories directly under ~/.loom/logs/.
const defaultWorkspaceDir = "_default"

// getLogDir returns the base log directory (~/.loom/logs).
func getLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".loom", "logs"), nil
}

// getWorkspaceLogDir returns the workspace-scoped log directory (~/.loom/logs/{wsID}).
// If workspaceID is empty, falls back to ~/.loom/logs/_default.
func getWorkspaceLogDir(workspaceID string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}
	if workspaceID == "" {
		workspaceID = defaultWorkspaceDir
	}
	return filepath.Join(logDir, workspaceID), nil
}

// getAgentLogPath returns the path to an agent's log file, scoped by workspace.
// It validates the resolved path to prevent symlink attacks.
func getAgentLogPath(workspaceID, agentName string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	wsLogDir, err := getWorkspaceLogDir(workspaceID)
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(wsLogDir, "agents", agentName+".log")

	// Prevent symlink attacks - ensure resolved path stays within logDir
	if err := validatePathWithinDir(logPath, logDir); err != nil {
		return "", err
	}

	return logPath, nil
}

// getTaskLogPath returns the path to a task's phase log file, scoped by workspace.
// It validates the resolved path to prevent symlink attacks.
func getTaskLogPath(workspaceID, taskID, phase string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	wsLogDir, err := getWorkspaceLogDir(workspaceID)
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(wsLogDir, "tasks", taskID, phase+".log")

	// Prevent symlink attacks - ensure resolved path stays within logDir
	if err := validatePathWithinDir(logPath, logDir); err != nil {
		return "", err
	}

	return logPath, nil
}

// getTaskLogDir returns the directory containing a task's log files, scoped by workspace.
// It validates the resolved path to prevent symlink attacks.
func getTaskLogDir(workspaceID, taskID string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	wsLogDir, err := getWorkspaceLogDir(workspaceID)
	if err != nil {
		return "", err
	}

	taskDir := filepath.Join(wsLogDir, "tasks", taskID)

	// Prevent symlink attacks - ensure resolved path stays within logDir
	if err := validatePathWithinDir(taskDir, logDir); err != nil {
		return "", err
	}

	return taskDir, nil
}

// validatePathWithinDir checks that the resolved path stays within the allowed directory.
// This prevents symlink attacks where a symlink could point outside the log directory.
func validatePathWithinDir(path, allowedDir string) error {
	// Resolve any symlinks in the path
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If file doesn't exist yet, check the parent directory
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(path)
			resolvedParent, err := filepath.EvalSymlinks(parentDir)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to resolve parent path: %w", err)
			}
			if err == nil {
				// Check parent stays within allowed dir
				if !strings.HasPrefix(resolvedParent+string(filepath.Separator), allowedDir+string(filepath.Separator)) &&
					resolvedParent != allowedDir {
					return fmt.Errorf("path outside allowed directory")
				}
			}
			return nil
		}
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Ensure resolved path is within the allowed directory
	if !strings.HasPrefix(resolvedPath+string(filepath.Separator), allowedDir+string(filepath.Separator)) &&
		resolvedPath != allowedDir {
		return fmt.Errorf("path outside allowed directory")
	}

	return nil
}

// listTaskPhases returns the available log phases for a task, scoped by workspace.
func listTaskPhases(workspaceID, taskID string) ([]string, error) {
	taskDir, err := getTaskLogDir(workspaceID, taskID)
	if err != nil {
		return nil, err
	}

	// Verify directory is not a symlink before reading
	fi, err := os.Lstat(taskDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink for task directory")
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("task log path is not a directory")
	}

	entries, err := os.ReadDir(taskDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var phases []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) > 4 && name[len(name)-4:] == ".log" {
			phases = append(phases, name[:len(name)-4])
		}
	}
	return phases, nil
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readFileLastLines is a helper that handles the common log reading pattern.
// It uses openLogFileSecure to prevent symlink TOCTOU attacks.
// When beforeLine > 0, reads lines ending before that line number.
func readFileLastLines(filepath string, lines int, beforeLine int64) ([]string, int64, error) {
	logDir, err := getLogDir()
	if err != nil {
		return nil, 0, err
	}
	return readLastNLinesFromFile(filepath, lines, &logDir, beforeLine)
}
