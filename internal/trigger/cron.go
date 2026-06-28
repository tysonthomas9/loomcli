package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Cron event-source constants. The scheduler fires synthetic cron.tick events
// for bindings with source_kind=cron into the normal trigger-route dispatch
// path; downstream routing, fan-out and idempotency behave exactly as for an
// external webhook event.
const (
	// CronSourceKind is the TriggerBinding source kind the scheduler sweeps.
	CronSourceKind = "cron"
	// CronEventType is the event type stamped on scheduler-fired events.
	CronEventType = "cron.tick"
	// CronActorRef identifies the scheduler as the acting principal.
	CronActorRef = "system:cron"
)

// ErrInvalidSchedule is the domain sentinel returned (wrapped) for malformed
// cron schedules and schedule timezones.
var ErrInvalidSchedule = errors.New("invalid cron schedule")

// ValidateSchedule checks a binding schedule against the standard 5-field
// cron grammar (plus @descriptors such as @hourly), mirroring fleet-db's
// ValidateTriggerSchedule. Errors wrap ErrInvalidSchedule.
func ValidateSchedule(schedule string) error {
	_, err := parseCronSchedule(schedule)
	return err
}

// ValidateScheduleTimezone checks an IANA timezone name (e.g.
// "Europe/Berlin"). Empty means UTC and is valid. Errors wrap
// ErrInvalidSchedule.
func ValidateScheduleTimezone(tz string) error {
	_, err := loadScheduleLocation(tz)
	return err
}

// parseCronSchedule parses with the same grammar fleet-db validates at
// binding-write time (cron.ParseStandard: 5-field plus @descriptors), so a
// schedule accepted by the API is always parseable here.
func parseCronSchedule(schedule string) (cron.Schedule, error) {
	sched, err := cron.ParseStandard(schedule)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrInvalidSchedule, schedule, err)
	}
	return sched, nil
}

func loadScheduleLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("%w: timezone %q: %v", ErrInvalidSchedule, tz, err)
	}
	return loc, nil
}

// CronScheduler is the built-in cron event source for `loom serve`. Each
// RunOnce sweep lists enabled source_kind=cron bindings and, per binding,
// fires at most one due tick from the window (lastTick, now] into the normal
// trigger-route dispatch path (persist-and-enqueue only — no workflow code
// runs here).
//
// Missed-tick policy (locked decision): at most 1 catch-up tick per binding
// per sweep — when several ticks were missed, the earliest due one fires and
// the rest are dropped, never backfilled.
//
// The tick idempotency key is cron:{bindingID}:{fireUnix}, so duplicate or
// overlapping schedulers (e.g. two serve replicas) are safe: the event dedups
// on the key via the store's SetNX-style create and the run/delivery legs
// dedup on their own deterministic ids.
//
// The zero value plus Store is ready to use; a nil-window binding (first
// observation) only primes its window and never fires historical ticks.
type CronScheduler struct {
	Store store.Store
	// WorkspaceKey scopes the sweep to one workspace. Empty sweeps every
	// known workspace (mirrors OutboxDispatcher/StaleTaskSweeper).
	WorkspaceKey string

	mu sync.Mutex
	// lastTick is the per-binding window start, keyed ws|bindingID. It only
	// advances when a due tick dispatches successfully, so a failed dispatch
	// is retried (idempotently) on the next sweep.
	lastTick map[string]time.Time
}

// CronSweepResult summarizes one RunOnce sweep.
type CronSweepResult struct {
	// Fired counts bindings whose due tick dispatched this sweep.
	Fired int
	// Skipped counts bindings passed over for an error (bad schedule or
	// timezone, missing route key, dispatch failure); details are in the
	// joined error returned alongside.
	Skipped int
}

// RunOnce performs one scheduler sweep at the given wall-clock instant. It
// keeps going past per-binding errors and returns them joined; a non-nil
// result is returned even when some bindings errored.
func (s *CronScheduler) RunOnce(ctx context.Context, now time.Time) (*CronSweepResult, error) {
	if s.Store == nil {
		return nil, errors.New("cron scheduler: Store is required")
	}
	workspaces, err := s.workspaceKeys(ctx)
	if err != nil {
		return nil, err
	}
	result := &CronSweepResult{}
	var errs []error
	enabled := true
	for _, ws := range workspaces {
		bindings, err := s.Store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{
			SourceKind: CronSourceKind,
			Enabled:    &enabled,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("list cron bindings in workspace %q: %w", ws, err))
			continue
		}
		for _, binding := range bindings {
			fired, err := s.sweepBinding(ctx, ws, binding, now)
			if err != nil {
				result.Skipped++
				errs = append(errs, err)
				continue
			}
			if fired {
				result.Fired++
			}
		}
	}
	return result, errors.Join(errs...)
}

// workspaceKeys resolves the sweep targets: the configured workspace, or
// every known workspace when unscoped (mirrors OutboxDispatcher).
func (s *CronScheduler) workspaceKeys(ctx context.Context) ([]string, error) {
	if s.WorkspaceKey != "" {
		return []string{s.WorkspaceKey}, nil
	}
	workspaces, err := s.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for cron sweep: %w", err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		keys = append(keys, ws.Key)
	}
	return keys, nil
}

// sweepBinding fires the binding's due tick, if any. First observation of a
// binding primes its window at now without firing (no historical backfill on
// startup); after that, the earliest schedule time in (lastTick, now] fires
// and the window jumps to now, collapsing any further missed ticks (at most
// 1 catch-up per sweep).
func (s *CronScheduler) sweepBinding(ctx context.Context, ws string, binding *domain.TriggerBinding, now time.Time) (bool, error) {
	if binding == nil || !binding.Enabled || binding.SourceKind != CronSourceKind {
		// Defensive re-check of the list filter; never fire for disabled or
		// non-cron bindings.
		return false, nil
	}
	sched, err := parseCronSchedule(binding.Schedule)
	if err != nil {
		return false, fmt.Errorf("cron binding %q in workspace %q: %w", binding.BindingID, ws, err)
	}
	loc, err := loadScheduleLocation(binding.ScheduleTimezone)
	if err != nil {
		return false, fmt.Errorf("cron binding %q in workspace %q: %w", binding.BindingID, ws, err)
	}
	key := ws + "|" + binding.BindingID
	windowStart, primed := s.primeWindow(key, now)
	if primed {
		return false, nil
	}
	fire := sched.Next(windowStart.In(loc))
	if fire.After(now) {
		return false, nil
	}
	if binding.RouteKey == "" {
		return false, fmt.Errorf("cron binding %q in workspace %q has no route key: %w", binding.BindingID, ws, domain.ErrInvalid)
	}
	if err := s.dispatchTick(ctx, ws, binding, fire); err != nil {
		return false, err
	}
	s.advanceWindow(key, now)
	return true, nil
}

// dispatchTick feeds one cron tick into the normal router path. Tick-level
// dedup rides the trigger-event idempotency key, so re-dispatch of the same
// fire instant (overlapping schedulers, retried sweeps) is a no-op replay.
func (s *CronScheduler) dispatchTick(ctx context.Context, ws string, binding *domain.TriggerBinding, fire time.Time) error {
	payload, err := json.Marshal(map[string]string{"tick": fire.UTC().Format(time.RFC3339)})
	if err != nil {
		return fmt.Errorf("encode cron tick payload for binding %q: %w", binding.BindingID, err)
	}
	tickKey := CronTickIdempotencyKey(binding.BindingID, fire)
	_, err = s.Store.TriggerRoutes().DispatchTriggerRouteV2(ctx, ws, binding.RouteKey, store.TriggerRouteDispatch{
		IdempotencyKey: tickKey,
		SourceEventID:  tickKey,
		EventType:      CronEventType,
		SubjectRef:     binding.BindingID,
		ActorRef:       CronActorRef,
		Payload:        payload,
	})
	if err != nil {
		return fmt.Errorf("dispatch cron tick for binding %q in workspace %q: %w", binding.BindingID, ws, err)
	}
	s.dispatchTickAwaits(ctx, ws, binding.BindingID, tickKey, payload)
	return nil
}

// dispatchTickAwaits hands the admitted tick to the dispatch-time await
// matcher (AW7): an await parked on "cron.tick:{bindingID}" resumes on the
// binding's next due tick. Best-effort — the tick already dispatched durably,
// so matcher errors are logged, never returned (the next sweep's idempotent
// re-dispatch retries naturally while the window has not advanced past it).
func (s *CronScheduler) dispatchTickAwaits(ctx context.Context, ws, bindingID, tickKey string, payload json.RawMessage) {
	matcher := &AwaitMatcher{Store: s.Store}
	if _, err := matcher.Dispatch(ctx, ws, AwaitDispatchEvent{
		EventID:    tickKey,
		EventType:  CronEventType,
		SubjectRef: bindingID,
		ActorRef:   CronActorRef,
		Payload:    payload,
	}); err != nil {
		slog.Default().Warn("cron tick await dispatch failed",
			"workspace", ws, "binding", bindingID, "tick", tickKey, "error", err)
	}
}

// CronTickIdempotencyKey is the deterministic per-tick dedup key:
// cron:{bindingID}:{fireUnix}. Exported so tests (and any future audit
// tooling) can derive the key a tick was dispatched under.
func CronTickIdempotencyKey(bindingID string, fire time.Time) string {
	return "cron:" + bindingID + ":" + strconv.FormatInt(fire.Unix(), 10)
}

// primeWindow returns the binding's window start. On first observation it
// records now and reports primed=true: the scheduler never fires schedule
// times from before it started watching a binding.
func (s *CronScheduler) primeWindow(key string, now time.Time) (start time.Time, primed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastTick == nil {
		s.lastTick = make(map[string]time.Time)
	}
	if start, ok := s.lastTick[key]; ok {
		return start, false
	}
	s.lastTick[key] = now
	return now, true
}

func (s *CronScheduler) advanceWindow(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTick[key] = now
}
