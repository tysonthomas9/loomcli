package backends

import (
	"sync"
)

// Thread-safe package-level state for Claude session resume.
// Set by auto-mode before invoking InvokeNonInteractive and consumed
// (read+cleared) inside the invoker.
var (
	resumeMu              sync.RWMutex
	resumeSessionID       string
	lastCapturedSessionID string
)

// SetResumeSessionID sets the Claude session ID to resume on the next
// non-interactive invocation. Thread-safe.
func SetResumeSessionID(id string) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	resumeSessionID = id
}

// GetResumeSessionID returns the current resume session ID. Thread-safe.
func GetResumeSessionID() string {
	resumeMu.RLock()
	defer resumeMu.RUnlock()
	return resumeSessionID
}

// ClearResumeSessionID clears the resume session ID. Thread-safe.
func ClearResumeSessionID() {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	resumeSessionID = ""
}

// consumeResumeSessionID atomically reads and clears the resume session ID.
func consumeResumeSessionID() string {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	id := resumeSessionID
	resumeSessionID = ""
	return id
}

// SetLastCapturedSessionID stores the Claude session ID captured from the most
// recent non-interactive invocation's stream output. Thread-safe.
func SetLastCapturedSessionID(id string) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	lastCapturedSessionID = id
}

// GetLastCapturedSessionID returns the session ID captured from the most recent
// non-interactive invocation. Thread-safe.
func GetLastCapturedSessionID() string {
	resumeMu.RLock()
	defer resumeMu.RUnlock()
	return lastCapturedSessionID
}

// ClearLastCapturedSessionID clears the last captured session ID. Thread-safe.
func ClearLastCapturedSessionID() {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	lastCapturedSessionID = ""
}
