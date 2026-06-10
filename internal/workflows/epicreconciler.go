package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// Error classes recorded on failed DriverRuns. The E2E acceptance
// criteria require interrupted runs to carry a clear class.
const (
	ErrorClassAgentError       = "agent_error"
	ErrorClassStreamLost       = "flue_stream_interrupted"
	ErrorClassPlaneUnavailable = "execution_plane_unavailable"
	ErrorClassLoomRestart      = "loom_restart"
)

// EpicReconcilerConfig wires an EpicReconciler.
type EpicReconcilerConfig struct {
	// Workspace scopes every store call.
	Workspace string
	// NodeID identifies this control-plane instance for run claims.
	// Must be stable across restarts so orphaned runs can be
	// recognized as our own.
	NodeID string
	// DriverName is the fleet-db Driver and Flue agent name.
	// Defaults to "epic-runner".
	DriverName string
	// SourceRef is recorded on dev-stamped DriverVersions (typically
	// the workflow project dir).
	SourceRef string
	// Store is the platform data plane.
	Store platform.Store
	// Plane is the execution plane.
	Plane execplane.ExecutionPlane
	// ResolveEpic maps an issue ID to its parent epic ID. ok=false
	// when the issue has no epic. Nil disables issue-event wakes
	// (runs still start via explicit admission).
	ResolveEpic func(ctx context.Context, issueID string) (epicID string, ok bool)
	// OnEvent observes captured Flue events (live-tail hook). May be nil.
	OnEvent func(epicID, runID string, e execplane.Event)
	// Tick is called once per watch iteration (daemon liveness). May be nil.
	Tick func()
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// PollTimeout is the server-side long-poll block. Defaults to 5s.
	PollTimeout time.Duration
	// RetryDelay is the wake retry delay after execution-plane
	// failures. Defaults to 10s.
	RetryDelay time.Duration
	// StaleRecoveryInterval bounds how often fleet-db's stale-run
	// recovery sweep is requested. Defaults to 60s.
	StaleRecoveryInterval time.Duration
}

// EpicReconciler is the level-triggered epic-runner loop: it watches
// FleetDB mutations, admits one DriverRun per wake (one_active_per_epic
// dedupes), claims it, invokes the per-epic Flue agent instance,
// captures the event stream, and finishes the run. Epic/task state is
// re-derived from FleetDB on every wake — correctness comes from
// idempotency, not journaling.
type EpicReconciler struct {
	cfg       EpicReconcilerConfig
	logger    *slog.Logger
	versionID string

	mu       sync.Mutex
	inflight map[string]bool      // epicID → wake in progress (this process)
	missed   map[string]bool      // epicID → wake signal arrived mid-flight
	retryAt  map[string]time.Time // epicID → next wake attempt

	wg sync.WaitGroup
}

// NewEpicReconciler validates cfg and applies defaults.
func NewEpicReconciler(cfg EpicReconcilerConfig) (*EpicReconciler, error) {
	if cfg.Workspace == "" || cfg.NodeID == "" || cfg.Store == nil || cfg.Plane == nil {
		return nil, errors.New("workflows: Workspace, NodeID, Store, and Plane are required")
	}
	if cfg.DriverName == "" {
		cfg.DriverName = "epic-runner"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 5 * time.Second
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 10 * time.Second
	}
	if cfg.StaleRecoveryInterval <= 0 {
		cfg.StaleRecoveryInterval = 60 * time.Second
	}
	return &EpicReconciler{
		cfg:      cfg,
		logger:   cfg.Logger.With("component", "epic-reconciler"),
		inflight: map[string]bool{},
		missed:   map[string]bool{},
		retryAt:  map[string]time.Time{},
	}, nil
}

// Run is the reconciler goroutine body. It returns when ctx is done,
// after waiting for in-flight wakes to settle.
func (r *EpicReconciler) Run(ctx context.Context) error {
	defer r.wg.Wait()

	if err := r.ensureDriver(ctx); err != nil {
		return fmt.Errorf("workflows: ensure driver: %w", err)
	}
	// Events are wake signals only — state is re-derived from the
	// store, so we recover current obligations first and then watch
	// from the feed's tail.
	r.recoverOrphans(ctx)
	r.claimQueuedRuns(ctx)
	cursor, err := r.drainToTail(ctx)
	if err != nil {
		return fmt.Errorf("workflows: drain mutation feed: %w", err)
	}

	lastSweep := time.Now()
	for {
		if r.cfg.Tick != nil {
			r.cfg.Tick()
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		page, err := r.cfg.Store.Events().Poll(ctx, r.cfg.Workspace, platform.MutationPoll{
			Since: cursor, Timeout: r.cfg.PollTimeout, Limit: 500,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			r.logger.Warn("mutation poll failed; backing off", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			continue
		}
		cursor = page.Cursor
		for _, e := range page.Events {
			r.handleEvent(ctx, e)
		}
		r.processDueRetries(ctx)
		if time.Since(lastSweep) >= r.cfg.StaleRecoveryInterval {
			lastSweep = time.Now()
			if _, err := r.cfg.Store.DriverRuns().RecoverStale(ctx, r.cfg.Workspace, 0, ErrorClassLoomRestart, "recovered by stale sweep"); err != nil {
				r.logger.Warn("stale run recovery failed", "err", err)
			}
		}
	}
}

// ensureDriver registers the driver if needed and stamps a fresh
// ephemeral dev version (dev-<ts>) so every run pins a version. One
// version accumulates per reconciler session (daemon start / Flue
// reattach) — cheap records, acceptable in dev; cloud mode replaces
// this with explicit register/activate (Phase 4).
func (r *EpicReconciler) ensureDriver(ctx context.Context) error {
	drivers := r.cfg.Store.Drivers()
	if _, err := drivers.Get(ctx, r.cfg.Workspace, r.cfg.DriverName); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if _, err := drivers.Create(ctx, r.cfg.Workspace, platform.Driver{
			DriverID: r.cfg.DriverName, Name: r.cfg.DriverName, OwnerType: "system", Status: "active",
		}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			return err
		}
	}
	now := time.Now().UTC()
	versionID := fmt.Sprintf("dev-%d", now.UnixNano())
	v, err := drivers.CreateVersion(ctx, r.cfg.Workspace, r.cfg.DriverName, platform.DriverVersion{
		VersionID:    versionID,
		Version:      int(now.Unix()),
		SourceRef:    r.cfg.SourceRef,
		SourceDigest: "sha256:dev",
		BundleDigest: "sha256:dev",
		Runtime:      "flue-dev",
		CreatedBy:    r.cfg.NodeID,
	})
	if err != nil {
		return err
	}
	if _, err := drivers.Activate(ctx, r.cfg.Workspace, r.cfg.DriverName, v.VersionID); err != nil {
		r.logger.Warn("activate dev version failed", "err", err)
	}
	r.versionID = v.VersionID
	r.logger.Info("stamped dev driver version", "driver", r.cfg.DriverName, "version", v.VersionID)
	return nil
}

// recoverOrphans finishes running runs this node owned before a
// restart (the owner triple is read back from the run record) and
// schedules a fresh wake for their epics.
func (r *EpicReconciler) recoverOrphans(ctx context.Context) {
	runs, err := r.cfg.Store.DriverRuns().List(ctx, r.cfg.Workspace, platform.DriverRunFilter{
		DriverID: r.cfg.DriverName, Status: platform.DriverRunRunning,
	})
	if err != nil {
		r.logger.Warn("orphan scan failed", "err", err)
		return
	}
	for _, run := range runs {
		if run.NodeID != r.cfg.NodeID {
			continue // another live node may own it; the stale sweep handles the dead ones
		}
		_, err := r.cfg.Store.DriverRuns().Finish(ctx, r.cfg.Workspace, run.RunID, run.NodeID, run.LeaseID, run.FencingToken, platform.DriverRunFinish{
			Status: platform.DriverRunFailed, ErrorClass: ErrorClassLoomRestart,
			Summary: "loom restarted mid-wake; epic re-woken",
		})
		if err != nil {
			r.logger.Warn("orphan finish failed", "run_id", run.RunID, "err", err)
			continue
		}
		r.logger.Info("recovered orphaned run", "run_id", run.RunID, "epic", run.EpicID)
		if run.EpicID != "" {
			r.wakeEpic(ctx, run.EpicID, "orphan recovery")
		}
	}
}

// claimQueuedRuns picks up queued runs admitted while we were away
// (CLI/UI admission with no live reconciler).
func (r *EpicReconciler) claimQueuedRuns(ctx context.Context) {
	runs, err := r.cfg.Store.DriverRuns().List(ctx, r.cfg.Workspace, platform.DriverRunFilter{
		DriverID: r.cfg.DriverName, Status: platform.DriverRunQueued,
	})
	if err != nil {
		r.logger.Warn("queued run scan failed", "err", err)
		return
	}
	for _, run := range runs {
		r.startWake(ctx, run.EpicID, run, "startup queued run")
	}
}

// drainToTail advances past the historical mutation feed without
// processing it (startup state was just reconciled from the store).
func (r *EpicReconciler) drainToTail(ctx context.Context) (string, error) {
	cursor := "0"
	for {
		page, err := r.cfg.Store.Events().Poll(ctx, r.cfg.Workspace, platform.MutationPoll{Since: cursor, Limit: 1000})
		if err != nil {
			return "", err
		}
		cursor = page.Cursor
		if !page.HasMore {
			return cursor, nil
		}
	}
}

// handleEvent classifies one mutation event into a wake signal.
func (r *EpicReconciler) handleEvent(ctx context.Context, e platform.MutationEvent) {
	switch e.EntityType {
	case "driver_run":
		if e.Metadata["driver_id"] != r.cfg.DriverName {
			return
		}
		epicID := e.Metadata["epic_id"]
		switch e.Action {
		case "driver_run.create":
			if e.Metadata["source_kind"] == "reconciler" {
				return // our own admission — already being executed
			}
			// Externally admitted run (CLI/UI) — claim and execute it.
			run, err := r.cfg.Store.DriverRuns().Get(ctx, r.cfg.Workspace, e.EntityID)
			if err != nil || run.Status != platform.DriverRunQueued {
				return
			}
			r.startWake(ctx, epicID, run, "admitted run")
		case "driver_run.finish", "driver_run.recover":
			// A wake signal may have been swallowed by admission dedupe
			// while this run was active — replay it now.
			if epicID != "" && r.takeMissed(epicID) {
				r.wakeEpic(ctx, epicID, "missed signal replay")
			}
		}
	case "issue":
		if r.cfg.ResolveEpic == nil {
			return
		}
		switch e.Action {
		case "issue.close", "issue.update", "issue.reopen":
		default:
			return
		}
		epicID, ok := r.cfg.ResolveEpic(ctx, e.EntityID)
		if !ok {
			return
		}
		// Only advance epics the runner is driving (≥1 prior run).
		runs, err := r.cfg.Store.DriverRuns().List(ctx, r.cfg.Workspace, platform.DriverRunFilter{
			DriverID: r.cfg.DriverName, EpicID: epicID, Limit: 1,
		})
		if err != nil || len(runs) == 0 {
			return
		}
		r.wakeEpic(ctx, epicID, "issue event "+e.Action)
	}
}

func (r *EpicReconciler) takeMissed(epicID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missed[epicID] {
		delete(r.missed, epicID)
		return true
	}
	return false
}

func (r *EpicReconciler) processDueRetries(ctx context.Context) {
	r.mu.Lock()
	var due []string
	now := time.Now()
	for epic, at := range r.retryAt {
		if now.After(at) {
			due = append(due, epic)
			delete(r.retryAt, epic)
		}
	}
	r.mu.Unlock()
	for _, epic := range due {
		r.wakeEpic(ctx, epic, "retry")
	}
}

func (r *EpicReconciler) scheduleRetry(epicID string) {
	if epicID == "" {
		return
	}
	r.mu.Lock()
	r.retryAt[epicID] = time.Now().Add(r.cfg.RetryDelay)
	r.mu.Unlock()
}

// wakeEpic admits a new DriverRun for the epic and executes it. When a
// run is already active (admission returns the existing run) the
// signal is remembered and replayed on that run's finish event.
func (r *EpicReconciler) wakeEpic(ctx context.Context, epicID, reason string) {
	r.startWake(ctx, epicID, nil, reason)
}

// startWake launches the wake goroutine. preAdmitted carries an
// already-created queued run to claim instead of admitting a new one.
func (r *EpicReconciler) startWake(ctx context.Context, epicID string, preAdmitted *platform.DriverRun, reason string) {
	key := epicID
	if key == "" && preAdmitted != nil {
		key = "run:" + preAdmitted.RunID
	}
	r.mu.Lock()
	if r.inflight[key] {
		if epicID != "" {
			r.missed[epicID] = true
		}
		r.mu.Unlock()
		return
	}
	r.inflight[key] = true
	r.mu.Unlock()

	r.wg.Go(func() {
		absorbed := r.executeWake(ctx, epicID, preAdmitted, reason)
		r.mu.Lock()
		delete(r.inflight, key)
		// Signals that arrived while THIS wake ran are replayed now.
		// An absorbed wake (admission returned someone else's active
		// run) keeps its missed flag instead — that run's finish event
		// replays it, and replaying immediately would busy-loop against
		// admission until the active run ends.
		replay := epicID != "" && r.missed[epicID] && !absorbed
		if replay {
			delete(r.missed, epicID)
		}
		r.mu.Unlock()
		if replay && ctx.Err() == nil {
			r.wakeEpic(ctx, epicID, "missed signal replay")
		}
	})
}

// executeWake performs one full wake: admission → claim → invoke →
// capture → finish. The returned flag reports whether the wake was
// absorbed by an already-active run (one_active_per_epic).
func (r *EpicReconciler) executeWake(ctx context.Context, epicID string, preAdmitted *platform.DriverRun, reason string) (absorbed bool) {
	logger := r.logger.With("epic", epicID, "reason", reason)

	run := preAdmitted
	if run == nil {
		payload, _ := json.Marshal(map[string]string{"action": "advance", "epic_id": epicID})
		created, err := r.cfg.Store.DriverRuns().Create(ctx, r.cfg.Workspace, platform.DriverRunCreate{
			RunID:           fmt.Sprintf("run-%s-%d", SanitizeID(epicID), time.Now().UnixNano()),
			DriverID:        r.cfg.DriverName,
			DriverVersionID: r.versionID,
			Entrypoint:      "advance",
			SourceKind:      "reconciler",
			EpicID:          epicID,
			Payload:         payload,
		})
		if err != nil {
			logger.Warn("run admission failed", "err", err)
			r.scheduleRetry(epicID)
			return false
		}
		run = created
	}

	if run.Status != platform.DriverRunQueued {
		// Admission returned an existing active run — the wake signal
		// is absorbed; that run's finish event replays it.
		if epicID != "" {
			r.mu.Lock()
			r.missed[epicID] = true
			r.mu.Unlock()
		}
		logger.Debug("wake absorbed by active run", "run_id", run.RunID)
		return true
	}

	lifecycle, err := ClaimRun(ctx, r.cfg.Store, r.cfg.Workspace, run.RunID, r.cfg.NodeID, r.logger)
	if err != nil {
		// Someone else claimed it, or it was transitioned out of queued
		// (e.g. the stale sweep raced us). Retry so the wake signal is
		// not lost — admission dedupes if a run is genuinely active.
		logger.Debug("claim skipped; retrying wake", "run_id", run.RunID, "err", err)
		r.scheduleRetry(epicID)
		return false
	}
	logger.Info("wake started", "run_id", run.RunID)

	stream, err := r.cfg.Plane.Invoke(ctx, r.cfg.DriverName, instanceID(epicID, run.RunID), execplane.InvokeRequest{
		Message: wakeMessage(r.cfg.Workspace, epicID, run.RunID),
	})
	if err != nil {
		logger.Warn("execution plane invoke failed", "run_id", run.RunID, "err", err)
		if _, ferr := lifecycle.Finish(ctx, platform.DriverRunFinish{
			Status: platform.DriverRunFailed, ErrorClass: ErrorClassPlaneUnavailable, Summary: err.Error(),
		}); ferr != nil {
			logger.Warn("finish after invoke failure failed", "err", ferr)
		}
		r.scheduleRetry(epicID)
		return false
	}

	res := CaptureStream(ctx, stream, CaptureOptions{
		Logger: logger,
		OnEvent: func(e execplane.Event) {
			if r.cfg.OnEvent != nil {
				r.cfg.OnEvent(epicID, run.RunID, e)
			}
		},
	})

	finish := platform.DriverRunFinish{
		Status:  platform.DriverRunCompleted,
		Summary: summarize(res),
		Output: map[string]string{
			"events":        fmt.Sprintf("%d", res.Events),
			"tool_calls":    fmt.Sprintf("%d", res.ToolCalls),
			"input_tokens":  fmt.Sprintf("%d", res.Usage.InputTokens),
			"output_tokens": fmt.Sprintf("%d", res.Usage.OutputTokens),
			"last_text":     res.LastText,
		},
	}
	switch {
	case res.Terminal && res.ErrorMessage != "":
		finish.Status = platform.DriverRunFailed
		finish.ErrorClass = ErrorClassAgentError
		finish.Summary = res.ErrorMessage
	case !res.Terminal:
		finish.Status = platform.DriverRunFailed
		finish.ErrorClass = ErrorClassStreamLost
		if res.StreamErr != nil {
			finish.Summary = res.StreamErr.Error()
		} else {
			finish.Summary = "flue stream ended without a terminal event"
		}
	}

	finishCtx := ctx
	if ctx.Err() != nil {
		// Shutdown mid-wake: still record the interruption.
		var cancel context.CancelFunc
		finishCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finish.Status = platform.DriverRunCancelled
		finish.ErrorClass = ErrorClassLoomRestart
	}
	if _, err := lifecycle.Finish(finishCtx, finish); err != nil {
		logger.Warn("finish failed", "run_id", run.RunID, "err", err)
		return false
	}
	logger.Info("wake finished", "run_id", run.RunID, "status", finish.Status, "summary", finish.Summary)

	if finish.Status == platform.DriverRunFailed && finish.ErrorClass == ErrorClassStreamLost {
		r.scheduleRetry(epicID)
	}
	return false
}

// instanceID names the Flue agent instance. Per-epic instances give
// the runner conversational memory across wakes; runs without an epic
// fall back to per-run instances.
func instanceID(epicID, runID string) string {
	if epicID != "" {
		return epicID
	}
	return runID
}

// wakeMessage is the prompt delivered to the epic-runner agent. The
// run-scoped identifiers ride inside the message so the agent's tools
// can call back into FleetDB with them.
func wakeMessage(ws, epicID, runID string) string {
	msg := map[string]string{
		"action":    "advance",
		"workspace": ws,
		"epic_id":   epicID,
		"run_id":    runID,
	}
	raw, _ := json.Marshal(msg)
	return string(raw)
}

func summarize(res CaptureResult) string {
	text := strings.TrimSpace(res.LastText)
	if text == "" {
		return fmt.Sprintf("%d events, %d tool calls", res.Events, res.ToolCalls)
	}
	if len(text) > 240 {
		text = text[len(text)-240:]
	}
	return text
}

// SanitizeID maps an arbitrary string (typically an epic ID) into a
// store-safe identifier fragment for generated run IDs. Shared by the
// reconciler, the CLI, and the serve handler so generated IDs stay
// consistent.
func SanitizeID(s string) string {
	if s == "" {
		return "adhoc"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}
