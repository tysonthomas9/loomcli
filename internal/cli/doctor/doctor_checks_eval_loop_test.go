package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestCheckEvalLoopNotProvisionedPasses(t *testing.T) {
	bindings := &evalLoopTestTriggerBindings{err: domain.ErrNotFound}
	runs := &evalLoopTestDriverRuns{}
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{bindings: bindings, runs: runs}, "TEST", time.Now())

	if result.Status != StatusPass || result.Summary != "evals not provisioned" {
		t.Fatalf("result = %+v, want informational not-provisioned pass", result)
	}
	if runs.called {
		t.Fatal("listed driver runs for an unprovisioned eval binding")
	}
}

func TestCheckEvalLoopDisabledPasses(t *testing.T) {
	bindings := &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)}
	bindings.binding.Enabled = false
	runs := &evalLoopTestDriverRuns{}
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{bindings: bindings, runs: runs}, "TEST", time.Now())

	if result.Status != StatusPass || result.Summary != "evals not provisioned" {
		t.Fatalf("result = %+v, want informational disabled pass", result)
	}
	if runs.called {
		t.Fatal("listed driver runs for a disabled eval binding")
	}
}

func TestCheckEvalLoopHealthyLatestRunPasses(t *testing.T) {
	now := time.Date(2026, time.January, 2, 10, 5, 0, 0, time.UTC)
	older := &domain.DriverRun{
		RunID:      "older-failed",
		Status:     domain.DriverRunFailed,
		ErrorClass: "judge_error",
		StartedAt:  now.Add(-4 * time.Minute),
	}
	latest := &domain.DriverRun{
		RunID:     "latest-completed",
		Status:    domain.DriverRunCompleted,
		StartedAt: now.Add(-time.Minute),
	}
	runs := &evalLoopTestDriverRuns{runs: []*domain.DriverRun{older, latest}}
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs:     runs,
	}, "TEST", now)

	if result.Status != StatusPass || result.Summary != "session eval loop healthy" {
		t.Fatalf("result = %+v, want healthy pass", result)
	}
	if !strings.Contains(result.Detail, "latest-completed") {
		t.Fatalf("detail = %q, want client-side latest run", result.Detail)
	}
	if !runs.called || runs.filter.DriverID != workflowdefs.BuiltinSessionEvalAgentWorkflowName || runs.filter.Limit != evalLoopDriverRunLimit {
		t.Fatalf("driver-run filter = %+v, want bounded session-eval-agent filter", runs.filter)
	}
}

func TestCheckEvalLoopStalenessBoundary(t *testing.T) {
	anchor := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	const expectedGrace = 5 * time.Minute
	tests := []struct {
		name string
		now  time.Time
		want CheckStatus
	}{
		{
			name: "one missed tick remains healthy",
			now:  anchor.Add(time.Minute + expectedGrace + 30*time.Second),
			want: StatusPass,
		},
		{
			name: "just past second fire plus grace is stale",
			now:  anchor.Add(2*time.Minute + expectedGrace + time.Second),
			want: StatusWarn,
		},
		{
			name: "between second fire and grace remains healthy",
			now:  anchor.Add(2*time.Minute + expectedGrace/2),
			want: StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
				bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
				runs: &evalLoopTestDriverRuns{runs: []*domain.DriverRun{{
					RunID:     "boundary-run",
					Status:    domain.DriverRunCompleted,
					StartedAt: anchor,
				}}},
			}, "TEST", tt.now)
			if result.Status != tt.want {
				t.Fatalf("status = %v, want %v (result=%+v)", result.Status, tt.want, result)
			}
		})
	}
}

func TestCheckEvalLoopUnsupportedBackendPasses(t *testing.T) {
	now := time.Date(2026, time.January, 2, 10, 5, 0, 0, time.UTC)
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs: &evalLoopTestDriverRuns{runs: []*domain.DriverRun{{
			RunID:      "unsupported",
			Status:     domain.DriverRunFailed,
			ErrorClass: evalBackendUnsupported,
			StartedAt:  now.Add(-time.Minute),
		}}},
	}, "TEST", now)

	if result.Status != StatusPass || result.Summary != "evals not applicable for this backend" {
		t.Fatalf("result = %+v, want informational unsupported-backend pass", result)
	}
}

func TestCheckEvalLoopJudgeErrorWarnsAndQuotesClass(t *testing.T) {
	now := time.Date(2026, time.January, 2, 10, 5, 0, 0, time.UTC)
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs: &evalLoopTestDriverRuns{runs: []*domain.DriverRun{{
			RunID:      "judge-failed",
			Status:     domain.DriverRunFailed,
			ErrorClass: "judge_error",
			StartedAt:  now.Add(-time.Minute),
		}}},
	}, "TEST", now)

	if result.Status != StatusWarn || !strings.Contains(result.Summary, `"judge_error"`) {
		t.Fatalf("result = %+v, want warning quoting judge_error", result)
	}
}

func TestCheckEvalLoopNoRunsUsesUpdatedAtAnchor(t *testing.T) {
	anchor := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	binding := evalLoopTestBinding(true)
	binding.CreatedAt = anchor.Add(-time.Hour)
	binding.UpdatedAt = anchor
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: binding},
		runs:     &evalLoopTestDriverRuns{},
	}, "TEST", anchor.Add(2*time.Minute+evalLoopStaleGrace+time.Second))

	if result.Status != StatusWarn || result.Summary != "no eval runs recorded" {
		t.Fatalf("result = %+v, want no-runs warning", result)
	}
	if !strings.Contains(result.Detail, "binding updated_at") {
		t.Fatalf("detail = %q, want updated_at anchor choice", result.Detail)
	}
}

func TestCheckEvalLoopNoRunsUsesCreatedAtFallback(t *testing.T) {
	anchor := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	binding := evalLoopTestBinding(true)
	binding.CreatedAt = anchor
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: binding},
		runs:     &evalLoopTestDriverRuns{},
	}, "TEST", anchor.Add(2*time.Minute+evalLoopStaleGrace+time.Second))

	if result.Status != StatusWarn || !strings.Contains(result.Detail, "binding created_at") {
		t.Fatalf("result = %+v, want created_at fallback warning", result)
	}
}

func TestCheckEvalLoopNoRunsWithoutBindingTimestampsWarns(t *testing.T) {
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs:     &evalLoopTestDriverRuns{},
	}, "TEST", time.Now())

	if result.Status != StatusWarn || result.Summary != "no eval runs recorded" {
		t.Fatalf("result = %+v, want missing-anchor warning", result)
	}
}

func TestCheckEvalLoopNoRunsWithinWindowPasses(t *testing.T) {
	anchor := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	binding := evalLoopTestBinding(true)
	binding.UpdatedAt = anchor
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: binding},
		runs:     &evalLoopTestDriverRuns{},
	}, "TEST", anchor.Add(time.Minute))

	if result.Status != StatusPass || result.Summary != "session eval loop awaiting first scheduled run" {
		t.Fatalf("result = %+v, want awaiting-first-run pass", result)
	}
}

func TestCheckEvalLoopLatestRunWithoutStartedAtWarns(t *testing.T) {
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs: &evalLoopTestDriverRuns{runs: []*domain.DriverRun{{
			RunID:  "missing-start",
			Status: domain.DriverRunCompleted,
		}}},
	}, "TEST", time.Now())

	if result.Status != StatusWarn || result.Summary != "latest session eval run has no started_at" {
		t.Fatalf("result = %+v, want missing-started_at warning", result)
	}
}

func TestCheckEvalLoopInvalidScheduleWarns(t *testing.T) {
	binding := evalLoopTestBinding(true)
	binding.Schedule = "not a cron"
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: binding},
		runs:     &evalLoopTestDriverRuns{},
	}, "TEST", time.Now())

	if result.Status != StatusWarn || result.Summary != "session eval cron schedule is invalid" {
		t.Fatalf("result = %+v, want invalid-schedule warning", result)
	}
}

func TestCheckEvalLoopBindingReadErrorWarns(t *testing.T) {
	runs := &evalLoopTestDriverRuns{}
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{err: errors.New("binding read failed")},
		runs:     runs,
	}, "TEST", time.Now())

	if result.Status != StatusWarn || result.Summary != "could not load session eval cron binding" {
		t.Fatalf("result = %+v, want binding-read warning", result)
	}
	if runs.called {
		t.Fatal("listed driver runs after binding read failed")
	}
}

func TestCheckEvalLoopDriverRunListErrorWarns(t *testing.T) {
	result := checkEvalLoopWithStore(t.Context(), evalLoopTestStore{
		bindings: &evalLoopTestTriggerBindings{binding: evalLoopTestBinding(true)},
		runs:     &evalLoopTestDriverRuns{err: errors.New("driver run list failed")},
	}, "TEST", time.Now())

	if result.Status != StatusWarn || result.Summary != "could not list session eval runs" {
		t.Fatalf("result = %+v, want driver-run-list warning", result)
	}
}

func TestCheckEvalLoopNoResolvableWorkspaceSkips(t *testing.T) {
	original := openEvalLoopStore
	openEvalLoopStore = func(context.Context) (*bootstrap.StoreHandle, error) {
		return &bootstrap.StoreHandle{Store: memstore.New()}, nil
	}
	t.Cleanup(func() { openEvalLoopStore = original })
	t.Setenv("LOOM_WORKSPACE", "")

	result := checkEvalLoop()
	if result.Status != StatusPass || !strings.Contains(result.Summary, "active workspace unavailable") {
		t.Fatalf("result = %+v, want active-workspace skip", result)
	}
}

func evalLoopTestBinding(enabled bool) *domain.TriggerBinding {
	return &domain.TriggerBinding{
		BindingID: "eval-cron",
		Enabled:   enabled,
		Schedule:  "* * * * *",
	}
}

type evalLoopTestStore struct {
	store.Store
	bindings store.TriggerBindingStore
	runs     store.DriverRunStore
}

func (s evalLoopTestStore) TriggerBindings() store.TriggerBindingStore { return s.bindings }
func (s evalLoopTestStore) DriverRuns() store.DriverRunStore           { return s.runs }

type evalLoopTestTriggerBindings struct {
	store.TriggerBindingStore
	binding *domain.TriggerBinding
	err     error
}

func (s *evalLoopTestTriggerBindings) GetByRouteKey(context.Context, string, string) (*domain.TriggerBinding, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.binding == nil {
		return nil, errors.New("unexpected nil binding")
	}
	return s.binding, nil
}

type evalLoopTestDriverRuns struct {
	store.DriverRunStore
	runs   []*domain.DriverRun
	err    error
	called bool
	filter store.DriverRunFilter
}

func (s *evalLoopTestDriverRuns) List(_ context.Context, _ string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	s.called = true
	s.filter = filter
	return s.runs, s.err
}
