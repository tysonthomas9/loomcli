package driver

import (
	"os"
	"strconv"
	"time"
)

const (
	AwaitMaxTimeoutEnvVar       = "LOOM_AWAIT_MAX_TIMEOUT_MS"
	DefaultAwaitMaxTimeout      = 14 * 24 * time.Hour
	AwaitMaxPerRunEnvVar        = "LOOM_AWAIT_MAX_PER_RUN"
	DefaultAwaitMaxPerRun       = 100
	AwaitTotalSuspendCapEnvVar  = "LOOM_AWAIT_TOTAL_SUSPEND_CAP_MS"
	DefaultAwaitTotalSuspendCap = 30 * 24 * time.Hour
)

type AwaitLimits struct {
	MaxTimeout      time.Duration
	MaxPerRun       int
	TotalSuspendCap time.Duration
}

// AwaitOutcomeSuspended is retained as the stable driver wire value. Mutation
// and transition policy live in Execution.
const AwaitOutcomeSuspended = "suspended"

// ResolveAwaitLimits snapshots the shipped env/default await policy so the
// Execution command receives explicit configuration instead of reading host
// process environment from capability core.
func ResolveAwaitLimits() AwaitLimits {
	return AwaitLimits{
		MaxTimeout:      resolveAwaitMaxTimeout(),
		MaxPerRun:       resolveAwaitMaxPerRun(),
		TotalSuspendCap: resolveAwaitTotalSuspendCap(),
	}
}

func resolveAwaitMaxTimeout() time.Duration {
	if raw := os.Getenv(AwaitMaxTimeoutEnvVar); raw != "" {
		if milliseconds, err := strconv.ParseInt(raw, 10, 64); err == nil && milliseconds > 0 {
			return time.Duration(milliseconds) * time.Millisecond
		}
	}
	return DefaultAwaitMaxTimeout
}

func resolveAwaitMaxPerRun() int {
	if raw := os.Getenv(AwaitMaxPerRunEnvVar); raw != "" {
		if count, err := strconv.Atoi(raw); err == nil && count > 0 {
			return count
		}
	}
	return DefaultAwaitMaxPerRun
}

func resolveAwaitTotalSuspendCap() time.Duration {
	if raw := os.Getenv(AwaitTotalSuspendCapEnvVar); raw != "" {
		if milliseconds, err := strconv.ParseInt(raw, 10, 64); err == nil && milliseconds > 0 {
			return time.Duration(milliseconds) * time.Millisecond
		}
	}
	return DefaultAwaitTotalSuspendCap
}
