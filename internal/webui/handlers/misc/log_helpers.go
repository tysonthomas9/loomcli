package misc

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// logReadDefaultLines is the default number of lines to read from a log file.
const logReadDefaultLines = 200

// logReadMaxLines is the maximum number of lines that can be requested.
const logReadMaxLines = 10000

// readChunkSize is the buffer size for seek-from-end chunk reads.
const readChunkSize = 32 * 1024

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
	StartLine int64    `json:"start_line"`
}

// TaskPhasesResponse is the response for listing task log phases.
type TaskPhasesResponse struct {
	Success bool            `json:"success"`
	Data    *TaskPhasesData `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// TaskPhasesData contains the available log phases for a task.
type TaskPhasesData struct {
	Phases []string `json:"phases"`
}

// defaultWorkspaceDir is the sentinel directory name used when no workspace ID
// is available.
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

// getTaskLogPath returns the path to a task's phase log file, scoped by workspace.
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

	if err := validatePathWithinDir(logPath, logDir); err != nil {
		return "", err
	}

	return logPath, nil
}

// getTaskLogDir returns the directory containing a task's log files, scoped by workspace.
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

	if err := validatePathWithinDir(taskDir, logDir); err != nil {
		return "", err
	}

	return taskDir, nil
}

// validatePathWithinDir checks that the resolved path stays within the allowed directory.
func validatePathWithinDir(path, allowedDir string) error {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(path)
			resolvedParent, err := filepath.EvalSymlinks(parentDir)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to resolve parent path: %w", err)
			}
			if err == nil {
				if !strings.HasPrefix(resolvedParent+string(filepath.Separator), allowedDir+string(filepath.Separator)) &&
					resolvedParent != allowedDir {
					return fmt.Errorf("path outside allowed directory")
				}
			}
			return nil
		}
		return fmt.Errorf("failed to resolve path: %w", err)
	}

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

// readFileLastLines reads the last N lines from a file with secure open.
// When beforeLine > 0, reads lines ending before that line number.
func readFileLastLines(fpath string, lines int, beforeLine int64) ([]string, int64, error) {
	logDir, err := getLogDir()
	if err != nil {
		return nil, 0, err
	}
	return readLastNLinesFromFile(fpath, lines, &logDir, beforeLine)
}

// readLastNLinesFromFile reads the last N lines using seek-from-end to avoid
// loading the entire file into memory. If secureDir is non-nil,
// uses openLogFileSecure to prevent symlink attacks.
func readLastNLinesFromFile(fpath string, n int, secureDir *string, beforeLine int64) ([]string, int64, error) { //nolint:gocognit,cyclop,funlen
	if n <= 0 {
		n = logReadDefaultLines
	}
	if n > logReadMaxLines {
		n = logReadMaxLines
	}

	var file *os.File
	var err error
	if secureDir != nil {
		file, err = OpenLogFileSecure(fpath, *secureDir)
	} else {
		file, err = os.Open(fpath) //nolint:gosec
	}
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	fileSize := stat.Size()

	// Empty file
	if fileSize == 0 {
		return nil, 1, nil
	}

	// Determine effective end position for backward scan
	effectiveEnd := fileSize
	if beforeLine > 0 {
		if beforeLine <= 1 {
			return nil, 1, nil
		}
		offset, err := findLineByteOffset(file, beforeLine)
		if err != nil {
			return nil, 0, err
		}
		if offset < 0 {
			return nil, beforeLine, nil
		}
		effectiveEnd = offset
		if effectiveEnd == 0 {
			return nil, 1, nil
		}
	}

	// Phase 1: Backward scan to find byte offset of last N lines
	readOffset := int64(0)
	newlineCount := 0
	buf := make([]byte, readChunkSize)
	pos := effectiveEnd
	firstChunk := true

	for pos > 0 && newlineCount < n {
		chunkSize := int64(readChunkSize)
		if chunkSize > pos {
			chunkSize = pos
		}
		pos -= chunkSize

		nRead, err := file.ReadAt(buf[:chunkSize], pos)
		if err != nil && err != io.EOF {
			return nil, 0, err
		}

		for i := nRead - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				if firstChunk && pos+int64(i) == effectiveEnd-1 {
					firstChunk = false
					continue
				}
				firstChunk = false
				newlineCount++
				if newlineCount == n {
					readOffset = pos + int64(i) + 1
					break
				}
			} else {
				firstChunk = false
			}
		}
	}

	// Phase 2: Count lines before readOffset to compute startLine
	var startLine int64
	if readOffset == 0 {
		startLine = 1
	} else {
		linesBeforeOffset := int64(0)
		countPos := int64(0)
		for countPos < readOffset {
			chunkSize := int64(readChunkSize)
			if countPos+chunkSize > readOffset {
				chunkSize = readOffset - countPos
			}
			nRead, err := file.ReadAt(buf[:chunkSize], countPos)
			if err != nil && err != io.EOF {
				return nil, 0, err
			}
			for i := 0; i < nRead; i++ {
				if buf[i] == '\n' {
					linesBeforeOffset++
				}
			}
			countPos += int64(nRead)
		}
		startLine = linesBeforeOffset + 1
	}

	// Phase 3: Read lines from readOffset to effectiveEnd using Scanner
	if _, err := file.Seek(readOffset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	reader := io.LimitReader(file, effectiveEnd-readOffset)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}

	return lines, startLine, nil
}

// findLineByteOffset returns the byte offset where the given 1-based line
// number starts in the file. Returns -1 if lineNum exceeds the total number
// of lines in the file.
func findLineByteOffset(file *os.File, lineNum int64) (int64, error) {
	if lineNum <= 1 {
		return 0, nil
	}

	buf := make([]byte, readChunkSize)
	pos := int64(0)
	nlCount := int64(0)
	targetNL := lineNum - 1

	for {
		nRead, err := file.ReadAt(buf, pos)
		if nRead > 0 {
			for i := 0; i < nRead; i++ {
				if buf[i] == '\n' {
					nlCount++
					if nlCount == targetNL {
						return pos + int64(i) + 1, nil
					}
				}
			}
		}
		pos += int64(nRead)
		if err == io.EOF {
			return -1, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
