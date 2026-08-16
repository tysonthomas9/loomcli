package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const (
	retryTestBinding = "binding-retry"
	retryTestEvent   = "event-retry"
)

// newRetryTestStore seeds the router fixture plus one binding with a small
// retry budget (3 attempts), mirroring fleet-db's seedRetryFixture so the
// two backends share scenario names.
func newRetryTestStore(t *testing.T) *Store {
	t.Helper()
	s := newRouterTestStore(t)
	in := routerBindingCreate(retryTestBinding)
	in.SourceKind = "webhook"
	in.RouteKey = "webhook.retry"
	in.RetryMaxAttempts = 3
	if _, err := s.TriggerBindings().Create(t.Context(), in); err != nil {
		t.Fatalf("Create binding: %v", err)
	}
	return s
}

func newRetryTestDelivery(deliveryID string, status automation.DeliveryStatus, attempt int, nextRetryAt *time.Time) *automation.Delivery {
	now := time.Now().UTC()
	return &automation.Delivery{
		WorkspaceKey:     "WS",
		DeliveryID:       deliveryID,
		TriggerEventID:   retryTestEvent,
		TriggerBindingID: retryTestBinding,
		SubjectKey:       retryTestBinding + "|WS-1",
		Status:           status,
		Attempt:          attempt,
		NextRetryAt:      nextRetryAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// dueIDs lists the due deliveries at now as bare ids, the memstore twin of
// fleet-db's ZSET-membership assertions.
func dueIDs(t *testing.T, s *Store, now time.Time) []string {
	t.Helper()
	due, err := s.TriggerDeliveries().ListDue(t.Context(), "WS", automation.TriggerDeliveryDueFilter{Now: now})
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	ids := make([]string, 0, len(due))
	for _, d := range due {
		ids = append(ids, d.DeliveryID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestMemTriggerDeliveryDueMembership pins which statuses count as
// retry-sweeper work, sharing scenario names with fleet-db's
// TestRedisCreateTriggerDeliveryDueIndex.
func TestMemTriggerDeliveryDueMembership(t *testing.T) {
	retryAt := time.Now().UTC().Add(30 * time.Second).Truncate(time.Second)
	exhausted := newRetryTestDelivery("d-exhausted", automation.DeliveryFailed, 3, nil)
	exhausted.ErrorClass = automation.TriggerDeliveryErrorRetriesExhausted

	tests := []struct {
		name     string
		delivery *automation.Delivery
		wantDue  bool
	}{
		{"failed with next_retry_at", newRetryTestDelivery("d-failed", automation.DeliveryFailed, 1, &retryAt), true},
		{"failed nil retry is immediately due", newRetryTestDelivery("d-failed-now", automation.DeliveryFailed, 1, nil), true},
		{"held enters index", newRetryTestDelivery("d-held", automation.DeliveryHeld, 1, &retryAt), true},
		{"failed retries_exhausted stays out", exhausted, false},
		{"dispatched stays out", newRetryTestDelivery("d-dispatched", automation.DeliveryDispatched, 1, nil), false},
		{"rejected stays out", newRetryTestDelivery("d-rejected", automation.DeliveryRejected, 1, nil), false},
		{"superseded stays out", newRetryTestDelivery("d-superseded", automation.DeliverySuperseded, 1, nil), false},
		{"accepted stays out", newRetryTestDelivery("d-accepted", automation.DeliveryAccepted, 1, nil), false},
	}
	s := newRetryTestStore(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.deliveries.create(tt.delivery); err != nil {
				t.Fatalf("create delivery: %v", err)
			}
			ids := dueIDs(t, s, time.Now().UTC().Add(time.Hour))
			if got := containsID(ids, tt.delivery.DeliveryID); got != tt.wantDue {
				t.Fatalf("due membership = %v, want %v (due = %v)", got, tt.wantDue, ids)
			}
		})
	}
}

// TestMemListDueTriggerDeliveries pins due ordering, the now cutoff and the
// limit, sharing scenario names with fleet-db's
// TestRedisListDueTriggerDeliveries.
func TestMemListDueTriggerDeliveries(t *testing.T) {
	s := newRetryTestStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) *time.Time {
		v := base.Add(d)
		return &v
	}

	// Created out of due order on purpose; d-now (nil retry) scores 0.
	for _, d := range []*automation.Delivery{
		newRetryTestDelivery("d-late", automation.DeliveryFailed, 1, at(2*time.Minute)),
		newRetryTestDelivery("d-now", automation.DeliveryFailed, 1, nil),
		newRetryTestDelivery("d-soon", automation.DeliveryHeld, 1, at(30*time.Second)),
		newRetryTestDelivery("d-dispatched", automation.DeliveryDispatched, 1, nil),
	} {
		if err := s.deliveries.create(d); err != nil {
			t.Fatalf("create delivery %s: %v", d.DeliveryID, err)
		}
	}

	tests := []struct {
		name   string
		filter automation.TriggerDeliveryDueFilter
		want   []string
	}{
		{"cutoff before soon", automation.TriggerDeliveryDueFilter{Now: base}, []string{"d-now"}},
		{"cutoff after soon", automation.TriggerDeliveryDueFilter{Now: base.Add(time.Minute)}, []string{"d-now", "d-soon"}},
		{"cutoff after all in due order", automation.TriggerDeliveryDueFilter{Now: base.Add(time.Hour)}, []string{"d-now", "d-soon", "d-late"}},
		{"limit truncates in due order", automation.TriggerDeliveryDueFilter{Now: base.Add(time.Hour), Limit: 2}, []string{"d-now", "d-soon"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.TriggerDeliveries().ListDue(t.Context(), "WS", tt.filter)
			if err != nil {
				t.Fatalf("ListDue: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d (%+v), want %d", len(got), got, len(tt.want))
			}
			for i, want := range tt.want {
				if got[i].DeliveryID != want {
					t.Fatalf("got[%d] = %s, want %s", i, got[i].DeliveryID, want)
				}
			}
		})
	}
}

// TestMemUpdateTriggerDeliveryResult pins the result-update state machine,
// sharing scenario names with fleet-db's TestRedisUpdateTriggerDeliveryResult:
// reschedules keep the delivery sweeper-visible with a fresh due time,
// success and supersede remove it, exhaustion forces the terminal
// failed/retries_exhausted shape, and final deliveries reject transitions.
func TestMemUpdateTriggerDeliveryResult(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) *time.Time {
		v := base.Add(d)
		return &v
	}
	terminal := func(id string) *automation.Delivery {
		d := newRetryTestDelivery(id, automation.DeliveryFailed, 3, nil)
		d.ErrorClass = automation.TriggerDeliveryErrorRetriesExhausted
		return d
	}

	tests := []struct {
		name           string
		initial        *automation.Delivery
		update         automation.TriggerDeliveryResultUpdate
		wantErr        error
		wantStatus     automation.DeliveryStatus
		wantAttempt    int
		wantErrorClass string
		wantRunID      string
		wantDue        bool
		wantRetryAt    *time.Time
	}{
		{
			name:        "failed reschedules with new score",
			initial:     newRetryTestDelivery("d-1", automation.DeliveryFailed, 1, at(30*time.Second)),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 2, NextRetryAt: at(2 * time.Minute), ErrorClass: "admission_failed"},
			wantStatus:  automation.DeliveryFailed,
			wantAttempt: 2, wantErrorClass: "admission_failed",
			wantDue: true, wantRetryAt: at(2 * time.Minute),
		},
		{
			name:        "accepted moves into index on failure",
			initial:     newRetryTestDelivery("d-2", automation.DeliveryAccepted, 1, nil),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 1, NextRetryAt: at(time.Minute)},
			wantStatus:  automation.DeliveryFailed,
			wantAttempt: 1,
			wantDue:     true, wantRetryAt: at(time.Minute),
		},
		{
			name:        "dispatch removes from index and stamps run",
			initial:     newRetryTestDelivery("d-3", automation.DeliveryFailed, 2, at(30*time.Second)),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryDispatched, Attempt: 3, DriverRunID: "run-retry-1"},
			wantStatus:  automation.DeliveryDispatched,
			wantAttempt: 3, wantRunID: "run-retry-1",
		},
		{
			name:        "held promotion to dispatched removes from index",
			initial:     newRetryTestDelivery("d-4", automation.DeliveryHeld, 1, at(30*time.Second)),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryDispatched, Attempt: 1, DriverRunID: "run-retry-2"},
			wantStatus:  automation.DeliveryDispatched,
			wantAttempt: 1, wantRunID: "run-retry-2",
		},
		{
			name:        "supersede removes from index",
			initial:     newRetryTestDelivery("d-5", automation.DeliveryHeld, 1, nil),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliverySuperseded, Attempt: 1},
			wantStatus:  automation.DeliverySuperseded,
			wantAttempt: 1,
		},
		{
			name:        "attempt at binding budget is terminal retries_exhausted",
			initial:     newRetryTestDelivery("d-6", automation.DeliveryFailed, 2, at(30*time.Second)),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 3, NextRetryAt: at(time.Minute), ErrorClass: "admission_failed"},
			wantStatus:  automation.DeliveryFailed,
			wantAttempt: 3, wantErrorClass: automation.TriggerDeliveryErrorRetriesExhausted,
		},
		{
			name:    "terminal failed rejects retry transition",
			initial: terminal("d-7"),
			update:  automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryDispatched, Attempt: 4},
			wantErr: persistence.ErrInvalidTransition,
		},
		{
			name:        "dispatched re-applied idempotently",
			initial:     newRetryTestDelivery("d-8", automation.DeliveryDispatched, 2, nil),
			update:      automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryDispatched, Attempt: 2, DriverRunID: "run-retry-3"},
			wantStatus:  automation.DeliveryDispatched,
			wantAttempt: 2, wantRunID: "run-retry-3",
		},
		{
			name:    "dispatched rejects move back to failed",
			initial: newRetryTestDelivery("d-9", automation.DeliveryDispatched, 1, nil),
			update:  automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 2, NextRetryAt: at(time.Minute)},
			wantErr: persistence.ErrInvalidTransition,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRetryTestStore(t)
			if err := s.deliveries.create(tt.initial); err != nil {
				t.Fatalf("create delivery: %v", err)
			}
			got, err := s.TriggerDeliveries().UpdateResult(t.Context(), "WS", tt.initial.DeliveryID, tt.update)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateResult: %v", err)
			}
			if got.Status != tt.wantStatus || got.Attempt != tt.wantAttempt {
				t.Fatalf("status/attempt = %s/%d, want %s/%d", got.Status, got.Attempt, tt.wantStatus, tt.wantAttempt)
			}
			if got.ErrorClass != tt.wantErrorClass {
				t.Fatalf("error_class = %q, want %q", got.ErrorClass, tt.wantErrorClass)
			}
			if tt.wantRunID != "" && got.DriverRunID != tt.wantRunID {
				t.Fatalf("driver_run_id = %q, want %q", got.DriverRunID, tt.wantRunID)
			}
			if tt.wantRetryAt == nil {
				if got.NextRetryAt != nil {
					t.Fatalf("next_retry_at = %v, want nil", got.NextRetryAt)
				}
			} else if got.NextRetryAt == nil || !got.NextRetryAt.Equal(*tt.wantRetryAt) {
				t.Fatalf("next_retry_at = %v, want %v", got.NextRetryAt, tt.wantRetryAt)
			}
			ids := dueIDs(t, s, base.Add(time.Hour))
			if due := containsID(ids, tt.initial.DeliveryID); due != tt.wantDue {
				t.Fatalf("due membership = %v, want %v (due = %v)", due, tt.wantDue, ids)
			}
			// Round-trip: the stored row must agree with the returned one.
			stored, err := s.TriggerDeliveries().Get(t.Context(), "WS", tt.initial.DeliveryID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.Status != got.Status || stored.Attempt != got.Attempt || stored.ErrorClass != got.ErrorClass {
				t.Fatalf("stored = %+v, want %+v", stored, got)
			}
		})
	}
}

// TestMemUpdateTriggerDeliveryResult_Validation pins the error surface and
// the vanished-binding fallback, mirroring fleet-db's
// TestRedisUpdateTriggerDeliveryResult_Validation.
func TestMemUpdateTriggerDeliveryResult_Validation(t *testing.T) {
	retryAt := time.Now().UTC().Add(time.Minute).Truncate(time.Second)

	t.Run("missing delivery wraps not found", func(t *testing.T) {
		s := newRetryTestStore(t)
		if _, err := s.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-missing", automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed}); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		s := newRetryTestStore(t)
		if err := s.deliveries.create(newRetryTestDelivery("d-bad", automation.DeliveryFailed, 1, nil)); err != nil {
			t.Fatalf("create delivery: %v", err)
		}
		if _, err := s.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-bad", automation.TriggerDeliveryResultUpdate{Status: "exploded"}); !errors.Is(err, persistence.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("vanished binding falls back to default budget", func(t *testing.T) {
		s := newRetryTestStore(t)
		d := newRetryTestDelivery("d-orphan", automation.DeliveryFailed, 3, nil)
		d.TriggerBindingID = "binding-vanished"
		if err := s.deliveries.create(d); err != nil {
			t.Fatalf("create delivery: %v", err)
		}
		// Attempt 4 < default budget 5: still retryable, not exhausted.
		got, err := s.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-orphan", automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 4, NextRetryAt: &retryAt, ErrorClass: "admission_failed"})
		if err != nil {
			t.Fatalf("UpdateResult: %v", err)
		}
		if got.ErrorClass != "admission_failed" || got.NextRetryAt == nil {
			t.Fatalf("delivery = %+v, want retryable below the %d-attempt default", got, automation.DefaultTriggerRetryMaxAttempts)
		}
		// Attempt 5 hits the default budget: terminal.
		got, err = s.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-orphan", automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed, Attempt: 5, NextRetryAt: &retryAt, ErrorClass: "admission_failed"})
		if err != nil {
			t.Fatalf("UpdateResult: %v", err)
		}
		if got.ErrorClass != automation.TriggerDeliveryErrorRetriesExhausted || got.NextRetryAt != nil {
			t.Fatalf("delivery = %+v, want terminal retries_exhausted at the default budget", got)
		}
	})
}
