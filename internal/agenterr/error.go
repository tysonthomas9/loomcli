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

	// Evidence records WHICH classification step produced Class and on what
	// text. It is descriptive only: nothing here feeds policy, and a zero
	// value simply means the verdict was synthesized rather than derived.
	Evidence Evidence
}

func (e *AgentError) Error() string {
	msg := fmt.Sprintf("agenterr: [%s] %s: %s", e.Class, e.Backend, e.Message)
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	// Appending the evidence summary here is what makes the existing
	// supervisor log line ("[daemon] Agent %s: classified error: %v",
	// supervisor/classify.go:56) diagnosable with no change at that site.
	if s := e.Evidence.Summary(); s != "" {
		msg += " [evidence: " + s + "]"
	}
	return msg
}
