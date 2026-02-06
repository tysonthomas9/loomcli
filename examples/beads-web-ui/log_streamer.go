package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// logReadDefaultLines is the default number of lines to read from a log file.
	logReadDefaultLines = 200
	// logReadMaxLines is the maximum number of lines that can be requested.
	logReadMaxLines = 10000
	// logDebounceInterval is how long to wait before sending batched log lines.
	logDebounceInterval = 50 * time.Millisecond
	// logHeartbeatInterval is how often to send heartbeat comments for log streams.
	logHeartbeatInterval = 30 * time.Second
)

// LogLinePayload represents a single log line for SSE.
type LogLinePayload struct {
	Line       string `json:"line"`
	LineNumber int64  `json:"line_number"`
	Timestamp  string `json:"timestamp"` // ISO8601 when line was read
}

// LogContentResponse is the response for log content endpoints.
type LogContentResponse struct {
	Success bool            `json:"success"`
	Data    *LogContentData `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// LogContentData contains the log file content.
type LogContentData struct {
	Lines     []string `json:"lines"`
	LineCount int64    `json:"line_count"`
}

// TaskPhasesResponse is the response for listing task log phases.
type TaskPhasesResponse struct {
	Success bool             `json:"success"`
	Data    *TaskPhasesData  `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// TaskPhasesData contains the available log phases for a task.
type TaskPhasesData struct {
	Phases []string `json:"phases"`
}

// LogStreamer watches a file and streams new lines via SSE.
type LogStreamer struct {
	logFilePath string
	watcher     *fsnotify.Watcher
	currentLine int64 // Current line count in file
	fileSize    int64 // Last known file size
	mu          sync.Mutex
}

// NewLogStreamer creates a streamer for the given file.
func NewLogStreamer(fp string) (*LogStreamer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	// Watch the directory to catch file creation/rotation
	dir := filepath.Dir(fp)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to watch directory: %w", err)
	}

	return &LogStreamer{
		logFilePath: fp,
		watcher:     watcher,
	}, nil
}

// NewLogStreamerFixed is an alias for NewLogStreamer.
func NewLogStreamerFixed(fp string) (*LogStreamer, error) {
	return NewLogStreamer(fp)
}

// ReadLastNLines reads the last N lines from the file.
// Returns lines and the starting line number.
func ReadLastNLines(filepath string, n int) ([]string, int64, error) {
	if n <= 0 {
		n = logReadDefaultLines
	}
	if n > logReadMaxLines {
		n = logReadMaxLines
	}

	file, err := os.Open(filepath)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	// Read all lines (for small files this is fine)
	var lines []string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}

	totalLines := int64(len(lines))
	if totalLines <= int64(n) {
		return lines, 1, nil
	}

	// Return last N lines
	startLine := totalLines - int64(n) + 1
	return lines[totalLines-int64(n):], startLine, nil
}

// Stream starts SSE streaming to the ResponseWriter.
// Blocks until context cancelled or error.
func (s *LogStreamer) Stream(ctx context.Context, w http.ResponseWriter, startLine int64) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send retry interval
	fmt.Fprintf(w, "retry: %d\n\n", sseRetryMs)
	flusher.Flush()

	// Open file and seek to position
	file, err := os.Open(s.logFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Count lines and position
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNum := int64(0)
	for scanner.Scan() {
		// Check for cancellation periodically during large file scans
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lineNum++
		if lineNum >= startLine {
			s.sendLogLine(w, flusher, scanner.Text(), lineNum)
		}
	}
	s.mu.Lock()
	s.currentLine = lineNum
	s.mu.Unlock()

	// Get initial file size
	stat, err := file.Stat()
	if err == nil {
		s.mu.Lock()
		s.fileSize = stat.Size()
		s.mu.Unlock()
	}

	// Start streaming new lines
	heartbeat := time.NewTicker(logHeartbeatInterval)
	defer heartbeat.Stop()

	debounce := time.NewTimer(logDebounceInterval)
	debounce.Stop()
	defer debounce.Stop()

	pendingRead := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-s.watcher.Events:
			if !ok {
				return nil
			}
			// Only process events for our file
			if event.Name != s.logFilePath {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if !pendingRead {
					pendingRead = true
					debounce.Reset(logDebounceInterval)
				}
			}

		case <-debounce.C:
			pendingRead = false
			if err := s.readNewLines(w, flusher); err != nil {
				// File might have been truncated or rotated
				if err == errFileTruncated {
					s.sendTruncatedEvent(w, flusher)
					s.mu.Lock()
					s.currentLine = 0
					s.fileSize = 0
					s.mu.Unlock()
				}
			}

		case err, ok := <-s.watcher.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher error: %w", err)

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return err
			}
			flusher.Flush()
		}
	}
}

var errFileTruncated = fmt.Errorf("file truncated")

// readNewLines reads any new lines from the file and sends them.
func (s *LogStreamer) readNewLines(w http.ResponseWriter, flusher http.Flusher) error {
	file, err := os.Open(s.logFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Check for truncation
	stat, err := file.Stat()
	if err != nil {
		return err
	}

	s.mu.Lock()
	if stat.Size() < s.fileSize {
		s.mu.Unlock()
		return errFileTruncated
	}
	currentLine := s.currentLine
	s.mu.Unlock()

	// Skip to current line
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNum := int64(0)
	for scanner.Scan() {
		lineNum++
		if lineNum > currentLine {
			s.sendLogLine(w, flusher, scanner.Text(), lineNum)
		}
	}

	s.mu.Lock()
	s.currentLine = lineNum
	s.fileSize = stat.Size()
	s.mu.Unlock()

	return scanner.Err()
}

// sendLogLine sends a single log line as an SSE event.
func (s *LogStreamer) sendLogLine(w http.ResponseWriter, flusher http.Flusher, line string, lineNum int64) {
	payload := LogLinePayload{
		Line:       line,
		LineNumber: lineNum,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	eventID := sseEventIDCounter.Add(1)
	fmt.Fprintf(w, "id: %d\nevent: log-line\ndata: %s\n\n", eventID, string(data))
	flusher.Flush()
}

// sendTruncatedEvent notifies the client that the file was truncated.
func (s *LogStreamer) sendTruncatedEvent(w http.ResponseWriter, flusher http.Flusher) {
	eventID := sseEventIDCounter.Add(1)
	fmt.Fprintf(w, "id: %d\nevent: truncated\ndata: {}\n\n", eventID)
	flusher.Flush()
}

// Close releases fsnotify resources.
func (s *LogStreamer) Close() error {
	return s.watcher.Close()
}

// getLogDir returns the base log directory (~/.loom/logs).
func getLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".loom", "logs"), nil
}

// getAgentLogPath returns the path to an agent's log file.
// It validates the resolved path to prevent symlink attacks.
func getAgentLogPath(agentName string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(logDir, "agents", agentName+".log")

	// Prevent symlink attacks - ensure resolved path stays within logDir
	if err := validatePathWithinDir(logPath, logDir); err != nil {
		return "", err
	}

	return logPath, nil
}

// getTaskLogPath returns the path to a task's phase log file.
// It validates the resolved path to prevent symlink attacks.
func getTaskLogPath(taskID, phase string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(logDir, "tasks", taskID, phase+".log")

	// Prevent symlink attacks - ensure resolved path stays within logDir
	if err := validatePathWithinDir(logPath, logDir); err != nil {
		return "", err
	}

	return logPath, nil
}

// getTaskLogDir returns the directory containing a task's log files.
// It validates the resolved path to prevent symlink attacks.
func getTaskLogDir(taskID string) (string, error) {
	logDir, err := getLogDir()
	if err != nil {
		return "", err
	}

	taskDir := filepath.Join(logDir, "tasks", taskID)

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

// listTaskPhases returns the available log phases for a task.
func listTaskPhases(taskID string) ([]string, error) {
	taskDir, err := getTaskLogDir(taskID)
	if err != nil {
		return nil, err
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
	_, err := os.Stat(path)
	return err == nil
}

// readFileLastLines is a helper that handles the common log reading pattern.
func readFileLastLines(filepath string, lines int) ([]string, int64, error) {
	return ReadLastNLines(filepath, lines)
}
