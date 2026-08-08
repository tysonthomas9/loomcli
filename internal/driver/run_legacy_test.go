package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// emitRunFinishedEvent is the compatibility-only fast path retained for
// characterization tests without the Phase 4 Execution queue. Production
// always uses emitRunFinishedEventWithExecution.
func emitRunFinishedEvent(
	ctx context.Context,
	s store.Store,
	publisher RunOutcomePublisher,
	run *domain.DriverRun,
	injectedAwaitNotifier ...RunOutcomeAwaitNotifier,
) {
	if s == nil || run == nil || !run.Status.IsTerminal() {
		return
	}
	outcome := newRunOutcome(ctx, s, run)
	var notifier RunOutcomeAwaitNotifier
	var notifierErr error
	if len(injectedAwaitNotifier) > 0 {
		notifier = injectedAwaitNotifier[0]
		if notifier == nil {
			notifierErr = fmt.Errorf("Execution run outcome await notifier is unavailable")
		}
	} else {
		notifier, notifierErr = NewRunOutcomeAwaitNotifier(s.Awaits())
	}
	unlock := lockRunOutcome(run.WorkspaceKey, run.RunID)
	if notifierErr == nil {
		dispatchRunFinishedAwaits(ctx, notifier, outcome)
	}
	unlock()
	if _, durable := s.DriverRuns().(store.DriverRunOutcomeStore); durable {
		return
	}
	if notifierErr != nil {
		slog.WarnContext(ctx, "run.finished await notifier unavailable",
			"runID", run.RunID, "status", string(run.Status), "eventID", outcome.EventID, "error", notifierErr)
	}
	if publisher == nil {
		return
	}
	if err := publisher.PublishRunOutcome(ctx, outcome); err != nil {
		slog.WarnContext(ctx, "publish run.finished outcome failed",
			"runID", run.RunID, "status", string(run.Status), "eventID", outcome.EventID, "error", err)
	}
}

// NewRunOutcomeAwaitNotifier is the legacy store-backed constructor used only
// by characterization tests. Production supplies an Execution-backed resolver
// to NewRunOutcomeAwaitNotifierWithResolver.
func NewRunOutcomeAwaitNotifier(awaits store.AwaitStore) (RunOutcomeAwaitNotifier, error) {
	resolver, ok := awaits.(store.RunOutcomeAwaitStore)
	if !ok {
		return nil, fmt.Errorf("await store lacks atomic run outcome resolve-and-resume capability")
	}
	return NewRunOutcomeAwaitNotifierWithResolver(awaits, resolver)
}

// RunOptions and CreateDriverRun remain only as fixture compatibility for the
// driver package's characterization/E2E tests. Production submissions cross
// the authenticated management API and typed Execution command boundary.
type RunOptions struct {
	WorkspaceKey     string
	DriverID         string
	DriverVersionID  string
	EpicID           string
	RunID            string
	IdempotencyKey   string
	Entrypoint       string
	SourceKind       string
	SourceRef        string
	TriggerBindingID string
	Payload          json.RawMessage
}

func CreateDriverRun(ctx context.Context, s store.Store, opts RunOptions) (*domain.DriverRun, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(opts.WorkspaceKey) == "" || strings.TrimSpace(opts.DriverID) == "" {
		return nil, fmt.Errorf("workspace key and driver id required: %w", domain.ErrInvalid)
	}
	driver, version, err := resolveDriverRunVersion(ctx, s, opts.WorkspaceKey, opts.DriverID, opts.DriverVersionID)
	if err != nil {
		return nil, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	entrypoint := opts.Entrypoint
	if entrypoint == "" {
		entrypoint = EntrypointRun
	}
	payload := clonePayload(opts.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("payload must be valid JSON: %w", domain.ErrInvalid)
	}
	sourceKind := strings.TrimSpace(opts.SourceKind)
	if sourceKind == "" {
		sourceKind = "cli"
	}
	sourceRef := strings.TrimSpace(opts.SourceRef)
	if sourceRef == "" {
		sourceRef = "loom driver run"
	}
	return s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: opts.WorkspaceKey, RunID: runID,
		DriverID: driver.DriverID, DriverVersionID: version.VersionID,
		Entrypoint: entrypoint, SourceKind: sourceKind, SourceRef: sourceRef,
		EpicID: opts.EpicID, TriggerBindingID: opts.TriggerBindingID,
		IdempotencyKey: opts.IdempotencyKey, Payload: payload,
	})
}

func resolveDriverRunVersion(ctx context.Context, s store.Store, workspaceKey, driverID, versionID string) (*workflowcatalog.Driver, *workflowcatalog.DriverVersion, error) {
	if strings.TrimSpace(versionID) == "" {
		return activeDriverVersion(ctx, s, workspaceKey, driverID)
	}
	driver, err := s.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	version, err := s.DriverVersions().Get(ctx, workspaceKey, strings.TrimSpace(versionID))
	if err != nil {
		return nil, nil, fmt.Errorf("get driver version: %w", err)
	}
	if version.DriverID != driver.DriverID || version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("driver %q version %q is not a passed version for this driver: %w", driver.DriverID, version.VersionID, domain.ErrInvalid)
	}
	return driver, version, nil
}
