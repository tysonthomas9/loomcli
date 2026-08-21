package leadapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const (
	defaultOccupantEpicConcurrency = 2
	maxOccupantEpicConcurrency     = 4
	defaultOccupantIntervalSeconds = 5
	maxOccupantEpicIDBytes         = 200
	occupantDefaultRunner          = "daytona-task-runner"
)

// allowedOccupantRunners excludes local-task-runner because it executes a
// backend CLI on the serve host. Occupant-dispatched workers stay sandboxed.
var allowedOccupantRunners = []string{occupantDefaultRunner}

type epicRunPlan struct {
	epicID         string
	leadName       string
	sessionID      string
	runner         string
	repoURL        string
	baseBranch     string
	maxConcurrency int
}

// payload returns the closed, server-derived epic-runner input. The occupant
// cannot name a workflow or supply identity, placement, or child-run fields.
func (p epicRunPlan) payload() (json.RawMessage, error) {
	payload := map[string]any{
		"epicId": p.epicID, "leadName": p.leadName,
		"orchestratorSessionId": p.sessionID,
		"maxConcurrency":        p.maxConcurrency,
		"intervalSeconds":       defaultOccupantIntervalSeconds,
		"runner":                p.runner,
		"requestedBy":           occupantRunSourceKind,
	}
	if p.repoURL != "" {
		payload["repoUrl"] = p.repoURL
		payload["baseBranch"] = p.baseBranch
		payload["openPullRequest"] = true
	}
	return json.Marshal(payload)
}

func validEpicID(raw string) (string, error) {
	epicID := strings.TrimSpace(raw)
	if epicID == "" {
		return "", newStatusError(http.StatusBadRequest, "invalid", "epicId is required", false)
	}
	if len(epicID) > maxOccupantEpicIDBytes {
		return "", newStatusError(http.StatusBadRequest, "invalid", "epicId exceeds 200 bytes", false)
	}
	return epicID, nil
}

func validOccupantRunner(raw string) (string, error) {
	runner := strings.TrimSpace(raw)
	if runner == "" {
		return occupantDefaultRunner, nil
	}
	for _, allowed := range allowedOccupantRunners {
		if runner == allowed {
			return runner, nil
		}
	}
	return "", newStatusError(http.StatusBadRequest, "invalid",
		fmt.Sprintf("runner %q is not allowed for occupant dispatch", runner), false)
}

func validMaxConcurrency(raw *int) (int, error) {
	if raw == nil {
		return defaultOccupantEpicConcurrency, nil
	}
	if *raw < 1 || *raw > maxOccupantEpicConcurrency {
		return 0, newStatusError(http.StatusBadRequest, "invalid",
			fmt.Sprintf("maxConcurrency must be between 1 and %d", maxOccupantEpicConcurrency), false)
	}
	return *raw, nil
}

func (m *Module) planEpicRun(ctx context.Context, ws string, id occupantIdentity,
	params epicRunDispatchParams,
) (epicRunPlan, error) {
	epicID, err := validEpicID(params.EpicID)
	if err != nil {
		return epicRunPlan{}, err
	}
	runner, err := validOccupantRunner(params.Runner)
	if err != nil {
		return epicRunPlan{}, err
	}
	concurrency, err := validMaxConcurrency(params.MaxConcurrency)
	if err != nil {
		return epicRunPlan{}, err
	}
	leadName, err := requirePlacementAgentName(id.node)
	if err != nil {
		return epicRunPlan{}, err
	}
	session, err := m.resolveLeadSession(ctx, ws, id.node)
	if err != nil {
		return epicRunPlan{}, dispatchSessionError(err)
	}
	epic, err := m.lookupEpic(ctx, epicID)
	if err != nil {
		return epicRunPlan{}, err
	}
	if err := m.assertNoLiveEpicRun(ctx, ws, epicID); err != nil {
		return epicRunPlan{}, err
	}
	repoURL, baseBranch, err := m.resolveEpicRepo(ctx, ws, epic)
	if err != nil {
		return epicRunPlan{}, err
	}
	return epicRunPlan{epicID: epicID, leadName: leadName, sessionID: session.SessionID,
		runner: runner, repoURL: repoURL, baseBranch: baseBranch, maxConcurrency: concurrency}, nil
}

func dispatchSessionError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return newStatusError(http.StatusConflict, "session_absent",
			"lead orchestration session not found for this placement; the lead runtime has not called session-ensure yet", false)
	}
	return err
}

func (m *Module) lookupEpic(ctx context.Context, epicID string) (*backend.IssueDetailData, error) {
	be := m.issueBackend(ctx)
	if be == nil {
		return nil, newStatusError(http.StatusServiceUnavailable, "unavailable", "issue backend unavailable", true)
	}
	epic, err := be.Get(ctx, epicID)
	if err != nil {
		return nil, epicLookupError(epicID, err)
	}
	if epic == nil {
		return nil, newStatusError(http.StatusNotFound, "not_found", "epic "+epicID+" was not found", false)
	}
	if issueType := strings.TrimSpace(epic.IssueType); issueType != "" && issueType != "epic" {
		return nil, newStatusError(http.StatusBadRequest, "invalid",
			fmt.Sprintf("issue %q has type %q; epic-run requires an epic", epicID, issueType), false)
	}
	return epic, nil
}

func epicLookupError(epicID string, err error) error {
	var backendErr *backend.BackendError
	if !errors.As(err, &backendErr) {
		return err
	}
	switch backendErr.Kind {
	case backend.KindNotFound:
		return newStatusError(http.StatusNotFound, "not_found", "epic "+epicID+" was not found", false)
	case backend.KindValidation:
		return newStatusError(http.StatusBadRequest, "invalid", "invalid epic id", false)
	case backend.KindUnavailable:
		return newStatusError(http.StatusServiceUnavailable, "unavailable", "issue backend unavailable", true)
	case backend.KindTimeout:
		return newStatusError(http.StatusGatewayTimeout, "timeout", "issue backend timed out", true)
	default:
		return fmt.Errorf("lookup epic: %w", err)
	}
}

func (m *Module) assertNoLiveEpicRun(ctx context.Context, ws, epicID string) error {
	runs, err := m.store.DriverRuns().List(ctx, ws, store.DriverRunFilter{EpicID: epicID})
	if err != nil {
		return fmt.Errorf("list driver runs for epic %q: %w", epicID, err)
	}
	for _, run := range runs {
		if run == nil || run.EpicID != epicID || run.Status.IsTerminal() {
			continue
		}
		return activeEpicRunError(epicID, run)
	}
	return nil
}

func activeEpicRunError(epicID string, run *domain.DriverRun) error {
	return newStatusError(http.StatusConflict, "epic_run_active",
		fmt.Sprintf("epic %s already has an active workflow run %s (%s)", epicID, run.RunID, run.Status), false)
}

// resolveEpicRunnerDriverID name-pins the operator-controlled active workflow
// named epic-runner. It does not digest-pin the embedded implementation.
func (m *Module) resolveEpicRunnerDriverID(ctx context.Context, ws string) (string, error) {
	name := workflowdefs.BuiltinEpicRunnerWorkflowName
	if driverID, err := workflowdefs.ResolveDriverID(ctx, m.store, ws, name); err == nil {
		return driverID, nil
	}
	if err := workflowdefs.EnsureBuiltinWorkflow(ctx, m.store, ws, name); err != nil {
		return "", fmt.Errorf("ensure builtin workflow %q: %w", name, err)
	}
	return workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
}

func (m *Module) createEpicRun(ctx context.Context, ws string, id occupantIdentity,
	plan epicRunPlan,
) (*domain.DriverRun, error) {
	driverID, err := m.resolveEpicRunnerDriverID(ctx, ws)
	if err != nil {
		return nil, err
	}
	payload, err := plan.payload()
	if err != nil {
		return nil, fmt.Errorf("encode epic-run payload: %w", err)
	}
	runID, err := mintOccupantRunID()
	if err != nil {
		return nil, fmt.Errorf("mint occupant run id: %w", err)
	}
	opts := driverpkg.RunOptions{
		RunID: runID, WorkspaceKey: ws, DriverID: driverID, EpicID: plan.epicID,
		SourceKind: occupantRunSourceKind,
		SourceRef:  leadtoken.OccupantActor(id.claims.PlacementID),
		Payload:    payload,
	}
	run, err := driverpkg.CreateDriverRun(ctx, m.store, opts)
	if err != nil {
		if got, getErr := m.store.DriverRuns().Get(ctx, ws, runID); getErr == nil && runMatchesRequest(got, opts) {
			return got, nil
		}
		return nil, fmt.Errorf("create occupant epic workflow run: %w", err)
	}
	if !runMatchesRequest(run, opts) {
		return nil, activeEpicRunError(plan.epicID, run)
	}
	return run, nil
}

func runMatchesRequest(run *domain.DriverRun, opts driverpkg.RunOptions) bool {
	return run != nil && run.RunID == opts.RunID &&
		run.SourceKind == opts.SourceKind && run.SourceRef == opts.SourceRef &&
		run.DriverID == opts.DriverID && run.EpicID == opts.EpicID
}

func mintOccupantRunID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("run-%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(random)), nil
}
