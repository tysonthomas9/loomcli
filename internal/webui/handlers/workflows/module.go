package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxRunPayloadBytes = 4 << 20

const (
	defaultRunsLimit = handler.DefaultRunsLimit
	maxRunsLimit     = handler.MaxRunsLimit
)

type Module struct {
	store             workflowProjectionStore
	catalog           workflowcatalog.API
	catalogRead       workflowCatalogDriverReader
	authoring         workflowcatalog.VersionAuthoringAPI
	catalogAuthority  workflowcataloghttp.OperatorAuthorityResolver
	prepareTarget     func(context.Context, string, string) (*workflowcatalog.Driver, error)
	execution         execution.DriverRunAPI
	operatorAuthority workflowcataloghttp.OperatorAuthorityResolver
	taskWorkflowRuns  readprojection.TaskWorkflowRunReader
	backendHealth     BackendHealthQuery
}

type workflowProjectionStore interface {
	DriverRuns() store.DriverRunStore
	DriverSteps() store.DriverStepStore
	TaskRuns() store.TaskRunStore
	TriggerBindings() store.TriggerBindingStore
}

type workflowCatalogDriverReader interface {
	GetDriver(context.Context, string, string) (*workflowcatalog.Driver, error)
}

type Config struct {
	Store                    workflowProjectionStore
	Catalog                  workflowcatalog.API
	Authoring                workflowcatalog.VersionAuthoringAPI
	CatalogOperatorAuthority workflowcataloghttp.OperatorAuthorityResolver
	PrepareWorkflowTarget    func(context.Context, string, string) (*workflowcatalog.Driver, error)
	Execution                execution.DriverRunAPI
	OperatorAuthority        workflowcataloghttp.OperatorAuthorityResolver
	TaskWorkflowRuns         readprojection.TaskWorkflowRunReader
	BackendHealth            BackendHealthQuery
}

func NewModule(config Config) *Module {
	return &Module{
		store: config.Store, catalog: config.Catalog, catalogRead: config.Catalog, authoring: config.Authoring,
		catalogAuthority: config.CatalogOperatorAuthority, prepareTarget: config.PrepareWorkflowTarget,
		execution:         config.Execution,
		operatorAuthority: config.OperatorAuthority, taskWorkflowRuns: config.TaskWorkflowRuns,
		backendHealth: config.BackendHealth,
	}
}

func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows", m.listWorkflows)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions", m.createWorkflowVersion)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflow-catalog/native-drivers", m.registerNativeDriver)
	// Builtin source/build/run behavior remains in this compatibility module.
	// Registered-driver reads and version lifecycle commands are owned by the
	// Workflow Catalog capability module and registered separately by app/serve.
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/{name}/source", m.getWorkflowSource)
	// Run history for a workflow: a thin, read-only view over DriverRunStore.List
	// so the UI can show past/active runs (Phase 1). Unlike the run/version
	// mutation paths it must not self-heal a driver, so it uses ResolveDriver.
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/{name}/runs", m.listWorkflowRuns)
	// Task-scoped workflow runs supplement task sessions when an automation has
	// no AgentSession row yet (for example, a repository admission guard or a
	// queued pre-session TaskRun). The handler joins through the TriggerEvent's
	// immutable subject_ref rather than inspecting workflow-defined payloads.
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/workflow-runs", m.listTaskWorkflowRuns)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}", m.createWorkflowRun)
	mux.HandleFunc("POST /api/workspaces/{ws}/execution/driver-runs", m.createDriverRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}", m.getRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/events", m.getRunEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/stream", m.streamRunEvents)
}

type createDriverRunRequest struct {
	CLICommand      string          `json:"cli_command"`
	DriverRef       string          `json:"driver_ref"`
	DriverVersionID string          `json:"driver_version_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Entrypoint      string          `json:"entrypoint,omitempty"`
	EpicID          string          `json:"epic_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

func decodeDriverRunSubmission(w http.ResponseWriter, r *http.Request) (createDriverRunRequest, string, string, bool) {
	var request createDriverRunRequest
	err := handler.DecodeOneJSON(w, r, &request, handler.JSONDecodeOptions{
		MaxBytes: maxRunPayloadBytes, DisallowUnknownFields: true,
	})
	if errors.Is(err, handler.ErrTrailingJSON) {
		writeError(w, http.StatusBadRequest, "DriverRun submission must contain exactly one JSON object")
		return createDriverRunRequest{}, "", "", false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid DriverRun submission JSON")
		return createDriverRunRequest{}, "", "", false
	}
	workspace := strings.TrimSpace(r.PathValue("ws"))
	driverRef := strings.TrimSpace(request.DriverRef)
	if workspace == "" || driverRef == "" {
		writeError(w, http.StatusBadRequest, "workspace and driver_ref are required")
		return createDriverRunRequest{}, "", "", false
	}
	return request, workspace, driverRef, true
}

// createDriverRun is the authenticated standalone-CLI submission boundary.
// It resolves driver/version reads through Workflow Catalog and delegates the
// only mutation to Execution; no CLI caller opens Store or mints operator
// authority locally.
func (m *Module) createDriverRun(w http.ResponseWriter, r *http.Request) {
	request, workspace, driverRef, ok := decodeDriverRunSubmission(w, r)
	if !ok {
		return
	}
	if m.catalog == nil || m.execution == nil || m.operatorAuthority == nil {
		writeDomainError(w, execution.ErrUnavailable, "Workflow Catalog and Execution submission capabilities are unavailable")
		return
	}
	sourceRef, err := driverRunCLISourceRef(request.CLICommand)
	if err != nil {
		writeDomainError(w, err, "invalid DriverRun CLI provenance")
		return
	}
	driverTarget, version, ok := m.resolveDriverRunSubmissionTarget(w, r, workspace, driverRef, sourceRef, request.DriverVersionID)
	if !ok {
		return
	}
	payload, ok := m.prepareDriverRunPayload(w, r, workspace, sourceRef, driverTarget, request.Payload)
	if !ok {
		return
	}
	auth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, execution.ActionSubmitDriverRun)
	if err != nil {
		writeDomainError(w, err, "resolve DriverRun submit authority failed")
		return
	}
	runID, requestID, entrypoint := driverRunSubmissionIdentity(r, workspace, driverTarget.DriverID, request)
	sourceRef, epicID := m.driverRunSourceAndEpic(r.Context(), workspace, driverTarget.DriverID, sourceRef, request.EpicID, payload)
	snapshot, err := m.execution.SubmitDriverRun(r.Context(), auth, execution.SubmitDriverRunCommand{
		WorkspaceKey: workspace, RequestID: requestID, RunID: runID,
		DriverID: driverTarget.DriverID, DriverVersionID: version.VersionID,
		Entrypoint: entrypoint, SourceKind: "cli", SourceRef: sourceRef,
		EpicID:  epicID,
		Payload: payload,
	})
	if err != nil {
		writeDomainError(w, err, "submit DriverRun failed")
		return
	}
	run, err := driver.LegacyDriverRunSnapshot(snapshot)
	if err != nil {
		writeDomainError(w, err, "map DriverRun submission result failed")
		return
	}
	handler.WriteJSON(w, http.StatusAccepted, run)
}

func (m *Module) driverRunSourceAndEpic(
	ctx context.Context,
	workspace, driverID, sourceRef, epicID string,
	payload json.RawMessage,
) (string, string) {
	if sourceRef == "loom workflow run" {
		sourceRef = m.resolveManualWorkflowRunSourceRef(ctx, workspace, driverID)
	}
	epicID = strings.TrimSpace(epicID)
	if epicID == "" {
		epicID = driver.DriverRunPayloadEpicID(payload)
	}
	return sourceRef, epicID
}

func (m *Module) prepareDriverRunPayload(
	w http.ResponseWriter,
	r *http.Request,
	workspace, sourceRef string,
	driverTarget *workflowcatalog.Driver,
	raw json.RawMessage,
) (json.RawMessage, bool) {
	payload := append(json.RawMessage(nil), raw...)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		writeError(w, http.StatusBadRequest, "payload must be valid JSON")
		return nil, false
	}
	if !workflowSubmissionPreparesBuiltin(sourceRef) {
		return payload, true
	}
	workflowName := strings.TrimSpace(driverTarget.Name)
	if workflowName == "" {
		workflowName = strings.TrimSpace(driverTarget.DriverID)
	}
	if err := m.preflightRunnerForRun(r.Context(), workspace, workflowName, payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return payload, true
}

func driverRunSubmissionIdentity(
	r *http.Request,
	workspace, driverID string,
	request createDriverRunRequest,
) (string, string, string) {
	runID := strings.TrimSpace(request.RunID)
	requestID := strings.TrimSpace(request.IdempotencyKey)
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if runID == "" && requestID != "" {
		runID = workflowSubmissionRunID(workspace, driverID, requestID)
	}
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	if requestID == "" {
		requestID = runID
	}
	entrypoint := strings.TrimSpace(request.Entrypoint)
	if entrypoint == "" {
		entrypoint = driver.EntrypointRun
	}
	return runID, requestID, entrypoint
}

func (m *Module) resolveDriverRunSubmissionTarget(
	w http.ResponseWriter,
	r *http.Request,
	workspace, driverRef, sourceRef, requestedVersionID string,
) (*workflowcatalog.Driver, *workflowcatalog.DriverVersion, bool) {
	driverTarget, err := m.catalog.GetDriver(r.Context(), workspace, driverRef)
	if errors.Is(err, workflowcatalog.ErrNotFound) && workflowSubmissionPreparesBuiltin(sourceRef) &&
		workflowdefs.IsBuiltinWorkflow(driverRef) && m.store != nil {
		// Workflow distribution remains a bounded Phase-5 compatibility lane.
		// Preserve the established builtin self-heal here, then return to the
		// public Catalog read and typed Execution mutation boundaries.
		if _, prepareErr := m.resolveWorkflowTarget(r.Context(), workspace, driverRef); prepareErr != nil {
			writeDomainError(w, prepareErr, "prepare builtin DriverRun target failed")
			return nil, nil, false
		}
		driverTarget, err = m.catalog.GetDriver(r.Context(), workspace, driverRef)
	}
	if err != nil {
		writeDomainError(w, err, "resolve DriverRun target failed")
		return nil, nil, false
	}
	if driverTarget == nil {
		writeDomainError(w, workflowcatalog.ErrInvalidPersistedState, "Workflow Catalog returned no DriverRun target")
		return nil, nil, false
	}

	versionID := strings.TrimSpace(requestedVersionID)
	var version *workflowcatalog.DriverVersion
	if versionID != "" {
		requested, ok := m.resolveRequestedDriverRunVersion(w, r, workspace, driverRef, versionID)
		if !ok {
			return nil, nil, false
		}
		driverTarget = requested.Driver
		version = requested.Version
	} else {
		if driverTarget.Status != workflowcatalog.DriverStatusActive {
			writeDomainError(w, domain.ErrInvalid, "DriverRun target is not active")
			return nil, nil, false
		}
		version, err = m.catalog.GetVersion(r.Context(), workspace, strings.TrimSpace(driverTarget.ActiveVersionID))
		if err != nil {
			writeDomainError(w, err, "resolve DriverRun version failed")
			return nil, nil, false
		}
	}
	if version == nil || version.DriverID != driverTarget.DriverID ||
		version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		writeDomainError(w, domain.ErrInvalid, "DriverRun version is not a passed version for the target driver")
		return nil, nil, false
	}
	return driverTarget, version, true
}

func (m *Module) resolveRequestedDriverRunVersion(
	w http.ResponseWriter,
	r *http.Request,
	workspace, driverRef, versionID string,
) (*workflowcatalog.RequestedVersion, bool) {
	catalogAuth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, workflowcatalog.ActionResolveRequestedVersion)
	if err != nil {
		writeDomainError(w, err, "resolve requested DriverRun version authority failed")
		return nil, false
	}
	requested, err := m.catalog.ResolveRequestedVersion(r.Context(), catalogAuth, workspace, driverRef, versionID)
	if err != nil {
		writeDomainError(w, err, "resolve requested DriverRun version failed")
		return nil, false
	}
	if requested == nil || requested.Driver == nil || requested.Version == nil {
		writeDomainError(w, workflowcatalog.ErrInvalidPersistedState, "Workflow Catalog returned no requested DriverRun version")
		return nil, false
	}
	return requested, true
}

func driverRunCLISourceRef(command string) (string, error) {
	switch strings.TrimSpace(command) {
	case "driver-run":
		return "loom driver run", nil
	case "workflow-run":
		return "loom workflow run", nil
	case "epic-run":
		return "loom epic run", nil
	default:
		return "", fmt.Errorf("unknown cli_command %q: %w", command, domain.ErrInvalid)
	}
}

func workflowSubmissionPreparesBuiltin(sourceRef string) bool {
	switch strings.TrimSpace(sourceRef) {
	case "loom workflow run", "loom epic run":
		return true
	default:
		return false
	}
}

// resolveManualWorkflowRunSourceRef preserves connector grant resolution for
// `loom workflow run`: a bound workflow stamps its binding route key; an
// unbound workflow retains the plain CLI provenance label.
func (m *Module) resolveManualWorkflowRunSourceRef(ctx context.Context, workspace, driverID string) string {
	if m == nil || m.store == nil {
		return "loom workflow run"
	}
	bindings, err := m.store.TriggerBindings().List(ctx, workspace, store.TriggerBindingFilter{DriverID: driverID, Limit: 1})
	if err == nil && len(bindings) > 0 && bindings[0] != nil && strings.TrimSpace(bindings[0].RouteKey) != "" {
		return bindings[0].RouteKey
	}
	return "loom workflow run"
}

type workflowSummary struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
}

// listWorkflows returns the workflows that can be started or bound to a
// trigger. Today that is the built-in catalog (epic-runner, github-review-agent,
// …); exposing it de-hardcodes the frontend, which previously only knew the
// epic-runner name.
func (m *Module) listWorkflows(w http.ResponseWriter, r *http.Request) {
	names := workflowdefs.BuiltinWorkflowNames()
	out := make([]workflowSummary, 0, len(names))
	for _, name := range names {
		out = append(out, workflowSummary{Name: name, Builtin: true})
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

// getWorkflowSource returns a builtin workflow's TS source files so the UI can
// show/seed an editor. Only builtins are available: custom driver versions
// persist a bundle + digest, not source text, so a non-builtin name is a 404
// rather than a fake empty editor.
func (m *Module) getWorkflowSource(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workflow name is required")
		return
	}
	spec, ok := workflowdefs.BuiltinWorkflow(name)
	if !ok {
		handler.RespondError(w, http.StatusNotFound, "no builtin source available for workflow "+name)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"name":       name,
		"builtin":    true,
		"entrypoint": spec.Entrypoint,
		"files":      spec.Files,
	})
}

// listWorkflowRuns returns a workflow's run history, newest first, over
// DriverRunStore.List. It resolves the workflow through Workflow Catalog
// without self-healing or registering a driver, so an unregistered workflow is
// a 404. The generic-store fallback preserves the documented read-only/test
// compatibility constructor and performs no mutation.
func (m *Module) listWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		writeError(w, http.StatusBadRequest, "workspace and workflow name are required")
		return
	}
	status, ok := parseRunStatusFilter(w, r)
	if !ok {
		return
	}
	limit, ok := handler.ParseRunLimit(w, r)
	if !ok {
		return
	}
	drv, err := m.resolveWorkflowDriverRead(r.Context(), ws, name)
	if err != nil {
		writeDomainError(w, err, "resolve workflow driver failed")
		return
	}
	// Both backends now order newest-first by StartedAt BEFORE applying the
	// limit (fleet-db server-side; memstore in store.DriverRuns().List), so the
	// limit can be pushed down — it returns the newest-by-StartedAt window
	// rather than dropping runs that belong in it. The client-side sort/truncate
	// below stays as defense in depth against an unordered backend.
	runs, err := m.store.DriverRuns().List(r.Context(), ws, store.DriverRunFilter{
		DriverID: drv.DriverID,
		Status:   status,
		Limit:    limit,
	})
	if err != nil {
		writeDomainError(w, err, "list workflow runs failed")
		return
	}
	runs = handler.SortAndTrim(runs, limit)
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"driver_id":         drv.DriverID,
		"active_version_id": drv.ActiveVersionID,
		"runs":              runs,
	})
}

func (m *Module) resolveWorkflowDriverRead(
	ctx context.Context,
	workspace, name string,
) (*workflowcatalog.Driver, error) {
	if m == nil || m.catalogRead == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return m.catalogRead.GetDriver(ctx, workspace, name)
}

// parseRunStatusFilter reads the optional ?status= filter and validates it
// against the known DriverRunStatus values. On an unknown status it writes a
// 400 and returns ok=false; an empty filter is allowed (no status constraint).
func parseRunStatusFilter(w http.ResponseWriter, r *http.Request) (domain.DriverRunStatus, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("status"))
	if raw == "" {
		return "", true
	}
	status := domain.DriverRunStatus(raw)
	if !isKnownRunStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status: "+raw)
		return "", false
	}
	return status, true
}

func isKnownRunStatus(s domain.DriverRunStatus) bool {
	switch s {
	case domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunCompleted,
		domain.DriverRunFailed, domain.DriverRunNeedsReview, domain.DriverRunCancelled,
		domain.DriverRunSuspendedAwaitingEvent:
		return true
	default:
		return false
	}
}

type createWorkflowVersionRequest struct {
	Files      map[string]string         `json:"files"`
	Entrypoint string                    `json:"entrypoint,omitempty"`
	Activate   *bool                     `json:"activate,omitempty"`
	Runners    []driver.DriverRunnerSpec `json:"runners,omitempty"`
	Manifest   map[string]string         `json:"manifest,omitempty"`
}

type workflowVersionInput struct {
	entrypoint string
	files      map[string]string
	runners    []driver.DriverRunnerSpec
	manifest   map[string]string
}

// parseCreateWorkflowVersionRequest decodes and validates the request body
// for createWorkflowVersion. On failure it writes the HTTP error response
// itself and returns ok=false.
func parseCreateWorkflowVersionRequest(w http.ResponseWriter, r *http.Request, name string) (workflowVersionInput, bool) {
	var in workflowVersionInput
	var req createWorkflowVersionRequest
	if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: maxRunPayloadBytes}); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return in, false
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "files is required")
		return in, false
	}
	in.entrypoint = strings.TrimSpace(req.Entrypoint)
	if in.entrypoint == "" {
		in.entrypoint = filepath.ToSlash(filepath.Join("workflows", name+".ts"))
	}
	if err := workflowdefs.ValidateWorkflowEntrypoint(name, in.entrypoint); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	files, err := workflowdefs.ValidateWorkflowFiles(req.Files)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	if _, ok := files[in.entrypoint]; !ok {
		writeError(w, http.StatusBadRequest, "entrypoint file is missing")
		return in, false
	}
	if req.Activate != nil && *req.Activate {
		writeError(w, http.StatusBadRequest, "activate=true is not supported; approve and activate the version through the workflow catalog lifecycle API")
		return in, false
	}
	if err := driver.ValidateDriverRunnerSpecs(req.Runners); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	if _, present := req.Manifest[driver.ManifestTrustLevelKey]; present {
		writeError(w, http.StatusBadRequest, "manifest trust_level is server-owned")
		return in, false
	}
	in.files = files
	in.runners = driver.NormalizeDriverRunnerSpecs(req.Runners)
	in.manifest = req.Manifest
	return in, true
}

func (m *Module) createWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if ws == "" {
		writeError(w, http.StatusBadRequest, "canonical workspace is required")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "workflow name is required")
		return
	}
	in, ok := parseCreateWorkflowVersionRequest(w, r, name)
	if !ok {
		return
	}
	if m.catalog == nil || m.authoring == nil || m.catalogAuthority == nil {
		writeDomainError(w, workflowcatalog.ErrUnavailable, "Workflow Catalog authoring is unavailable")
		return
	}
	catalogAuth, err := m.catalogAuthority.ResolveOperatorAuthority(r, ws, workflowcatalog.ActionAuthorVersion)
	if err != nil {
		writeDomainError(w, err, "resolve Workflow Catalog authoring authority failed")
		return
	}
	expectedRevision, ok := m.workflowVersionExpectedRevision(w, r, ws, name)
	if !ok {
		return
	}
	coordinator, err := appworkflowauthoring.New(workflowdefs.NewBundleStager())
	if err != nil {
		writeDomainError(w, err, "compose workflow authoring failed")
		return
	}
	result, buildOutput, err := coordinator.AuthorOperator(r.Context(), m.authoring, catalogAuth, appworkflowauthoring.BuildOptions{
		WorkspaceKey:     ws,
		Name:             name,
		Entrypoint:       in.entrypoint,
		Files:            in.files,
		Runners:          applicationRunnerSpecs(in.runners),
		Manifest:         in.manifest,
		RequestID:        strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		ExpectedRevision: expectedRevision,
		// HTTP submission only builds and registers. Approval and activation
		// cross the Workflow Catalog lifecycle command boundary explicitly.
		Activate: false,
	})
	if err != nil {
		writeDomainError(w, err, "author workflow version failed")
		return
	}
	writeWorkflowVersionAuthoringResult(w, result, buildOutput)
}

func writeWorkflowVersionAuthoringResult(
	w http.ResponseWriter,
	result *appworkflowauthoring.Result,
	buildOutput string,
) {
	handler.WriteJSON(w, http.StatusCreated, map[string]any{
		"driver":            result.Driver,
		"version":           result.Version,
		"bundle":            result.Bundle,
		"created_driver":    result.CreatedDriver,
		"created_version":   result.CreatedVersion,
		"reused_version":    result.ReusedVersion,
		"activated":         result.Activated,
		"build_diagnostics": buildOutput,
	})
}

func (m *Module) workflowVersionExpectedRevision(
	w http.ResponseWriter,
	r *http.Request,
	workspace,
	name string,
) (uint64, bool) {
	existing, err := m.catalog.GetDriver(r.Context(), workspace, name)
	switch {
	case err == nil && existing != nil:
		if existing.Revision == 0 {
			writeDomainError(w, workflowcatalog.ErrInvalidPersistedState, "Workflow Catalog returned a driver without a durable revision")
			return 0, false
		}
		return existing.Revision, true
	case err == nil:
		writeDomainError(w, workflowcatalog.ErrInvalidPersistedState, "Workflow Catalog returned no driver")
		return 0, false
	case errors.Is(err, workflowcatalog.ErrNotFound):
		return 0, true
	default:
		writeDomainError(w, err, "resolve Workflow Catalog authoring target failed")
		return 0, false
	}
}

func applicationRunnerSpecs(
	input []driver.DriverRunnerSpec,
) []appworkflowauthoring.RunnerSpec {
	output := make([]appworkflowauthoring.RunnerSpec, 0, len(input))
	for _, runner := range input {
		output = append(output, appworkflowauthoring.RunnerSpec{
			Name: runner.Name, Kind: runner.Kind, Entrypoint: runner.Entrypoint,
		})
	}
	return output
}

func (m *Module) createWorkflowRun(w http.ResponseWriter, r *http.Request) {
	payload, err := readRawJSONBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	target, err := m.resolveWorkflowTarget(r.Context(), ws, name)
	if err != nil {
		slog.Error("createWorkflowRun: resolveWorkflowDriverID failed", "ws", ws, "workflow", name, "err", err.Error())
		// A genuine not-found is the ONLY case that is a 404 "workflow not found".
		// Any other cause (builtin self-heal failure, a fleet-db rejection, a
		// bundling error) must surface its real status/message instead of being
		// collapsed into a misleading generic 500 "workflow not found", which
		// hides the true failure in the serve log alone.
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeDomainError(w, err, err.Error())
		return
	}
	// Fail-closed BEFORE creating the run: when the epic-runner routes child
	// task runs to the local task runner, the resolved backend CLI must be
	// installed and authenticated, else the run would fail deep in the worker.
	if err := m.preflightRunnerForRun(r.Context(), ws, name, payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := m.submitWorkflowRun(r, ws, target, payload)
	if err != nil {
		writeDomainError(w, err, "create workflow run failed")
		return
	}
	handler.WriteJSON(w, http.StatusAccepted, run)
}

func (m *Module) submitWorkflowRun(r *http.Request, workspace string, target *workflowcatalog.Driver, payload json.RawMessage) (*domain.DriverRun, error) {
	if target == nil {
		return nil, domain.ErrNotFound
	}
	if m.execution == nil {
		return nil, fmt.Errorf("execution submit API is unavailable: %w", execution.ErrUnavailable)
	}
	if m.operatorAuthority == nil {
		return nil, fmt.Errorf("execution operator authority is unavailable: %w", execution.ErrUnavailable)
	}
	auth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, execution.ActionSubmitDriverRun)
	if err != nil {
		return nil, err
	}
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	runID := fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	if requestID == "" {
		requestID = runID
	} else {
		// A client can lose the accepted response and retry later. Bind the
		// persisted identity to the stable request key so the replay presents
		// the exact same RunID to Execution instead of conflicting with the
		// already-created run.
		runID = workflowSubmissionRunID(workspace, target.DriverID, requestID)
	}
	snapshot, err := m.execution.SubmitDriverRun(r.Context(), auth, execution.SubmitDriverRunCommand{
		WorkspaceKey: workspace, RequestID: requestID, RunID: runID,
		DriverID: target.DriverID, DriverVersionID: target.ActiveVersionID,
		Entrypoint: driver.EntrypointRun, SourceKind: "api", SourceRef: r.URL.Path,
		EpicID: driver.DriverRunPayloadEpicID(payload), Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	return driver.LegacyDriverRunSnapshot(snapshot)
}

func workflowSubmissionRunID(workspace, targetID, requestID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"loom-workflow-submit",
		strings.TrimSpace(workspace),
		strings.TrimSpace(targetID),
		strings.TrimSpace(requestID),
	}, "\x00")))
	return "run-" + hex.EncodeToString(digest[:16])
}

func (m *Module) resolveWorkflowDriverID(ctx context.Context, ws, name string) (string, error) {
	drv, err := m.resolveWorkflowTarget(ctx, ws, name)
	if err != nil {
		return "", err
	}
	return drv.DriverID, nil
}

func (m *Module) resolveWorkflowTarget(ctx context.Context, ws, name string) (*workflowcatalog.Driver, error) {
	if m.prepareTarget == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return m.prepareTarget(ctx, ws, name)
}

func (m *Module) getRun(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	run, err := m.store.DriverRuns().Get(r.Context(), ws, r.PathValue("runId"))
	if err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	steps, err := m.runStepSummaries(r.Context(), ws, run.RunID)
	if err != nil {
		writeDomainError(w, err, "run steps unavailable")
		return
	}
	handler.WriteJSON(w, http.StatusOK, runDetailResponse{DriverRun: run, Steps: steps})
}

type runDetailResponse struct {
	*domain.DriverRun
	Steps []runStepSummary `json:"steps,omitempty"`
}

type runStepSummary struct {
	ID        string                  `json:"id"`
	StepKind  string                  `json:"step_kind"`
	TaskRunID string                  `json:"task_run_id,omitempty"`
	TaskID    string                  `json:"task_id,omitempty"`
	Status    domain.DriverStepStatus `json:"status"`
}

func (m *Module) runStepSummaries(ctx context.Context, ws, runID string) ([]runStepSummary, error) {
	steps, err := m.store.DriverSteps().ListForRun(ctx, ws, runID, store.DriverStepFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]runStepSummary, 0, len(steps))
	for _, step := range steps {
		if step == nil {
			continue
		}
		summary := runStepSummary{
			ID:        step.StepID,
			StepKind:  step.StepKind,
			TaskRunID: step.TaskRunID,
			Status:    step.Status,
		}
		if step.TaskRunID != "" {
			// Task IDs are best-effort enrichment: older or partial stores may have
			// the step before the task-run row is readable, but the step link itself
			// should still reach the UI.
			if taskRun, err := m.store.TaskRuns().Get(ctx, ws, step.TaskRunID); err == nil && taskRun != nil {
				summary.TaskID = taskRun.TaskID
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

func (m *Module) getRunEvents(w http.ResponseWriter, r *http.Request) {
	page, err := m.loadRunEvents(r.Context(), r, 100)
	if err != nil {
		writeDomainError(w, err, "run events unavailable")
		return
	}
	handler.WriteJSON(w, http.StatusOK, page)
}

func (m *Module) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "run event stream is unavailable for this store")
		return
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(r.Context(), ws, runID); err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after == "" {
		after = "0"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, err := reader.Events(r.Context(), ws, runID, after, 100)
		if err != nil {
			writeSSE(w, "error", map[string]string{"error": err.Error()})
			flusher.Flush()
			return
		}
		for _, event := range page.Events {
			writeSSE(w, "event", event)
		}
		if page.Cursor != "" {
			after = page.Cursor
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) loadRunEvents(ctx context.Context, r *http.Request, defaultLimit int) (*domain.PlatformEventsPage, error) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		return nil, store.ErrDriverRunEventsUnavailable
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(ctx, ws, runID); err != nil {
		return nil, err
	}
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			return nil, fmt.Errorf("invalid limit: %w", domain.ErrInvalid)
		}
		limit = parsed
	}
	return reader.Events(ctx, ws, runID, strings.TrimSpace(r.URL.Query().Get("after")), limit)
}

func readRawJSONBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("payload must be valid JSON")
	}
	out := make(json.RawMessage, len(body))
	copy(out, body)
	return out, nil
}

func writeSSE(w io.Writer, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	handler.WriteJSON(w, status, map[string]string{"error": message})
}

// writeDomainError handles this package's one extra sentinel (event reads on a
// store without event support → 501), then defers to the shared domain mapper.
func writeDomainError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrDriverRunEventsUnavailable):
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}
	if classification, ok := handler.ClassifyAuthenticationAuthorityError(err); ok {
		message := "authentication required"
		if classification.Status == http.StatusForbidden {
			message = "forbidden"
		}
		writeError(w, classification.Status, message)
		return
	}
	switch {
	case errors.Is(err, workflowcatalog.ErrWrongWorkspace):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, execution.ErrInvalid),
		errors.Is(err, execution.ErrPreflightFailed),
		errors.Is(err, execution.ErrUnschedulable),
		errors.Is(err, workflowcatalog.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, execution.ErrNotFound),
		errors.Is(err, workflowcatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, fallback)
	case errors.Is(err, execution.ErrConflict),
		errors.Is(err, execution.ErrFenceConflict),
		errors.Is(err, execution.ErrInvalidTransition),
		errors.Is(err, workflowcatalog.ErrVersionOwnership),
		errors.Is(err, workflowcatalog.ErrStaleRevision),
		errors.Is(err, workflowcatalog.ErrAuthoringConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, workflowcatalog.ErrVersionNotValidated),
		errors.Is(err, workflowcatalog.ErrVersionNotApproved):
		writeError(w, http.StatusPreconditionFailed, err.Error())
	case errors.Is(err, execution.ErrUnavailable),
		errors.Is(err, workflowcatalog.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, fallback)
	case errors.Is(err, workflowcatalog.ErrInvalidPersistedState):
		writeError(w, http.StatusBadGateway, fallback)
	default:
		handler.WriteDomainError(w, err, fallback)
	}
}
