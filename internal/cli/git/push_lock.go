package git

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

type repoPushLock struct {
	file *os.File
	path string
}

func withRepoPushLock(repoPath string, fn func() error) error {
	lock, err := acquireRepoPushLock(repoPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to release repo integration lock %s: %v\n", lock.path, err)
		}
	}()

	return fn()
}

func acquireRepoPushLock(repoPath string) (*repoPushLock, error) {
	lockPath, err := repoPushLockPath(repoPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating repo integration lock directory: %w", err)
	}

	// #nosec G304 - lockPath is derived from a hash of a repo path, under os.TempDir.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening repo integration lock: %w", err)
	}

	fmt.Printf("Waiting for repo integration lock: %s\n", repoPath)
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquiring repo integration lock: %w", err)
	}

	if err := writeRepoPushLockMetadata(f, repoPath); err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, err
	}

	fmt.Printf("Acquired repo integration lock: %s\n", repoPath)
	return &repoPushLock{file: f, path: lockPath}, nil
}

func repoPushLockPath(repoPath string) (string, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolving repo path for integration lock: %w", err)
	}

	sum := sha256.Sum256([]byte(absPath))
	name := hex.EncodeToString(sum[:]) + ".lock"
	return filepath.Join(os.TempDir(), "loomcli-push-locks", name), nil
}

func writeRepoPushLockMetadata(f *os.File, repoPath string) error {
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating repo integration lock metadata: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("rewinding repo integration lock metadata: %w", err)
	}
	_, err := fmt.Fprintf(f, "pid=%d\nrepo=%s\nacquired_at=%s\n", os.Getpid(), repoPath, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("writing repo integration lock metadata: %w", err)
	}
	return nil
}

func (l *repoPushLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := lockfile.FlockUnlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
