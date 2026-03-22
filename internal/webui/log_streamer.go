package webui

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	// readChunkSize is the buffer size for seek-from-end chunk reads.
	readChunkSize = 32 * 1024
)

// LogChunkPayload represents a raw log byte chunk for SSE.
type LogChunkPayload struct {
	ChunkBase64 string `json:"chunk_b64"`
	ByteOffset  int64  `json:"byte_offset"`
	Timestamp   string `json:"timestamp"` // ISO8601 when chunk was read
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

// LogStreamer watches a file and streams new lines via SSE.
type LogStreamer struct {
	logFilePath string
	watcher     *fsnotify.Watcher
	currentSize int64 // Last byte offset consumed from file
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
		_ = watcher.Close()
		return nil, fmt.Errorf("failed to watch directory: %w", err)
	}

	return &LogStreamer{
		logFilePath: fp,
		watcher:     watcher,
	}, nil
}

// ReadLastNLines reads the last N lines from the file.
// Returns lines and the starting line number.
func ReadLastNLines(filepath string, n int) ([]string, int64, error) {
	return readLastNLinesFromFile(filepath, n, nil, 0)
}

// readLastNLinesFromFile reads the last N lines using seek-from-end to avoid
// loading the entire file into memory. If secureDir is non-nil,
// uses openLogFileSecure to prevent symlink attacks.
//
// When beforeLine > 0, reads the last N lines that appear before the given
// line number (i.e., lines ending at beforeLine-1). This enables paginated
// backward scrolling through large log files.
func readLastNLinesFromFile(filepath string, n int, secureDir *string, beforeLine int64) ([]string, int64, error) {
	if n <= 0 {
		n = logReadDefaultLines
	}
	if n > logReadMaxLines {
		n = logReadMaxLines
	}

	var file *os.File
	var err error
	if secureDir != nil {
		file, err = openLogFileSecure(filepath, *secureDir)
	} else {
		file, err = os.Open(filepath)
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
			// Nothing before line 1
			return nil, 1, nil
		}
		offset, err := findLineByteOffset(file, beforeLine)
		if err != nil {
			return nil, 0, err
		}
		if offset < 0 {
			// beforeLine exceeds total lines in file — nothing to return
			return nil, beforeLine, nil
		}
		effectiveEnd = offset
		if effectiveEnd == 0 {
			// beforeLine is 1, nothing before it
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

		// Scan backward through the chunk
		for i := nRead - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				// Skip trailing newline at end of range
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
	targetNL := lineNum - 1 // number of newlines before lineNum

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
			return -1, nil // lineNum exceeds total lines
		}
		if err != nil {
			return 0, err
		}
	}
}

// Stream starts SSE streaming to the ResponseWriter.
// startOffset is the byte offset to begin replay from.
// Blocks until context canceled or error.
func (s *LogStreamer) Stream(ctx context.Context, w http.ResponseWriter, startOffset int64) error {
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
	_, _ = fmt.Fprintf(w, "retry: %d\n\n", sseRetryMs)
	flusher.Flush()

	// Open file and seek to position
	logDir, dirErr := getLogDir()
	if dirErr != nil {
		return dirErr
	}
	file, err := openLogFileSecure(s.logFilePath, logDir)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	if startOffset < 0 {
		startOffset = 0
	}
	if startOffset > stat.Size() {
		startOffset = stat.Size()
	}

	// Read initial content and emit existing entries from offset.
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, readChunkSize)
	currentOffset := startOffset
	buf := make([]byte, readChunkSize)
	for {
		// Check for cancellation periodically during large file scans
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		nRead, readErr := reader.Read(buf)
		if nRead > 0 {
			currentOffset += int64(nRead)
			s.sendLogChunk(w, flusher, buf[:nRead], currentOffset)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	s.mu.Lock()
	s.currentSize = currentOffset
	s.mu.Unlock()

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
			if err := s.readNewChunks(w, flusher); err != nil {
				// File might have been truncated or rotated
				if err == errFileTruncated {
					s.sendTruncatedEvent(w, flusher)
					s.mu.Lock()
					s.currentSize = 0
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

// readNewChunks reads new bytes appended since the last read and emits them.
func (s *LogStreamer) readNewChunks(w http.ResponseWriter, flusher http.Flusher) error {
	logDir, dirErr := getLogDir()
	if dirErr != nil {
		return dirErr
	}
	file, err := openLogFileSecure(s.logFilePath, logDir)
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
	if stat.Size() < s.currentSize {
		s.mu.Unlock()
		return errFileTruncated
	}
	currentSize := s.currentSize
	s.mu.Unlock()

	if stat.Size() == currentSize {
		return nil
	}

	if _, err := file.Seek(currentSize, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(file, readChunkSize)
	newSize := currentSize
	buf := make([]byte, readChunkSize)

	for {
		nRead, readErr := reader.Read(buf)
		if nRead > 0 {
			newSize += int64(nRead)
			s.sendLogChunk(w, flusher, buf[:nRead], newSize)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	s.mu.Lock()
	s.currentSize = newSize
	s.mu.Unlock()

	return nil
}

// sendLogChunk sends a raw log byte chunk as an SSE event.
func (s *LogStreamer) sendLogChunk(w http.ResponseWriter, flusher http.Flusher, chunk []byte, byteOffset int64) {
	if len(chunk) == 0 {
		return
	}

	payload := LogChunkPayload{
		ChunkBase64: base64.StdEncoding.EncodeToString(chunk),
		ByteOffset:  byteOffset,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	eventID := sseEventIDCounter.Add(1)
	_, _ = fmt.Fprintf(w, "id: %d\nevent: log-chunk\ndata: %s\n\n", eventID, string(data))
	flusher.Flush()
}

// sendTruncatedEvent notifies the client that the file was truncated.
func (s *LogStreamer) sendTruncatedEvent(w http.ResponseWriter, flusher http.Flusher) {
	eventID := sseEventIDCounter.Add(1)
	_, _ = fmt.Fprintf(w, "id: %d\nevent: truncated\ndata: {}\n\n", eventID)
	flusher.Flush()
}

// Close releases fsnotify resources.
func (s *LogStreamer) Close() error {
	return s.watcher.Close()
}
