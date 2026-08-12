package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

const (
	maxAwaitListProbe       = 1000
	awaitPendingScanLimit   = 10000
	awaitPendingScanHorizon = 24 * time.Hour * 365 * 10
)

// ListRunAwaits is the read-only re-entry projection. It accepts only the
// narrow AwaitStore query port; it cannot mutate DriverRun state.
func ListRunAwaits(ctx context.Context, awaits store.AwaitStore, workspace, runID string) ([]*domain.AwaitInstance, error) {
	due, err := awaits.ListDueAwaitDeadlines(ctx, workspace,
		time.Now().UTC().Add(awaitPendingScanHorizon), awaitPendingScanLimit)
	if err != nil {
		return nil, fmt.Errorf("scan pending awaits: %w", err)
	}
	pending := make(map[string]*domain.AwaitInstance)
	for _, instance := range due {
		if instance.RunID == runID {
			pending[instance.InstanceKey] = instance
		}
	}
	out := make([]*domain.AwaitInstance, 0)
	for index := 1; index <= maxAwaitListProbe; index++ {
		key := domain.AwaitInstanceKey(runID, index)
		if instance, ok := pending[key]; ok {
			out = append(out, instance)
			continue
		}
		instance, getErr := awaits.GetSatisfiedAwait(ctx, workspace, key)
		if errors.Is(getErr, domain.ErrNotFound) {
			break
		}
		if getErr != nil {
			return nil, fmt.Errorf("get await %s: %w", key, getErr)
		}
		out = append(out, instance)
	}
	return out, nil
}
