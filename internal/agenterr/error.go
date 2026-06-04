package agenterr

import (
	"fmt"
	"time"
)

// AgentError represents a classified agent subprocess failure.
type AgentError struct {
	Class      Outcome       // Classified outcome: harness class OR loom-domain outcome
	ExitCode   int           // Process exit code
	Message    string        // Human-readable error message
	RawOutput  string        // Tail of log that was parsed (for debugging)
	Backend    string        // Which backend produced this ("claude", "codex", "opencode")
	RetryAfter time.Duration // Suggested wait time (populated for rate limits, zero otherwise)
	Timestamp  time.Time     // When the error was classified
}

func (e *AgentError) Error() string {
	msg := fmt.Sprintf("agenterr: [%s] %s: %s", e.Class, e.Backend, e.Message)
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	return msg
}

// IsRetryable returns true if this error is worth retrying.
// Transitional: see Outcome.IsRetryable.
func (e *AgentError) IsRetryable() bool {
	return e.Class.IsRetryable()
}

// IsFatal returns true if this error indicates a permanent failure.
// Transitional: see Outcome.IsFatal.
func (e *AgentError) IsFatal() bool {
	return e.Class.IsFatal()
}
