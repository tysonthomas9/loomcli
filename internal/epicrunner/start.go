// Package epicrunner owns the shared lead-assignment primitives used by the
// epic-runner workflow: lead role classification, the lead/epic bind lock, and
// the lead assignment context delivered to provider runtimes.
package epicrunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const (
	defaultBindLockTimeout      = 30 * time.Second
	defaultBindLockPollInterval = 100 * time.Millisecond
)

// ErrorKind classifies epic-runner errors for CLI callers.
type ErrorKind string

const (
	ErrorKindValidation  ErrorKind = "validation"
	ErrorKindNotFound    ErrorKind = "not_found"
	ErrorKindConflict    ErrorKind = "conflict"
	ErrorKindUnavailable ErrorKind = "unavailable"
	ErrorKindInternal    ErrorKind = "internal"
)

// Error is returned for expected epic-runner failures.
type Error struct {
	Kind ErrorKind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrorKindOf returns the classified kind for err.
func ErrorKindOf(err error) ErrorKind {
	var runErr *Error
	if errors.As(err, &runErr) {
		return runErr.Kind
	}
	if errors.Is(err, persistence.ErrNotFound) {
		return ErrorKindNotFound
	}
	return ErrorKindInternal
}

// IsLeadRole reports whether roleName is treated as a lead/orchestrator.
func IsLeadRole(roleName string) bool {
	switch strings.ToLower(strings.TrimSpace(roleName)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}

// AcquireBindLock serializes lead/epic ownership changes for a workspace.
func AcquireBindLock(workspace, leadName string) (func(), error) {
	return AcquireBindLockWithTimeout(workspace, leadName, defaultBindLockTimeout, defaultBindLockPollInterval)
}

// AcquireBindLockWithTimeout is exported for CLI tests that assert timeout behavior.
func AcquireBindLockWithTimeout(workspace, leadName string, timeout, pollInterval time.Duration) (func(), error) {
	dir := bootstrap.LoomDir()
	if dir == "" {
		return func() {}, runError(ErrorKindInternal, "cannot resolve loom data directory for lead assignment lock", nil)
	}
	lockDir := filepath.Join(dir, "epic-runner-locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return func() {}, runError(ErrorKindInternal, "create lead assignment lock directory", err)
	}
	lockName := sanitizeLockName(workspace)
	if lockName == "" {
		lockName = "lead"
	}
	lockPath := filepath.Join(lockDir, lockName+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path is under loom data dir with sanitized filename
	if err != nil {
		return func() {}, runError(ErrorKindInternal, fmt.Sprintf("open lead assignment lock %s", lockPath), err)
	}

	if pollInterval <= 0 {
		pollInterval = defaultBindLockPollInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := lockfile.TryLockExclusive(f); err != nil {
			if !errors.Is(err, lockfile.ErrLocked) {
				_ = f.Close()
				return func() {}, runError(ErrorKindInternal, fmt.Sprintf("acquire lead assignment lock %s", lockPath), err)
			}
			if timeout <= 0 || !time.Now().Before(deadline) {
				_ = f.Close()
				return func() {}, runError(ErrorKindConflict, fmt.Sprintf("timed out acquiring lead assignment lock %s for lead %q after %s", lockPath, leadName, timeout), lockfile.ErrLocked)
			}
			sleepFor := pollInterval
			if remaining := time.Until(deadline); remaining < sleepFor {
				sleepFor = remaining
			}
			time.Sleep(sleepFor)
			continue
		}
		break
	}
	return func() {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}, nil
}

func runError(kind ErrorKind, msg string, err error) error {
	return &Error{Kind: kind, Msg: msg, Err: err}
}

func sanitizeLockName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
