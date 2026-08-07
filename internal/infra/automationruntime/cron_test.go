// Cron scheduler tests live in the external trigger_test package so they can
// drive the real memstore dispatch path (memstore imports trigger for the
// pattern engine, so an internal test would be an import cycle).
package trigger_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestValidateSchedule(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		wantErr  bool
	}{
		{name: "every minute", schedule: "* * * * *"},
		{name: "every five minutes", schedule: "*/5 * * * *"},
		{name: "weekday mornings", schedule: "0 9 * * 1-5"},
		{name: "descriptor hourly", schedule: "@hourly"},
		{name: "descriptor daily", schedule: "@daily"},
		{name: "empty", schedule: "", wantErr: true},
		{name: "minute out of range", schedule: "61 * * * *", wantErr: true},
		{name: "too few fields", schedule: "* * *", wantErr: true},
		{name: "garbage", schedule: "not-a-cron", wantErr: true},
		{name: "six fields rejected", schedule: "0 0 9 * * 1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := trigger.ValidateSchedule(tt.schedule)
			if tt.wantErr {
				if !errors.Is(err, trigger.ErrInvalidSchedule) {
					t.Fatalf("ValidateSchedule(%q) = %v, want ErrInvalidSchedule", tt.schedule, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSchedule(%q) = %v, want nil", tt.schedule, err)
			}
		})
	}
}

func TestValidateScheduleTimezone(t *testing.T) {
	tests := []struct {
		name    string
		tz      string
		wantErr bool
	}{
		{name: "empty means UTC", tz: ""},
		{name: "utc", tz: "UTC"},
		{name: "iana zone", tz: "America/New_York"},
		{name: "unknown zone", tz: "Mars/Olympus_Mons", wantErr: true},
		{name: "garbage", tz: "not a zone", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := trigger.ValidateScheduleTimezone(tt.tz)
			if tt.wantErr {
				if !errors.Is(err, trigger.ErrInvalidSchedule) {
					t.Fatalf("ValidateScheduleTimezone(%q) = %v, want ErrInvalidSchedule", tt.tz, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateScheduleTimezone(%q) = %v, want nil", tt.tz, err)
			}
		})
	}
}

func TestNextFire(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		timezone string
		after    time.Time
		want     time.Time
		wantErr  bool
	}{
		{
			name:     "every five minutes rounds up in UTC",
			schedule: "*/5 * * * *",
			after:    time.Date(2026, 6, 11, 10, 2, 30, 0, time.UTC),
			want:     time.Date(2026, 6, 11, 10, 5, 0, 0, time.UTC),
		},
		{
			name:     "strictly after a boundary instant",
			schedule: "*/5 * * * *",
			after:    time.Date(2026, 6, 11, 10, 5, 0, 0, time.UTC),
			want:     time.Date(2026, 6, 11, 10, 10, 0, 0, time.UTC),
		},
		{
			name:     "daily nine am evaluated in America/New_York",
			schedule: "0 9 * * *",
			timezone: "America/New_York",
			after:    time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC), // 08:00 EDT
			want:     time.Date(2026, 6, 11, 13, 0, 0, 0, time.UTC), // 09:00 EDT
		},
		{
			name:     "invalid schedule",
			schedule: "not-a-cron",
			after:    time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC),
			wantErr:  true,
		},
		{
			name:     "invalid timezone",
			schedule: "* * * * *",
			timezone: "Mars/Olympus_Mons",
			after:    time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC),
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trigger.NextFire(tt.schedule, tt.timezone, tt.after)
			if tt.wantErr {
				if !errors.Is(err, trigger.ErrInvalidSchedule) {
					t.Fatalf("NextFire(%q, %q) err = %v, want ErrInvalidSchedule", tt.schedule, tt.timezone, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NextFire(%q, %q) err = %v, want nil", tt.schedule, tt.timezone, err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("NextFire(%q, %q, %s) = %s, want %s", tt.schedule, tt.timezone, tt.after.UTC(), got.UTC(), tt.want.UTC())
			}
			if !got.After(tt.after) {
				t.Fatalf("NextFire returned %s, want an instant strictly after %s", got.UTC(), tt.after.UTC())
			}
		})
	}
}

// cronBinding is one binding fixture row for the scheduler tests.
type cronBinding struct {
	bindingID  string
	sourceKind string
	routeKey   string
	schedule   string
	timezone   string
	disabled   bool
}

// setupCronBindings seeds a memstore with a driver, a version and the given
// bindings (mirrors the fan-out test fixtures).
func setupCronBindings(t *testing.T, s *memstore.Store, bindings []cronBinding) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "nightly-report", Name: "nightly-report",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "nightly-report", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	for _, b := range bindings {
		sourceKind := b.sourceKind
		if sourceKind == "" {
			sourceKind = trigger.CronSourceKind
		}
		if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey: "WS", BindingID: b.bindingID, Name: b.bindingID, SourceKind: sourceKind,
			RouteKey: b.routeKey, Schedule: b.schedule, ScheduleTimezone: b.timezone,
			DriverID: "nightly-report", DriverVersionID: "v1", TargetEntrypoint: "run",
			Enabled: !b.disabled,
		}); err != nil {
			t.Fatalf("Create trigger binding %s: %v", b.bindingID, err)
		}
	}
}

func cronStoreState(t *testing.T, s *memstore.Store) (events, runs, deliveries int) {
	t.Helper()
	ctx := t.Context()
	evs, err := s.TriggerEvents().List(ctx, "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	rns, err := s.DriverRuns().List(ctx, "WS", store.DriverRunFilter{})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	dels, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{})
	if err != nil {
		t.Fatalf("List deliveries: %v", err)
	}
	return len(evs), len(rns), len(dels)
}

func runSweep(t *testing.T, s *trigger.CronScheduler, now time.Time) *trigger.CronSweepResult {
	t.Helper()
	result, err := s.RunOnce(t.Context(), now)
	if err != nil {
		t.Fatalf("RunOnce(%s): %v", now, err)
	}
	return result
}

// TestCronSchedulerFiresDueTick covers the happy path with a fake clock: the
// first sweep primes the window without firing, a due tick then dispatches
// one cron.tick event through the normal router path, and a repeat sweep at
// the same instant fires nothing.
func TestCronSchedulerFiresDueTick(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-nightly", routeKey: "cron.nightly", schedule: "* * * * *"},
	})
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	if result := runSweep(t, scheduler, t0); result.Fired != 0 || result.Skipped != 0 {
		t.Fatalf("priming sweep = %+v, want nothing fired", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events+runs+deliveries != 0 {
		t.Fatalf("priming sweep wrote state: events=%d runs=%d deliveries=%d", events, runs, deliveries)
	}

	fire := time.Date(2026, 6, 11, 10, 1, 0, 0, time.UTC)
	t1 := t0.Add(45 * time.Second) // 10:01:15, one tick due at 10:01:00
	if result := runSweep(t, scheduler, t1); result.Fired != 1 {
		t.Fatalf("due sweep = %+v, want 1 fired", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("state after fire: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}

	evs, err := st.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	wantKey := trigger.CronTickIdempotencyKey("cron-nightly", fire)
	event := evs[0]
	if event.EventType != trigger.CronEventType {
		t.Fatalf("event type = %q, want %q", event.EventType, trigger.CronEventType)
	}
	if event.ActorRef != trigger.CronActorRef {
		t.Fatalf("actor ref = %q, want %q", event.ActorRef, trigger.CronActorRef)
	}
	if event.SubjectRef != "cron-nightly" {
		t.Fatalf("subject ref = %q, want the binding id", event.SubjectRef)
	}
	if event.IdempotencyKey != wantKey || event.SourceEventID != wantKey {
		t.Fatalf("idempotency/source = %q/%q, want %q", event.IdempotencyKey, event.SourceEventID, wantKey)
	}
	if event.SourceKind != trigger.CronSourceKind {
		t.Fatalf("source kind = %q, want cron", event.SourceKind)
	}

	// Same instant again: the window already advanced, nothing refires.
	if result := runSweep(t, scheduler, t1); result.Fired != 0 {
		t.Fatalf("repeat sweep = %+v, want nothing fired", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("repeat sweep duplicated state: events=%d runs=%d deliveries=%d", events, runs, deliveries)
	}
}

// TestCronSchedulerTickDedupAcrossSchedulers proves invariant 3: two
// schedulers sweeping the same window (overlapping serve replicas) both fire
// the tick, but the store-level idempotency key collapses them to one event,
// one run and one delivery.
func TestCronSchedulerTickDedupAcrossSchedulers(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-nightly", routeKey: "cron.nightly", schedule: "* * * * *"},
	})
	a := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}
	b := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	t1 := t0.Add(45 * time.Second)
	runSweep(t, a, t0)
	runSweep(t, b, t0)
	if result := runSweep(t, a, t1); result.Fired != 1 {
		t.Fatalf("scheduler A sweep = %+v, want 1 fired", result)
	}
	if result := runSweep(t, b, t1); result.Fired != 1 {
		t.Fatalf("scheduler B sweep = %+v, want 1 fired (dedup is store-level, not local)", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("overlapping schedulers duplicated state: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}
}

// TestCronSchedulerCatchUpClamp covers the missed-tick policy: a sweep that
// arrives after many missed every-minute ticks fires exactly one catch-up
// tick (the earliest due), never a backfill storm.
func TestCronSchedulerCatchUpClamp(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-minutely", routeKey: "cron.minutely", schedule: "* * * * *"},
	})
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	runSweep(t, scheduler, t0)

	// Ten ticks elapsed since the window opened; exactly one fires.
	late := t0.Add(10 * time.Minute)
	if result := runSweep(t, scheduler, late); result.Fired != 1 {
		t.Fatalf("late sweep = %+v, want exactly 1 catch-up tick", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("late sweep backfilled: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}
	// The catch-up tick is the earliest missed one (10:01:00).
	evs, err := st.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	wantKey := trigger.CronTickIdempotencyKey("cron-minutely", time.Date(2026, 6, 11, 10, 1, 0, 0, time.UTC))
	if evs[0].IdempotencyKey != wantKey {
		t.Fatalf("catch-up tick key = %q, want %q", evs[0].IdempotencyKey, wantKey)
	}

	// The window advanced to the sweep instant: the next due tick fires on
	// the next sweep, not another catch-up from the gap.
	if result := runSweep(t, scheduler, late.Add(time.Minute)); result.Fired != 1 {
		t.Fatalf("post-gap sweep = %+v, want 1 fired", result)
	}
	if events, _, _ := cronStoreState(t, st); events != 2 {
		t.Fatalf("events after post-gap sweep = %d, want 2", events)
	}
}

// TestCronSchedulerTimezone evaluates the schedule in the binding's IANA
// zone: a 9am America/New_York schedule fires at 13:00 UTC in June (EDT).
func TestCronSchedulerTimezone(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-ny", routeKey: "cron.ny", schedule: "0 9 * * *", timezone: "America/New_York"},
	})
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

	t0 := time.Date(2026, 6, 11, 12, 50, 0, 0, time.UTC) // 08:50 in New York
	runSweep(t, scheduler, t0)
	if result := runSweep(t, scheduler, time.Date(2026, 6, 11, 12, 59, 0, 0, time.UTC)); result.Fired != 0 {
		t.Fatalf("pre-9am-NY sweep = %+v, want nothing fired", result)
	}
	if result := runSweep(t, scheduler, time.Date(2026, 6, 11, 13, 0, 30, 0, time.UTC)); result.Fired != 1 {
		t.Fatalf("post-9am-NY sweep = %+v, want 1 fired", result)
	}
	evs, err := st.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	wantFire := time.Date(2026, 6, 11, 13, 0, 0, 0, time.UTC) // 09:00 EDT
	wantKey := trigger.CronTickIdempotencyKey("cron-ny", wantFire)
	if len(evs) != 1 || evs[0].IdempotencyKey != wantKey {
		t.Fatalf("events = %+v, want one tick keyed %q", evs, wantKey)
	}
}

// TestCronSchedulerSkipsDisabledAndNonCron: disabled cron bindings and
// enabled non-cron bindings never fire.
func TestCronSchedulerSkipsDisabledAndNonCron(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-off", routeKey: "cron.off", schedule: "* * * * *", disabled: true},
		{bindingID: "hook-github", sourceKind: "github", routeKey: "github.push", schedule: "* * * * *"},
	})
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	runSweep(t, scheduler, t0)
	if result := runSweep(t, scheduler, t0.Add(2*time.Minute)); result.Fired != 0 || result.Skipped != 0 {
		t.Fatalf("sweep = %+v, want nothing fired or skipped", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events+runs+deliveries != 0 {
		t.Fatalf("disabled/non-cron bindings fired: events=%d runs=%d deliveries=%d", events, runs, deliveries)
	}
}

// TestCronSchedulerInvalidBindings: malformed schedules and timezones are
// skipped with a wrapped ErrInvalidSchedule while healthy bindings in the
// same sweep still fire.
func TestCronSchedulerInvalidBindings(t *testing.T) {
	tests := []struct {
		name    string
		binding cronBinding
	}{
		{name: "bad schedule", binding: cronBinding{bindingID: "cron-bad-sched", routeKey: "cron.bad.sched", schedule: "not-a-cron"}},
		{name: "empty schedule", binding: cronBinding{bindingID: "cron-no-sched", routeKey: "cron.no.sched"}},
		{name: "bad timezone", binding: cronBinding{bindingID: "cron-bad-tz", routeKey: "cron.bad.tz", schedule: "* * * * *", timezone: "Mars/Olympus_Mons"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			setupCronBindings(t, st, []cronBinding{
				tt.binding,
				{bindingID: "cron-good", routeKey: "cron.good", schedule: "* * * * *"},
			})
			scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}

			t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
			// Even the priming sweep surfaces the validation error.
			if _, err := scheduler.RunOnce(t.Context(), t0); !errors.Is(err, trigger.ErrInvalidSchedule) {
				t.Fatalf("priming RunOnce error = %v, want ErrInvalidSchedule", err)
			}
			result, err := scheduler.RunOnce(t.Context(), t0.Add(2*time.Minute))
			if !errors.Is(err, trigger.ErrInvalidSchedule) {
				t.Fatalf("RunOnce error = %v, want ErrInvalidSchedule", err)
			}
			if result == nil || result.Fired != 1 || result.Skipped != 1 {
				t.Fatalf("result = %+v, want the healthy binding fired and the bad one skipped", result)
			}
			if events, _, _ := cronStoreState(t, st); events != 1 {
				t.Fatalf("events = %d, want only the healthy binding's tick", events)
			}
		})
	}
}

// TestCronSchedulerMissingRouteKey: a cron binding without a route key cannot
// feed the router; it is skipped with ErrInvalid and retried (not advanced).
// A cron binding created without an explicit route_key gets one derived from its
// binding_id by the store (TriggerBindingCreate.WithDerivedRoute), so it is a
// valid 1:1 routing address and the scheduler fires it normally. The old
// "keyless cron binding" the scheduler used to reject is no longer creatable
// through the store — the derivation is what lets two scheduled workflows coexist
// without a shared hand-picked route.
func TestCronSchedulerDerivesRouteKey(t *testing.T) {
	st := memstore.New()
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-derived", schedule: "* * * * *"}, // no routeKey supplied
	})
	b, err := st.TriggerBindings().Get(t.Context(), "WS", "cron-derived")
	if err != nil {
		t.Fatalf("Get binding: %v", err)
	}
	if b.RouteKey != "cron:cron-derived" {
		t.Fatalf("derived route_key = %q, want cron:cron-derived", b.RouteKey)
	}

	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}
	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	if result := runSweep(t, scheduler, t0); result.Fired != 0 || result.Skipped != 0 {
		t.Fatalf("priming sweep = %+v, want nothing fired", result)
	}
	t1 := t0.Add(45 * time.Second) // 10:01:15, one tick due at 10:01:00
	result := runSweep(t, scheduler, t1)
	if result.Fired != 1 || result.Skipped != 0 {
		t.Fatalf("due sweep = %+v, want 1 fired / 0 skipped (the derived route is valid)", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("state after fire: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}
}

// TestCronSchedulerUnscopedWorkspaces: an empty WorkspaceKey sweeps every
// known workspace (mirrors the outbox dispatcher's scoping).
func TestCronSchedulerUnscopedWorkspaces(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	setupCronBindings(t, st, []cronBinding{
		{bindingID: "cron-nightly", routeKey: "cron.nightly", schedule: "* * * * *"},
	})
	scheduler := &trigger.CronScheduler{Store: st}

	t0 := time.Date(2026, 6, 11, 10, 0, 30, 0, time.UTC)
	runSweep(t, scheduler, t0)
	if result := runSweep(t, scheduler, t0.Add(time.Minute)); result.Fired != 1 {
		t.Fatalf("unscoped sweep = %+v, want 1 fired", result)
	}
	if events, runs, deliveries := cronStoreState(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("state: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}
}
