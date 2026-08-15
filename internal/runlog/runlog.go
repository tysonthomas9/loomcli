// Package runlog owns the private on-disk log files produced by driver and
// task runs. It centralizes path validation, tail caps, and atomic publication
// so producers and WebUI readers share one filesystem contract.
package runlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
)

const (
	// MaxBytes is the maximum persisted or returned log size. Oversized logs
	// retain their tail because backend completion details are emitted last.
	MaxBytes = 1 << 20

	taskLogDir = "task-logs"
	runLogDir  = "run-logs"
)

// ErrInvalidID reports a run identifier that cannot be a single log filename.
var ErrInvalidID = errors.New("run log id is invalid")

// ResolveRuntimeDir mirrors workflows.builtinWorkflowWorkDir: the explicit
// workspace runtime directory wins, then the daemon's working directory.
func ResolveRuntimeDir() string {
	if dir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")); dir != "" {
		return dir
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workDir
}

// TaskPath returns the fixed log path for one task run identifier.
func TaskPath(runtimeDir, taskRunID string) (string, error) {
	return logPath(runtimeDir, taskLogDir, taskRunID)
}

// DriverPath returns the fixed log path for one driver run identifier.
func DriverPath(runtimeDir, runID string) (string, error) {
	return logPath(runtimeDir, runLogDir, runID)
}

// WriteTask atomically publishes a private, tail-capped task log. Empty logs
// leave the filesystem untouched.
func WriteTask(runtimeDir, taskRunID, content string) error {
	if content == "" {
		return nil
	}
	path, err := TaskPath(runtimeDir, taskRunID)
	if err != nil {
		return err
	}
	return writeTail(path, []byte(content))
}

// WriteDriver atomically publishes private, delimited stdout and stderr for a
// driver run, retaining the tail when their combined representation is large.
func WriteDriver(runtimeDir, runID, stdout, stderr string) error {
	path, err := DriverPath(runtimeDir, runID)
	if err != nil {
		return err
	}
	content := "===== stdout =====\n" + stdout
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n===== stderr =====\n" + stderr
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeTail(path, []byte(content))
}

// ReadTail returns the final MaxBytes of a fixed log path plus file metadata.
func ReadTail(path string) (string, time.Time, bool, error) {
	file, err := os.Open(path) //nolint:gosec // callers construct paths through TaskPath or DriverPath.
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", time.Time{}, false, err
	}
	truncated := info.Size() > MaxBytes
	if truncated {
		if _, err := file.Seek(-MaxBytes, io.SeekEnd); err != nil {
			return "", time.Time{}, false, err
		}
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxBytes))
	if err != nil {
		return "", time.Time{}, false, err
	}
	return string(content), info.ModTime(), truncated, nil
}

func logPath(runtimeDir, kind, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) || filepath.Base(id) != id {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return filepath.Join(runtimeDir, ".loom", kind, id+".log"), nil
}

func writeTail(path string, content []byte) error {
	if len(content) > MaxBytes {
		content = content[len(content)-MaxBytes:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create run log directory: %w", err)
	}
	if err := atomicfile.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("publish run log: %w", err)
	}
	return nil
}
