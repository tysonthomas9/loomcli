package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	evalspkg "github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const (
	evalLoopCheckName      = "eval_loop"
	evalLoopDriverRunLimit = 200
	evalLoopStaleGrace     = 5 * time.Minute
	evalBackendUnsupported = "eval_backend_unsupported"
)

type evalLoopStoreOpener func(context.Context) (*bootstrap.StoreHandle, error)

var openEvalLoopStore evalLoopStoreOpener = cmdstore.OpenStore

// checkEvalLoop reports whether the opt-in session-eval cron has recently
// started. Evals are observability, so this check intentionally never fails
// doctor: unavailable store/workspace state is an informational skip and all
// data-plane problems are warnings.
func checkEvalLoop() CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := openEvalLoopStore(ctx)
	if err != nil || handle == nil || handle.Store == nil {
		return skippedEvalLoop("control-plane store unavailable")
	}
	defer func() { _ = handle.Close() }()

	workspaceKey, err := cmdstore.ActiveWorkspace(ctx, handle.Store)
	if err != nil || workspaceKey == "" {
		return skippedEvalLoop("active workspace unavailable")
	}
	return checkEvalLoopWithStore(ctx, handle.Store, workspaceKey, time.Now().UTC())
}

func checkEvalLoopWithStore(ctx context.Context, st store.Store, workspaceKey string, now time.Time) CheckResult {
	if st == nil || workspaceKey == "" {
		return skippedEvalLoop("control-plane store unavailable")
	}

	binding, err := st.TriggerBindings().GetByRouteKey(ctx, workspaceKey, evalspkg.EvalCronRouteKey)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return evalsNotProvisioned()
		}
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "could not load session eval cron binding",
			Detail:  err.Error(),
		}
	}
	if binding == nil || !binding.Enabled {
		return evalsNotProvisioned()
	}

	runs, err := st.DriverRuns().List(ctx, workspaceKey, store.DriverRunFilter{
		DriverID: workflowdefs.BuiltinSessionEvalAgentWorkflowName,
		Limit:    evalLoopDriverRunLimit,
	})
	if err != nil {
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "could not list session eval runs",
			Detail:  err.Error(),
		}
	}

	return assessEvalLoop(binding, runs, now)
}

func skippedEvalLoop(reason string) CheckResult {
	return CheckResult{
		Name:    evalLoopCheckName,
		Status:  StatusPass,
		Summary: "eval loop check skipped: " + reason,
	}
}

func evalsNotProvisioned() CheckResult {
	return CheckResult{
		Name:    evalLoopCheckName,
		Status:  StatusPass,
		Summary: "evals not provisioned",
	}
}

func assessEvalLoop(binding *domain.TriggerBinding, runs []*domain.DriverRun, now time.Time) CheckResult {
	schedule, location, err := evalLoopSchedule(binding)
	if err != nil {
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "session eval cron schedule is invalid",
			Detail:  err.Error(),
		}
	}

	latest := latestEvalLoopRun(runs)
	if latest == nil {
		return assessEvalLoopWithoutRuns(binding, schedule, location, now)
	}
	if latest.StartedAt.IsZero() {
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "latest session eval run has no started_at",
			Detail:  fmt.Sprintf("run=%s", latest.RunID),
		}
	}

	secondFire := secondEvalLoopFire(schedule, latest.StartedAt, location)
	staleAfter := secondFire.Add(evalLoopStaleGrace)
	if now.After(staleAfter) {
		detail := fmt.Sprintf("latest run=%s started_at=%s; second scheduled fire=%s; grace=%s",
			latest.RunID, latest.StartedAt.UTC().Format(time.RFC3339), secondFire.UTC().Format(time.RFC3339), evalLoopStaleGrace)
		if latest.Status == domain.DriverRunFailed {
			detail += fmt.Sprintf("; latest error_class=%q", evalLoopErrorClass(latest))
		}
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "session eval loop appears stale",
			Detail:  detail,
		}
	}

	return assessEvalLoopLatestRun(latest)
}

func assessEvalLoopLatestRun(latest *domain.DriverRun) CheckResult {
	if latest.Status == domain.DriverRunFailed {
		class := evalLoopErrorClass(latest)
		if class == evalBackendUnsupported {
			return CheckResult{
				Name:    evalLoopCheckName,
				Status:  StatusPass,
				Summary: "evals not applicable for this backend",
				Detail:  fmt.Sprintf("latest run=%s error_class=%q", latest.RunID, class),
			}
		}
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: fmt.Sprintf("latest session eval run failed (error_class=%q)", class),
			Detail:  fmt.Sprintf("run=%s started_at=%s", latest.RunID, latest.StartedAt.UTC().Format(time.RFC3339)),
		}
	}

	return CheckResult{
		Name:    evalLoopCheckName,
		Status:  StatusPass,
		Summary: "session eval loop healthy",
		Detail:  fmt.Sprintf("latest run=%s started_at=%s", latest.RunID, latest.StartedAt.UTC().Format(time.RFC3339)),
	}
}

func assessEvalLoopWithoutRuns(binding *domain.TriggerBinding, schedule cron.Schedule, location *time.Location, now time.Time) CheckResult {
	anchor, anchorName := evalLoopAnchor(binding)
	if anchor.IsZero() {
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "no eval runs recorded",
			Detail:  "binding has neither updated_at nor created_at; cannot determine when evals were enabled",
		}
	}

	secondFire := secondEvalLoopFire(schedule, anchor, location)
	staleAfter := secondFire.Add(evalLoopStaleGrace)
	if now.After(staleAfter) {
		return CheckResult{
			Name:    evalLoopCheckName,
			Status:  StatusWarn,
			Summary: "no eval runs recorded",
			Detail: fmt.Sprintf("using binding %s=%s as the provision anchor; second scheduled fire=%s; grace=%s",
				anchorName, anchor.UTC().Format(time.RFC3339), secondFire.UTC().Format(time.RFC3339), evalLoopStaleGrace),
		}
	}

	return CheckResult{
		Name:    evalLoopCheckName,
		Status:  StatusPass,
		Summary: "session eval loop awaiting first scheduled run",
		Detail: fmt.Sprintf("using binding %s=%s as the provision anchor",
			anchorName, anchor.UTC().Format(time.RFC3339)),
	}
}

// evalLoopSchedule intentionally mirrors trigger.parseCronSchedule and
// trigger.loadScheduleLocation. Those helpers are private to the trigger
// runtime, so doctor uses the same cron parser and UTC-empty-timezone rule.
func evalLoopSchedule(binding *domain.TriggerBinding) (cron.Schedule, *time.Location, error) {
	if binding == nil {
		return nil, nil, errors.New("eval cron binding is missing")
	}
	schedule, err := cron.ParseStandard(binding.Schedule)
	if err != nil {
		return nil, nil, fmt.Errorf("parse schedule %q: %w", binding.Schedule, err)
	}
	if binding.ScheduleTimezone == "" {
		return schedule, time.UTC, nil
	}
	location, err := time.LoadLocation(binding.ScheduleTimezone)
	if err != nil {
		return nil, nil, fmt.Errorf("load schedule timezone %q: %w", binding.ScheduleTimezone, err)
	}
	return schedule, location, nil
}

func latestEvalLoopRun(runs []*domain.DriverRun) *domain.DriverRun {
	var latest *domain.DriverRun
	for _, run := range runs {
		if run == nil {
			continue
		}
		if latest == nil || run.StartedAt.After(latest.StartedAt) ||
			(run.StartedAt.Equal(latest.StartedAt) && run.CreatedAt.After(latest.CreatedAt)) ||
			(run.StartedAt.Equal(latest.StartedAt) && run.CreatedAt.Equal(latest.CreatedAt) && run.RunID > latest.RunID) {
			latest = run
		}
	}
	return latest
}

func secondEvalLoopFire(schedule cron.Schedule, anchor time.Time, location *time.Location) time.Time {
	firstFire := schedule.Next(anchor.In(location))
	return schedule.Next(firstFire)
}

func evalLoopAnchor(binding *domain.TriggerBinding) (time.Time, string) {
	if !binding.UpdatedAt.IsZero() {
		return binding.UpdatedAt, "updated_at"
	}
	return binding.CreatedAt, "created_at"
}

func evalLoopErrorClass(run *domain.DriverRun) string {
	if strings.TrimSpace(run.ErrorClass) == "" {
		return "unknown"
	}
	return run.ErrorClass
}
