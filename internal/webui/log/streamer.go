package log

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

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	// LogReadDefaultLines is the default number of lines to read from a log file.
	LogReadDefaultLines = 200
	// LogReadMaxLines is the maximum number of lines that can be requested.
	LogReadMaxLines = 10000
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

// Stream starts SSE streaming to the ResponseWriter.
// startOffset is the byte offset to begin replay from.
// Blocks until context canceled or error.
func (s *LogStreamer) Stream(ctx context.Context, w http.ResponseWriter, startOffset int64) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}

	writeSSEHeaders(w)
	_, _ = fmt.Fprintf(w, "retry: %d\n\n", realtime.RetryMs)
	flusher.Flush()

	currentOffset, err := s.replayExistingContent(ctx, w, flusher, startOffset)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.currentSize = currentOffset
	s.mu.Unlock()

	return s.streamEventLoop(ctx, w, flusher)
}

// writeSSEHeaders sets standard Server-Sent Events headers.
func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// replayExistingContent opens the log file and replays content from startOffset.
// Returns the final byte offset after replay.
func (s *LogStreamer) replayExistingContent(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, startOffset int64) (int64, error) {
	logDir, dirErr := GetLogDir()
	if dirErr != nil {
		return 0, dirErr
	}
	file, err := openLogFileSecure(s.logFilePath, logDir)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}

	startOffset = clampOffset(startOffset, stat.Size())
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return 0, err
	}

	return s.readAndEmitChunks(ctx, w, flusher, file, startOffset)
}

// clampOffset ensures offset is within [0, fileSize].
func clampOffset(offset, fileSize int64) int64 {
	if offset < 0 {
		return 0
	}
	if offset > fileSize {
		return fileSize
	}
	return offset
}

// readAndEmitChunks reads the file from current position and emits SSE chunks.
func (s *LogStreamer) readAndEmitChunks(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, file *os.File, currentOffset int64) (int64, error) {
	reader := bufio.NewReaderSize(file, readChunkSize)
	buf := make([]byte, readChunkSize)
	for {
		select {
		case <-ctx.Done():
			return currentOffset, ctx.Err()
		default:
		}
		nRead, readErr := reader.Read(buf)
		if nRead > 0 {
			currentOffset += int64(nRead)
			s.sendLogChunk(w, flusher, buf[:nRead], currentOffset)
		}
		if readErr == io.EOF {
			return currentOffset, nil
		}
		if readErr != nil {
			return currentOffset, readErr
		}
	}
}

// streamEventLoop handles the watcher-based live streaming loop.
func (s *LogStreamer) streamEventLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) error {
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
			if event.Name == s.logFilePath && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				if !pendingRead {
					pendingRead = true
					debounce.Reset(logDebounceInterval)
				}
			}
		case <-debounce.C:
			pendingRead = false
			s.handleDebouncedRead(w, flusher)
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

// handleDebouncedRead reads new chunks and handles file truncation.
func (s *LogStreamer) handleDebouncedRead(w http.ResponseWriter, flusher http.Flusher) {
	if err := s.readNewChunks(w, flusher); err != nil {
		if err == errFileTruncated {
			s.sendTruncatedEvent(w, flusher)
			s.mu.Lock()
			s.currentSize = 0
			s.mu.Unlock()
		}
	}
}

var errFileTruncated = fmt.Errorf("file truncated")

// readNewChunks reads new bytes appended since the last read and emits them.
func (s *LogStreamer) readNewChunks(w http.ResponseWriter, flusher http.Flusher) error {
	file, currentSize, err := s.openAndCheckTruncation()
	if err != nil {
		return err
	}
	if file == nil {
		return nil // no new data
	}
	defer file.Close()

	if _, err := file.Seek(currentSize, io.SeekStart); err != nil {
		return err
	}

	newSize, err := s.emitNewData(w, flusher, file, currentSize)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.currentSize = newSize
	s.mu.Unlock()
	return nil
}

// openAndCheckTruncation opens the log file and checks if it was truncated.
// Returns (nil, 0, nil) if no new data is available.
func (s *LogStreamer) openAndCheckTruncation() (*os.File, int64, error) {
	logDir, dirErr := GetLogDir()
	if dirErr != nil {
		return nil, 0, dirErr
	}
	file, err := openLogFileSecure(s.logFilePath, logDir)
	if err != nil {
		return nil, 0, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}

	s.mu.Lock()
	if stat.Size() < s.currentSize {
		s.mu.Unlock()
		file.Close()
		return nil, 0, errFileTruncated
	}
	currentSize := s.currentSize
	s.mu.Unlock()

	if stat.Size() == currentSize {
		file.Close()
		return nil, 0, nil
	}
	return file, currentSize, nil
}

// emitNewData reads from the file at the current position and sends SSE chunks.
func (s *LogStreamer) emitNewData(w http.ResponseWriter, flusher http.Flusher, file *os.File, offset int64) (int64, error) {
	reader := bufio.NewReaderSize(file, readChunkSize)
	buf := make([]byte, readChunkSize)
	newSize := offset
	for {
		nRead, readErr := reader.Read(buf)
		if nRead > 0 {
			newSize += int64(nRead)
			s.sendLogChunk(w, flusher, buf[:nRead], newSize)
		}
		if readErr == io.EOF {
			return newSize, nil
		}
		if readErr != nil {
			return newSize, readErr
		}
	}
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
	eventID := realtime.NextEventID()
	_, _ = fmt.Fprintf(w, "id: %d\nevent: log-chunk\ndata: %s\n\n", eventID, string(data))
	flusher.Flush()
}

// sendTruncatedEvent notifies the client that the file was truncated.
func (s *LogStreamer) sendTruncatedEvent(w http.ResponseWriter, flusher http.Flusher) {
	eventID := realtime.NextEventID()
	_, _ = fmt.Fprintf(w, "id: %d\nevent: truncated\ndata: {}\n\n", eventID)
	flusher.Flush()
}

// Close releases fsnotify resources.
func (s *LogStreamer) Close() error {
	return s.watcher.Close()
}
