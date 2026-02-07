package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const bufferSize = 64 * 1024 // 64KB buffer

// validAgentName matches alphanumeric characters, hyphens, underscores, and dots.
// Dots are needed for beads-style IDs (e.g., "loomcli-mp5.33").
// The "." and ".." cases are rejected separately to prevent path traversal.
var validAgentName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// validTaskID matches alphanumeric characters, hyphens, underscores, and dots.
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// LogRouter handles routing log output to multiple destinations.
type LogRouter struct {
	baseDir     string
	agentName   string
	agentWriter *bufio.Writer
	agentFile   *os.File

	mu         sync.Mutex
	taskID     string
	phase      string
	taskWriter *bufio.Writer
	taskFile   *os.File
}

// NewLogRouter creates a new LogRouter that writes to the agent log file.
func NewLogRouter(agentName, baseDir string) (*LogRouter, error) {
	if !validAgentName.MatchString(agentName) || agentName == "." || agentName == ".." {
		return nil, fmt.Errorf("invalid agent name: %q (must match %s)", agentName, validAgentName.String())
	}

	agentLogPath := filepath.Join(baseDir, "agents", agentName+".log")

	// Open agent log file (append mode, create if not exists)
	// #nosec G304 - controlled path from CLI flags
	agentFile, err := os.OpenFile(agentLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open agent log file: %w", err)
	}

	return &LogRouter{
		baseDir:     baseDir,
		agentName:   agentName,
		agentWriter: bufio.NewWriterSize(agentFile, bufferSize),
		agentFile:   agentFile,
	}, nil
}

// SetTask sets the current task context for routing.
// When taskID is non-empty, output will also be written to task-specific log.
// Phase should be "planning" or "implementation".
func (r *LogRouter) SetTask(taskID, phase string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If task hasn't changed, nothing to do
	if r.taskID == taskID && r.phase == phase {
		return nil
	}

	// Close existing task log if any
	if err := r.closeTaskLogLocked(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close previous task log: %v\n", err)
	}

	// Clear task context if no task
	if taskID == "" {
		r.taskID = ""
		r.phase = ""
		return nil
	}

	// Validate taskID to prevent path traversal
	if !validTaskID.MatchString(taskID) || taskID == "." || taskID == ".." {
		return fmt.Errorf("invalid task ID: %q (must match %s)", taskID, validTaskID.String())
	}

	// Validate phase to prevent path traversal
	if phase != "planning" && phase != "implementation" {
		return fmt.Errorf("invalid phase: %q (must be 'planning' or 'implementation')", phase)
	}

	// Create task log directory
	taskLogDir := filepath.Join(r.baseDir, "tasks", taskID)
	if err := os.MkdirAll(taskLogDir, 0755); err != nil {
		return fmt.Errorf("failed to create task log directory: %w", err)
	}

	// Open task log file
	taskLogPath := filepath.Join(taskLogDir, phase+".log")
	// #nosec G304 - controlled path from CLI flags and lock file
	taskFile, err := os.OpenFile(taskLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open task log file: %w", err)
	}

	r.taskID = taskID
	r.phase = phase
	r.taskFile = taskFile
	r.taskWriter = bufio.NewWriterSize(taskFile, bufferSize)

	return nil
}

// ClearTask clears the current task context, closing the task log.
func (r *LogRouter) ClearTask() error {
	return r.SetTask("", "")
}

// Write writes data to the agent log and optionally to the task log.
func (r *LogRouter) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Always write to agent log
	n, err = r.agentWriter.Write(p)
	if err != nil {
		return n, fmt.Errorf("failed to write to agent log: %w", err)
	}

	// Write to task log if active
	if r.taskWriter != nil {
		if _, err := r.taskWriter.Write(p); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write to task log: %v\n", err)
		}
	}

	return n, nil
}

// RouteStdin reads from stdin and routes to the appropriate log files.
func (r *LogRouter) RouteStdin(ctx context.Context) error {
	reader := bufio.NewReaderSize(os.Stdin, bufferSize)
	buf := make([]byte, bufferSize)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := reader.Read(buf)
		if n > 0 {
			if _, writeErr := r.Write(buf[:n]); writeErr != nil {
				fmt.Fprintf(os.Stderr, "error writing: %v\n", writeErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// Flush flushes all buffered data to disk.
func (r *LogRouter) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.agentWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush agent log: %w", err)
	}

	if r.taskWriter != nil {
		if err := r.taskWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush task log: %w", err)
		}
	}

	return nil
}

// Close flushes and closes all log files.
func (r *LogRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error

	// Flush and close task log
	if err := r.closeTaskLogLocked(); err != nil {
		errs = append(errs, err)
	}

	// Flush and close agent log
	if err := r.agentWriter.Flush(); err != nil {
		errs = append(errs, fmt.Errorf("failed to flush agent log: %w", err))
	}
	if err := r.agentFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close agent log: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing router: %v", errs)
	}
	return nil
}

// closeTaskLogLocked closes the task log file. Must be called with mu held.
func (r *LogRouter) closeTaskLogLocked() error {
	if r.taskWriter == nil {
		return nil
	}

	var errs []error

	if err := r.taskWriter.Flush(); err != nil {
		errs = append(errs, fmt.Errorf("failed to flush task log: %w", err))
	}
	if err := r.taskFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close task log: %w", err))
	}

	r.taskWriter = nil
	r.taskFile = nil

	if len(errs) > 0 {
		return fmt.Errorf("errors closing task log: %v", errs)
	}
	return nil
}
