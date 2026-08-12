package driver

// Test-only legacy await-event driver-op logic (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW9).
//
// AwaitEvent is the server-side flow behind POST
// /api/workspaces/{ws}/driver/events/await: validate (RULES 1/3/5), then ONE
// store.AwaitStore.RegisterAwaitAndCheck call (RULE 2 — never a separate
// read-then-register), and either return the recorded event inline (satisfied,
// including idempotent replay of a finished await) or suspend the run with
// the caller's fenced owner credentials so only the live executor can suspend
// its run.
//
// The instance key is derived server-side from the AUTHENTICATED run identity
// (runID#await-{awaitIndex}); a workflow can never forge another run's await
// instance (RULE 3, server-derived).

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

// AwaitEventOptions carries one events/await driver-op invocation. RunID,
// NodeID, LeaseID and FencingToken are the VERIFIED owner identity of the
// calling run (the driver headers), never request-body data.
type AwaitEventOptions struct {
	WorkspaceKey string
	RunID        string
	NodeID       string
	LeaseID      string
	FencingToken int64

	// Pattern is the fully rendered subject-scoped key to wait for (RULE 1,
	// exact equality only).
	Pattern string
	// ActorAllow is the normalized eligible-actor predicate (RULE 4).
	ActorAllow []string
	// TimeoutMs is the mandatory await timeout (RULE 5), bounded by
	// MaxTimeout.
	TimeoutMs int64
	// AwaitIndex is the 1-based ordinal of this await within the run; the
	// instance key is derived as runID#await-{awaitIndex}.
	AwaitIndex int

	// MaxTimeout overrides the env/default timeout bound (tests). Zero means
	// AwaitMaxTimeoutEnvVar, falling back to DefaultAwaitMaxTimeout.
	MaxTimeout time.Duration
	// MaxPerRun overrides the env/default per-run await budget (tests). Zero
	// means AwaitMaxPerRunEnvVar, falling back to DefaultAwaitMaxPerRun.
	MaxPerRun int
	// TotalSuspendCap overrides the env/default total-suspend cap (tests).
	// Zero means AwaitTotalSuspendCapEnvVar, falling back to
	// DefaultAwaitTotalSuspendCap.
	TotalSuspendCap time.Duration
}

// AwaitEventOutcome is the op result. Status is AwaitOutcomeSuspended or the
// terminal await status (string(domain.AwaitSatisfied) / AwaitTimedOut);
// Instance is the persisted row (terminal with its recorded event and inline
// resume payload, or the pending row when suspended); Run is the suspended
// run when Status is AwaitOutcomeSuspended.
type AwaitEventOutcome struct {
	Status   string
	Instance *domain.AwaitInstance
	Run      *domain.DriverRun
}

// AwaitEvent runs the register-and-check flow for one await of the verified run.
// On a pending registration it suspends the run; a resolution that wins the
// accepted pending->suspend window (domain.ErrDriverRunAlreadyResumed, or a
// terminal row observed by the post-suspend recheck) is never lost.
func AwaitEvent(ctx context.Context, st store.Store, opts AwaitEventOptions) (*AwaitEventOutcome, error) {
	res, instanceKey, err := registerAwait(ctx, st, opts)
	if err != nil {
		return nil, err
	}
	if res.Satisfied {
		return &AwaitEventOutcome{Status: string(res.Instance.Status), Instance: res.Instance}, nil
	}
	return suspendForAwait(ctx, st, opts, instanceKey, res.Instance)
}

// registerAwait owns the validation, budget, instance identity, and one
// atomic registration call shared by the generic event-await and the
// composition-specific terminal-child recheck.
func registerAwait(ctx context.Context, st store.Store, opts AwaitEventOptions) (*store.AwaitResult, string, error) {
	if opts.AwaitIndex < 1 {
		return nil, "", fmt.Errorf("awaitIndex %d must be >= 1: %w", opts.AwaitIndex, domain.ErrAwaitInstanceKeyMalformed)
	}
	if err := domain.ValidateAwaitPattern(opts.Pattern); err != nil {
		return nil, "", err
	}
	timeout, err := awaitTimeout(opts.TimeoutMs, opts.maxTimeout())
	if err != nil {
		return nil, "", err
	}
	if err := enforceAwaitBudget(ctx, st, opts, timeout); err != nil {
		return nil, "", err
	}
	instanceKey := domain.AwaitInstanceKey(opts.RunID, opts.AwaitIndex)
	// RULE 2: one atomic registration scan + pending row. Idempotent on the
	// instance key: a finished await replays its recorded event (RULE 3).
	res, err := st.Awaits().RegisterAwaitAndCheck(ctx, opts.WorkspaceKey, store.AwaitRegistration{
		InstanceKey: instanceKey,
		RunID:       opts.RunID,
		Pattern:     opts.Pattern,
		ActorAllow:  opts.ActorAllow,
		Deadline:    time.Now().UTC().Add(timeout),
	})
	if err != nil {
		return nil, "", fmt.Errorf("register await %s: %w", instanceKey, err)
	}
	return res, instanceKey, nil
}

// suspendForAwait is the pending leg: fenced suspend, tolerating a resolution
// that won the accepted pending->suspend window.
func suspendForAwait(ctx context.Context, st store.Store, opts AwaitEventOptions, instanceKey string, pending *domain.AwaitInstance) (*AwaitEventOutcome, error) {
	run, err := st.DriverRuns().Suspend(ctx, opts.WorkspaceKey, opts.RunID,
		opts.NodeID, opts.LeaseID, opts.FencingToken, instanceKey)
	if errors.Is(err, domain.ErrDriverRunAlreadyResumed) {
		// The await resolved between registration and suspend and the backend
		// recorded a pending-resume marker (fleet-db, AW3): do not suspend —
		// continue inline with the recorded event.
		inst, replayErr := st.Awaits().GetSatisfiedAwait(ctx, opts.WorkspaceKey, instanceKey)
		if replayErr != nil {
			return nil, fmt.Errorf("replay await %s resolved in pending->suspend window: %w", instanceKey, replayErr)
		}
		return &AwaitEventOutcome{Status: string(inst.Status), Instance: inst}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("suspend driver run %s for await %s: %w", opts.RunID, instanceKey, err)
	}
	closePendingSuspendWindow(ctx, st, opts.WorkspaceKey, opts.RunID, instanceKey)
	return &AwaitEventOutcome{Status: AwaitOutcomeSuspended, Instance: pending, Run: run}, nil
}

// closePendingSuspendWindow re-checks the await after the suspend committed. On
// backends without a pending-resume marker (memstore) a resolver that ran
// inside the pending->suspend window saw a still-running run, tolerated
// ErrInvalidTransition and moved on — so the now-suspended run would sleep
// until its deadline. Any resolution committed before our suspend is visible
// here (the suspend already landed), so a tolerant resume closes the hole.
// Best-effort: failures leave the deadline sweeper as the backstop, and the
// op still reports suspended — the runner exits and the re-queued run is
// claimed normally.
func closePendingSuspendWindow(ctx context.Context, st store.Store, ws, runID, instanceKey string) {
	inst, err := st.Awaits().GetSatisfiedAwait(ctx, ws, instanceKey)
	if err != nil {
		return // still pending (ErrNotFound) or transient read failure
	}
	// Racing the resolver's own retry is fine: exactly one resume wins and
	// the loser's ErrInvalidTransition is tolerated by contract.
	_, _ = st.DriverRuns().ResumeAwaiting(ctx, ws, runID, instanceKey, inst.SatisfiedByEventID)
}

// enforceAwaitBudget applies the locked per-run limits (AW8) BEFORE
// registration so a run can never suspend beyond its budget: the await-count
// budget (awaitIndex bounded) and the total-suspend cap (time actually spent
// suspended on prior awaits plus the requested timeout). Replays re-pass
// deterministically under the same configuration: awaits 1..n-1 are terminal
// before await n registers, so the accumulated sum is stable.
func enforceAwaitBudget(ctx context.Context, st store.Store, opts AwaitEventOptions, timeout time.Duration) error {
	if budget := opts.maxPerRun(); opts.AwaitIndex > budget {
		return fmt.Errorf("awaitIndex %d exceeds the per-run await budget %d: %w",
			opts.AwaitIndex, budget, domain.ErrInvalid)
	}
	suspendCap := opts.totalSuspendCap()
	prior, err := priorSuspendedTime(ctx, st, opts)
	if err != nil {
		return err
	}
	if prior+timeout > suspendCap {
		return fmt.Errorf("await timeout %s plus %s already spent suspended exceeds the total-suspend cap %s for run %s: %w",
			timeout, prior, suspendCap, opts.RunID, domain.ErrAwaitTimeoutRequired)
	}
	return nil
}

// priorSuspendedTime sums the run's recorded suspended intervals across its
// prior awaits (RegisteredAt -> ResumedAt on terminal rows; an immediate
// satisfaction never sets ResumedAt and costs zero). Indices are dense (RULE
// 3), so the probe is exactly awaitIndex-1 point reads.
func priorSuspendedTime(ctx context.Context, st store.Store, opts AwaitEventOptions) (time.Duration, error) {
	var total time.Duration
	for n := 1; n < opts.AwaitIndex; n++ {
		inst, err := st.Awaits().GetSatisfiedAwait(ctx, opts.WorkspaceKey, domain.AwaitInstanceKey(opts.RunID, n))
		if errors.Is(err, domain.ErrNotFound) {
			continue // cancelled prior await: no recorded suspended interval
		}
		if err != nil {
			return 0, fmt.Errorf("read prior await %d for the total-suspend cap: %w", n, err)
		}
		if inst.ResumedAt != nil && inst.ResumedAt.After(inst.RegisteredAt) {
			total += inst.ResumedAt.Sub(inst.RegisteredAt)
		}
	}
	return total, nil
}

// awaitTimeout enforces RULE 5 on the wire field: required, positive,
// bounded.
func awaitTimeout(timeoutMs int64, maxTimeout time.Duration) (time.Duration, error) {
	if timeoutMs <= 0 {
		return 0, fmt.Errorf("timeoutMs is required and must be positive: %w", domain.ErrAwaitTimeoutRequired)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout > maxTimeout {
		return 0, fmt.Errorf("timeoutMs %d exceeds the maximum await timeout %s: %w",
			timeoutMs, maxTimeout, domain.ErrAwaitTimeoutRequired)
	}
	return timeout, nil
}

// maxTimeout resolves the effective timeout bound: explicit option, env
// override, default.
func (o AwaitEventOptions) maxTimeout() time.Duration {
	if o.MaxTimeout > 0 {
		return o.MaxTimeout
	}
	if raw := os.Getenv(AwaitMaxTimeoutEnvVar); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return DefaultAwaitMaxTimeout
}

// maxPerRun resolves the effective per-run await budget: explicit option,
// env override, default.
func (o AwaitEventOptions) maxPerRun() int {
	if o.MaxPerRun > 0 {
		return o.MaxPerRun
	}
	if raw := os.Getenv(AwaitMaxPerRunEnvVar); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return DefaultAwaitMaxPerRun
}

// totalSuspendCap resolves the effective total-suspend cap: explicit option,
// env override, default.
func (o AwaitEventOptions) totalSuspendCap() time.Duration {
	if o.TotalSuspendCap > 0 {
		return o.TotalSuspendCap
	}
	if raw := os.Getenv(AwaitTotalSuspendCapEnvVar); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return DefaultAwaitTotalSuspendCap
}
