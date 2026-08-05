package local

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	serveLogMaxBytes = int64(50 * 1024 * 1024)
	serveLogBackups  = 2
)

// boundedServeLog caps the child serve process's stdout/stderr without asking
// the child to reopen a file after rotation. Including the active file, the
// retained service log budget is at most (serveLogBackups+1)*serveLogMaxBytes.
type boundedServeLog struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	file    *os.File
	size    int64
}

func openBoundedServeLog(path string) (*boundedServeLog, error) {
	return openBoundedServeLogWithLimits(path, serveLogMaxBytes, serveLogBackups)
}

func openBoundedServeLogWithLimits(path string, maxSize int64, backups int) (*boundedServeLog, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("serve log max size must be positive")
	}
	if backups < 0 {
		return nil, fmt.Errorf("serve log backup count cannot be negative")
	}
	if err := compactOversizedServeLog(path, maxSize); err != nil {
		return nil, err
	}
	file, err := os.OpenFile( //nolint:gosec // path is derived from the configured app data directory.
		path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600,
	)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &boundedServeLog{
		path: path, maxSize: maxSize, backups: backups,
		file: file, size: stat.Size(),
	}, nil
}

// compactOversizedServeLog repairs legacy unbounded logs in-place while
// preserving their newest bytes. It uses a sibling temporary file so a crash
// leaves either the old log or the compacted log, never a partially rewritten
// active file.
func compactOversizedServeLog(path string, maxSize int64) error {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat serve log: %w", err)
	}
	if stat.Size() <= maxSize {
		return nil
	}

	source, err := os.Open(path) //nolint:gosec // path is derived from the configured app data directory.
	if err != nil {
		return fmt.Errorf("open oversized serve log: %w", err)
	}
	defer func() { _ = source.Close() }()
	if _, err := source.Seek(stat.Size()-maxSize, io.SeekStart); err != nil {
		return fmt.Errorf("seek oversized serve log: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".loom-serve-*.compact")
	if err != nil {
		return fmt.Errorf("create compacted serve log: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.CopyN(temp, source, maxSize); err != nil {
		return fmt.Errorf("compact serve log: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync compacted serve log: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close compacted serve log: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace oversized serve log: %w", err)
	}
	removeTemp = false
	return nil
}

func (log *boundedServeLog) Write(p []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file == nil {
		return 0, os.ErrClosed
	}
	originalLength := len(p)
	if int64(len(p)) > log.maxSize {
		p = p[int64(len(p))-log.maxSize:]
	}
	if log.size > 0 && log.size+int64(len(p)) > log.maxSize {
		if err := log.rotateLocked(); err != nil {
			return 0, err
		}
	}
	written, err := log.file.Write(p)
	log.size += int64(written)
	if err != nil {
		return written, err
	}
	// io.Writer requires reporting the original input length. Dropping the old
	// prefix of a single oversized write is intentional retention behavior.
	return originalLength, nil
}

func (log *boundedServeLog) rotateLocked() error {
	if err := log.file.Close(); err != nil {
		return fmt.Errorf("close serve log for rotation: %w", err)
	}
	log.file = nil
	if log.backups == 0 {
		if err := os.Remove(log.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove rotated serve log: %w", err)
		}
	} else {
		oldest := log.path + "." + strconv.Itoa(log.backups)
		if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove oldest serve log backup: %w", err)
		}
		for index := log.backups - 1; index >= 1; index-- {
			from := log.path + "." + strconv.Itoa(index)
			to := log.path + "." + strconv.Itoa(index+1)
			if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("rotate serve log backup: %w", err)
			}
		}
		if err := os.Rename(log.path, log.path+".1"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate serve log: %w", err)
		}
	}
	file, err := os.OpenFile( //nolint:gosec // path is derived from the configured app data directory.
		log.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600,
	)
	if err != nil {
		return fmt.Errorf("open rotated serve log: %w", err)
	}
	log.file = file
	log.size = 0
	return nil
}

func (log *boundedServeLog) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file == nil {
		return nil
	}
	err := log.file.Close()
	log.file = nil
	return err
}
