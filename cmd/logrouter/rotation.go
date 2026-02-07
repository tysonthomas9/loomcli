package main

import (
	"fmt"
	"os"
)

// rotatingWriter wraps an os.File with size tracking and automatic rotation.
// When the file exceeds maxSize bytes, it rotates the file by renaming it
// with a numeric suffix (.1, .2, etc.) and opening a fresh file.
// If maxSize <= 0, rotation is disabled and writes pass through directly.
type rotatingWriter struct {
	path       string
	maxSize    int64
	maxBackups int
	size       int64
	file       *os.File
}

// newRotatingWriter creates a rotatingWriter that opens/creates the file at path
// in append mode. It initializes the current size from the existing file.
func newRotatingWriter(path string, maxSize int64, maxBackups int) (*rotatingWriter, error) {
	// #nosec G304 - controlled path from CLI flags
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for rotating writer: %w", err)
	}

	var size int64
	if maxSize > 0 {
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("failed to stat file for rotating writer: %w", err)
		}
		size = info.Size()
	}

	return &rotatingWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		size:       size,
		file:       f,
	}, nil
}

// Write writes p to the underlying file. If rotation is enabled and the write
// would push the file past maxSize, the file is rotated first.
func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.maxSize > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: log rotation failed: %v\n", err)
			// Continue writing to current file on rotation failure
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts backup files and opens a fresh file. On Linux, renaming a file
// while it's open is safe — the old handle remains valid. This means w.file is
// always usable even if rotation fails partway through.
func (w *rotatingWriter) rotate() error {
	oldFile := w.file

	// Shift backup files: remove oldest, rename N-1 -> N, ..., current -> .1
	for i := w.maxBackups; i >= 1; i-- {
		src := w.backupPath(i - 1)
		dst := w.backupPath(i)
		if i == 1 {
			src = w.path
		}
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		if i == w.maxBackups {
			// Remove oldest backup to make room
			os.Remove(dst)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("failed to rename %s to %s: %w", src, dst, err)
		}
	}

	// Open fresh file at the original path
	// #nosec G304 - controlled path from CLI flags
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open new file after rotation: %w", err)
	}

	w.file = f
	w.size = 0

	// Close the old file handle (now pointing to the renamed .1 backup)
	if err := oldFile.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close old log file after rotation: %v\n", err)
	}

	return nil
}

// backupPath returns the path for backup file number n (e.g., path.1, path.2).
func (w *rotatingWriter) backupPath(n int) string {
	return fmt.Sprintf("%s.%d", w.path, n)
}

// Close closes the underlying file.
func (w *rotatingWriter) Close() error {
	return w.file.Close()
}
