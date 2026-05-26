package agenterr

// ErrorClass categorizes agent subprocess failures.
type ErrorClass int

const (
	RateLimited        ErrorClass = iota // API rate limit / 429 / overloaded
	AuthFailure                          // Invalid API key / 401 / unauthorized
	BillingError                         // Payment required / 402 / quota exceeded
	ModelNotFound                        // Model does not exist / 404
	ContextOverflow                      // Context length exceeded / token limit
	Timeout                              // Connection timeout / ETIMEDOUT / SIGKILL
	Transient                            // Server error / 5xx / SIGTERM
	NoWork                               // No claimable tasks available
	SpawnFailure                         // Supervisor could not start the agent subprocess
	BackendUnavailable                   // Backend CLI binary not on PATH (recoverable on install)
	Unknown                              // Unclassifiable error
)

func (c ErrorClass) String() string {
	switch c {
	case RateLimited:
		return "RateLimited"
	case AuthFailure:
		return "AuthFailure"
	case BillingError:
		return "BillingError"
	case ModelNotFound:
		return "ModelNotFound"
	case ContextOverflow:
		return "ContextOverflow"
	case Timeout:
		return "Timeout"
	case Transient:
		return "Transient"
	case NoWork:
		return "NoWork"
	case SpawnFailure:
		return "SpawnFailure"
	case BackendUnavailable:
		return "BackendUnavailable"
	case Unknown:
		return "Unknown"
	default:
		return "Unknown"
	}
}

// IsRetryable returns true if the error class is worth retrying.
func (c ErrorClass) IsRetryable() bool {
	switch c {
	case RateLimited, Timeout, Transient, SpawnFailure:
		return true
	default:
		return false
	}
}

// IsFatal returns true if the error class indicates a permanent failure
// that should stop the agent without retrying.
func (c ErrorClass) IsFatal() bool {
	switch c {
	case AuthFailure, BillingError:
		return true
	default:
		return false
	}
}
