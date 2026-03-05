package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultMaxSize    int64 = 50 * 1024 * 1024 // 50MB
	defaultMaxBackups       = 5
	bufferSize              = 32 * 1024 // 32KB
)

// JSONLWriter writes Event values as newline-delimited JSON to day-based files
// with size-based rotation within each day. Thread-safe via mutex.
type JSONLWriter struct {
	dir        string
	maxSize    int64
	maxBackups int

	mu          sync.Mutex
	currentDate string
	file        *os.File
	buf         *bufio.Writer
	size        int64
	closed      bool
}

// NewJSONLWriter creates a writer that stores JSONL files in dir.
// Files are lazily opened on first write.
func NewJSONLWriter(dir string, maxSize int64, maxBackups int) *JSONLWriter {
	return &JSONLWriter{
		dir:        dir,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
}

// Write marshals the event to JSON and writes it as a single line.
func (w *JSONLWriter) Write(event Event) error {
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("writer is closed")
	}

	date := event.Timestamp.Format("2006-01-02")
	if err := w.ensureFile(date); err != nil {
		return err
	}

	// Rotate if size would exceed maxSize
	if w.maxSize > 0 && w.size+int64(len(line)) > w.maxSize {
		if err := w.rotate(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: event log rotation failed: %v\n", err)
			// rotate() clears file/buf on failure; re-open via ensureFile
			w.currentDate = ""
			if err := w.ensureFile(date); err != nil {
				return fmt.Errorf("recovering after rotation failure: %w", err)
			}
		}
	}

	n, err := w.buf.Write(line)
	w.size += int64(n)
	if err != nil {
		return fmt.Errorf("writing event: %w", err)
	}
	return nil
}

// Flush flushes the buffered writer to disk.
func (w *JSONLWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf == nil {
		return nil
	}
	return w.buf.Flush()
}

// Close flushes and closes the underlying file.
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			w.file.Close()
			return err
		}
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// ensureFile opens or switches to the file for the given date. Must be called with mu held.
func (w *JSONLWriter) ensureFile(date string) error {
	if w.currentDate == date && w.file != nil {
		return nil
	}

	// Close existing file if switching dates
	if w.file != nil {
		if w.buf != nil {
			_ = w.buf.Flush()
		}
		_ = w.file.Close()
		w.file = nil
		w.buf = nil
	}

	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return fmt.Errorf("creating events dir: %w", err)
	}

	path := w.pathForDate(date)
	// #nosec G304 - controlled path from config
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening events file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat events file: %w", err)
	}

	w.file = f
	w.buf = bufio.NewWriterSize(f, bufferSize)
	w.size = info.Size()
	w.currentDate = date
	return nil
}

func (w *JSONLWriter) pathForDate(date string) string {
	return filepath.Join(w.dir, fmt.Sprintf("events-%s.jsonl", date))
}

// rotate shifts backup files and opens a fresh file. Must be called with mu held.
// On failure, the writer's file/buf are cleared so subsequent writes fail cleanly
// rather than silently going to a renamed backup file.
func (w *JSONLWriter) rotate() error {
	if w.buf != nil {
		_ = w.buf.Flush()
	}
	oldFile := w.file

	// Clear writer state before any renames to avoid writing to a renamed backup
	// if rotation fails partway through.
	w.file = nil
	w.buf = nil
	w.size = 0

	path := w.pathForDate(w.currentDate)

	// Shift backups: remove oldest, rename N-1 -> N, ..., current -> .1
	for i := w.maxBackups; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i-1)
		dst := fmt.Sprintf("%s.%d", path, i)
		if i == 1 {
			src = path
		}
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		if i == w.maxBackups {
			os.Remove(dst)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rotating %s to %s: %w", src, dst, err)
		}
	}

	// #nosec G304 - controlled path from config
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening new events file after rotation: %w", err)
	}

	w.file = f
	w.buf = bufio.NewWriterSize(f, bufferSize)

	if oldFile != nil {
		if err := oldFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close old events file: %v\n", err)
		}
	}

	return nil
}

// Now is a test seam for the current time. Package-level so tests can override.
var Now = time.Now
