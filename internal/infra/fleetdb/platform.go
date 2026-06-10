package fleetdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverStore struct{ client *Client }

var _ store.DriverStore = (*driverStore)(nil)

func (s *driverStore) Create(ctx context.Context, in store.DriverCreate) (*domain.Driver, error) {
	body := map[string]any{
		"driver_id":         in.DriverID,
		"name":              in.Name,
		"owner_type":        in.OwnerType,
		"owner_ref":         in.OwnerRef,
		"description":       in.Description,
		"active_version_id": in.ActiveVersionID,
		"status":            in.Status,
		"metadata":          in.Metadata,
	}
	var out domain.Driver
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/drivers", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStore) Get(ctx context.Context, ws, driverID string) (*domain.Driver, error) {
	var out domain.Driver
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/drivers/"+pathEscape(driverID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStore) List(ctx context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	q := url.Values{}
	if filter.Name != "" {
		q.Set("name", filter.Name)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/drivers", q)
	var resp struct {
		Drivers []*domain.Driver `json:"drivers"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Drivers == nil {
		resp.Drivers = []*domain.Driver{}
	}
	return resp.Drivers, nil
}

func (s *driverStore) Update(ctx context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	var out domain.Driver
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/drivers/"+pathEscape(driverID), driverUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type driverVersionStore struct{ client *Client }

var _ store.DriverVersionStore = (*driverVersionStore)(nil)

func (s *driverVersionStore) Create(ctx context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	body := map[string]any{
		"version_id":        in.VersionID,
		"version":           in.Version,
		"source_ref":        in.SourceRef,
		"source_digest":     in.SourceDigest,
		"bundle_ref":        in.BundleRef,
		"bundle_digest":     in.BundleDigest,
		"runtime":           in.Runtime,
		"manifest":          in.Manifest,
		"build_diagnostics": in.BuildDiagnostics,
		"validation_status": in.ValidationStatus,
		"created_by":        in.CreatedBy,
	}
	var out domain.DriverVersion
	path := "/api/v1/" + pathEscape(in.WorkspaceKey) + "/drivers/" + pathEscape(in.DriverID) + "/versions"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverVersionStore) Get(ctx context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	var out domain.DriverVersion
	path := "/api/v1/" + pathEscape(ws) + "/driver-versions/" + pathEscape(versionID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverVersionStore) List(ctx context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	q := url.Values{}
	if filter.ValidationStatus != "" {
		q.Set("validation_status", string(filter.ValidationStatus))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/driver-versions"
	if filter.DriverID != "" {
		path = "/api/v1/" + pathEscape(ws) + "/drivers/" + pathEscape(filter.DriverID) + "/versions"
	}
	path = withQuery(path, q)
	var resp struct {
		DriverVersions []*domain.DriverVersion `json:"driver_versions"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.DriverVersions == nil {
		resp.DriverVersions = []*domain.DriverVersion{}
	}
	return resp.DriverVersions, nil
}

type triggerBindingStore struct{ client *Client }

var _ store.TriggerBindingStore = (*triggerBindingStore)(nil)

func (s *triggerBindingStore) Create(ctx context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	body := map[string]any{
		"binding_id":              in.BindingID,
		"name":                    in.Name,
		"source_kind":             in.SourceKind,
		"source_ref":              in.SourceRef,
		"source_config_ref":       in.SourceConfigRef,
		"route_key":               in.RouteKey,
		"method":                  in.Method,
		"path_template":           in.PathTemplate,
		"topic":                   in.Topic,
		"event_type_patterns":     in.EventTypePatterns,
		"filter_ref":              in.FilterRef,
		"driver_id":               in.DriverID,
		"driver_version_id":       in.DriverVersionID,
		"target_entrypoint":       in.TargetEntrypoint,
		"target_agent_service_id": in.TargetAgentServiceID,
		"concurrency_policy":      in.ConcurrencyPolicy,
		"idempotency_policy":      in.IdempotencyPolicy,
		"auth_policy":             in.AuthPolicy,
		"permissions":             in.Permissions,
		"enabled":                 in.Enabled,
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(in.WorkspaceKey) + "/trigger-bindings"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(ws) + "/trigger-bindings/" + pathEscape(bindingID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerBindingStore) GetByRouteKey(ctx context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	bindings, err := s.List(ctx, ws, store.TriggerBindingFilter{RouteKey: routeKey, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, domain.ErrNotFound
	}
	return bindings[0], nil
}

func (s *triggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	q := url.Values{}
	if filter.SourceKind != "" {
		q.Set("source_kind", filter.SourceKind)
	}
	if filter.RouteKey != "" {
		q.Set("route_key", filter.RouteKey)
	}
	if filter.DriverID != "" {
		q.Set("driver_id", filter.DriverID)
	}
	if filter.TargetAgentServiceID != "" {
		q.Set("target_agent_service_id", filter.TargetAgentServiceID)
	}
	if filter.Enabled != nil {
		q.Set("enabled", strconv.FormatBool(*filter.Enabled))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/trigger-bindings", q)
	var resp struct {
		TriggerBindings []*domain.TriggerBinding `json:"trigger_bindings"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerBindings == nil {
		resp.TriggerBindings = []*domain.TriggerBinding{}
	}
	return resp.TriggerBindings, nil
}

func (s *triggerBindingStore) Update(ctx context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(ws) + "/trigger-bindings/" + pathEscape(bindingID)
	if err := s.client.do(ctx, "PATCH", path, triggerBindingUpdateBody(patch), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type driverRunStore struct{ client *Client }

var _ store.DriverRunStore = (*driverRunStore)(nil)

func (s *driverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	body := map[string]any{
		"run_id":            in.RunID,
		"driver_id":         in.DriverID,
		"driver_version_id": in.DriverVersionID,
		"entrypoint":        in.Entrypoint,
		"source_kind":       in.SourceKind,
		"source_ref":        in.SourceRef,
		"epic_id":           in.EpicID,
		"idempotency_key":   in.IdempotencyKey,
		"payload":           in.Payload,
	}
	var out domain.DriverRun
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/driver-runs", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	body := map[string]any{
		"run_id":          in.RunID,
		"idempotency_key": in.IdempotencyKey,
		"payload":         in.Payload,
	}
	headers := map[string]string{}
	if in.IdempotencyKey != "" {
		headers["Idempotency-Key"] = in.IdempotencyKey
	}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/epics/" + pathEscape(epicID) + "/runs"
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Get(ctx context.Context, ws, runID string) (*domain.DriverRun, error) {
	var out domain.DriverRun
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/driver-runs/"+pathEscape(runID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Events(ctx context.Context, ws, runID, after string, limit int) (*domain.PlatformEventsPage, error) {
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/driver-runs/"+pathEscape(runID)+"/events", q)
	var out domain.PlatformEventsPage
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	if out.Events == nil {
		out.Events = []domain.PlatformEvent{}
	}
	return &out, nil
}

func (s *driverRunStore) List(ctx context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	q := url.Values{}
	if filter.DriverID != "" {
		q.Set("driver_id", filter.DriverID)
	}
	if filter.DriverVersionID != "" {
		q.Set("driver_version_id", filter.DriverVersionID)
	}
	if filter.EpicID != "" {
		q.Set("epic_id", filter.EpicID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/driver-runs", q)
	var resp struct {
		DriverRuns []*domain.DriverRun `json:"driver_runs"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.DriverRuns == nil {
		resp.DriverRuns = []*domain.DriverRun{}
	}
	return resp.DriverRuns, nil
}

func (s *driverRunStore) Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*domain.DriverRun, error) {
	body := map[string]string{"node_id": nodeID, "lease_id": leaseID}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/claim"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	body := map[string]any{"node_id": nodeID, "lease_id": leaseID, "fencing_token": fencingToken}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/heartbeat"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Finish(ctx context.Context, ws, runID string, finish store.DriverRunFinish) (*domain.DriverRun, error) {
	body := map[string]any{
		"node_id":       finish.NodeID,
		"lease_id":      finish.LeaseID,
		"fencing_token": finish.FencingToken,
		"status":        finish.Status,
		"summary":       finish.Summary,
		"error_class":   finish.ErrorClass,
		"output":        finish.Output,
	}
	var out domain.DriverRun
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/finish"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) RecoverStale(ctx context.Context, ws string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	type recoverStaleDriverRunsRequest struct {
		StaleBefore   *time.Time `json:"stale_before,omitempty"`
		MaxAgeSeconds int64      `json:"max_age_seconds,omitempty"`
		ErrorClass    string     `json:"error_class,omitempty"`
		Summary       string     `json:"summary,omitempty"`
		Limit         int        `json:"limit,omitempty"`
	}
	body := recoverStaleDriverRunsRequest{
		MaxAgeSeconds: recover.MaxAgeSeconds,
		ErrorClass:    recover.ErrorClass,
		Summary:       recover.Summary,
		Limit:         recover.Limit,
	}
	if !recover.StaleBefore.IsZero() {
		staleBefore := recover.StaleBefore.UTC()
		body.StaleBefore = &staleBefore
	}
	var out store.StaleDriverRunRecoveryResult
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/recover-stale"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) RecoverStaleTaskRuns(ctx context.Context, ws, runID string, recover store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, error) {
	type recoverStaleTaskRunsRequest struct {
		StaleBefore   *time.Time `json:"stale_before,omitempty"`
		MaxAgeSeconds int64      `json:"max_age_seconds,omitempty"`
		ErrorClass    string     `json:"error_class,omitempty"`
		ErrorMessage  string     `json:"error_message,omitempty"`
	}
	body := recoverStaleTaskRunsRequest{
		MaxAgeSeconds: recover.MaxAgeSeconds,
		ErrorClass:    recover.ErrorClass,
		ErrorMessage:  recover.ErrorMessage,
	}
	if !recover.StaleBefore.IsZero() {
		staleBefore := recover.StaleBefore.UTC()
		body.StaleBefore = &staleBefore
	}
	var out store.StaleTaskRunRecoveryResult
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/recover-stale-tasks"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type driverStepStore struct{ client *Client }

var _ store.DriverStepStore = (*driverStepStore)(nil)

func (s *driverStepStore) Create(ctx context.Context, in store.DriverStepCreate) (*domain.DriverStep, error) {
	var out domain.DriverStep
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/driver-steps", driverStepCreateBody(in), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	var out domain.DriverStep
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/steps"
	if err := s.client.do(ctx, "POST", path, driverStepCreateBody(in), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStepStore) Get(ctx context.Context, ws, stepID string) (*domain.DriverStep, error) {
	var out domain.DriverStep
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/driver-steps/"+pathEscape(stepID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStepStore) List(ctx context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	path := "/api/v1/" + pathEscape(ws) + "/driver-steps" + driverStepListQuery(filter, true)
	var resp struct {
		DriverSteps []*domain.DriverStep `json:"driver_steps"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.DriverSteps == nil {
		resp.DriverSteps = []*domain.DriverStep{}
	}
	return resp.DriverSteps, nil
}

func (s *driverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	path := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(runID) + "/steps" + driverStepListQuery(filter, false)
	var resp struct {
		DriverSteps []*domain.DriverStep `json:"driver_steps"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.DriverSteps == nil {
		resp.DriverSteps = []*domain.DriverStep{}
	}
	return resp.DriverSteps, nil
}

func (s *driverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	var out domain.DriverStep
	path := "/api/v1/" + pathEscape(ws) + "/driver-steps/" + pathEscape(stepID)
	if err := s.client.do(ctx, "PATCH", path, update, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func driverStepCreateBody(in store.DriverStepCreate) map[string]any {
	return map[string]any{
		"step_id":          in.StepID,
		"driver_run_id":    in.DriverRunID,
		"step_kind":        in.StepKind,
		"status":           in.Status,
		"task_run_id":      in.TaskRunID,
		"action_ledger_id": in.ActionLedgerID,
		"external_ref":     in.ExternalRef,
		"input_ref":        in.InputRef,
		"output_ref":       in.OutputRef,
		"started_at":       in.StartedAt,
		"ended_at":         in.EndedAt,
		"node_id":          in.NodeID,
		"lease_id":         in.LeaseID,
		"fencing_token":    in.FencingToken,
	}
}

func driverStepListQuery(filter store.DriverStepFilter, includeDriverRunID bool) string {
	q := url.Values{}
	if includeDriverRunID && filter.DriverRunID != "" {
		q.Set("driver_run_id", filter.DriverRunID)
	}
	if filter.TaskRunID != "" {
		q.Set("task_run_id", filter.TaskRunID)
	}
	if filter.ActionLedgerID != "" {
		q.Set("action_ledger_id", filter.ActionLedgerID)
	}
	if filter.StepKind != "" {
		q.Set("step_kind", filter.StepKind)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	if encoded := q.Encode(); encoded != "" {
		return "?" + encoded
	}
	return ""
}

// leaseTokenHeaders returns the X-Lease-Token header map for lease-fenced
// task-run operations; nil (no extra headers) when the token is unset.
func leaseTokenHeaders(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"X-Lease-Token": token}
}

type taskRunStore struct{ client *Client }

var _ store.TaskRunStore = (*taskRunStore)(nil)

func (s *taskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	body := map[string]any{
		"task_run_id":       in.TaskRunID,
		"driver_run_id":     in.DriverRunID,
		"driver_step_id":    in.DriverStepID,
		"task_id":           in.TaskID,
		"worker_profile_id": in.WorkerProfileID,
		"provider_profile":  in.ProviderProfile,
		"status":            in.Status,
		"node_id":           in.NodeID,
		"lease_id":          in.LeaseID,
		"fencing_token":     in.FencingToken,
		"runner_placement":  in.RunnerPlacement,
		"sandbox_placement": in.SandboxPlacement,
		"runtime_metadata":  in.RuntimeMetadata,
	}
	var out domain.TaskRun
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/task-runs", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) ClaimQueued(ctx context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	body := map[string]any{
		"task_run_id":         claim.TaskRunID,
		"node_id":             claim.NodeID,
		"runner_id":           claim.RunnerID,
		"lease_id":            claim.LeaseID,
		"supported_providers": claim.SupportedProviders,
		"capabilities":        claim.Capabilities,
		"worker_profile_ids":  claim.WorkerProfileIDs,
		"runner_placement":    claim.RunnerPlacement,
		"sandbox_placement":   claim.SandboxPlacement,
	}
	headers := leaseTokenHeaders(claim.LeaseToken)
	var out domain.TaskRun
	if err := s.client.doWithHeaders(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/task-runs/claim", body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	var out domain.TaskRun
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/task-runs/"+pathEscape(taskRunID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	q := url.Values{}
	if filter.DriverRunID != "" {
		q.Set("driver_run_id", filter.DriverRunID)
	}
	if filter.DriverStepID != "" {
		q.Set("driver_step_id", filter.DriverStepID)
	}
	if filter.TaskID != "" {
		q.Set("task_id", filter.TaskID)
	}
	if filter.WorkerProfileID != "" {
		q.Set("worker_profile_id", filter.WorkerProfileID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/task-runs", q)
	var resp struct {
		TaskRuns []*domain.TaskRun `json:"task_runs"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TaskRuns == nil {
		resp.TaskRuns = []*domain.TaskRun{}
	}
	return resp.TaskRuns, nil
}

func (s *taskRunStore) Finish(ctx context.Context, ws, taskRunID string, finish store.TaskRunFinish) (*domain.TaskRun, error) {
	body := map[string]any{
		"node_id":            finish.NodeID,
		"lease_id":           finish.LeaseID,
		"fencing_token":      finish.FencingToken,
		"status":             finish.Status,
		"exit_code":          finish.ExitCode,
		"logs_ref":           finish.LogsRef,
		"artifacts_ref":      finish.ArtifactsRef,
		"input_tokens":       finish.InputTokens,
		"output_tokens":      finish.OutputTokens,
		"cache_read_tokens":  finish.CacheReadTokens,
		"cache_write_tokens": finish.CacheWriteTokens,
		"estimated_cost_usd": finish.EstimatedCostUSD,
		"runtime_metadata":   finish.RuntimeMetadata,
		"error_class":        finish.ErrorClass,
		"error_message":      finish.ErrorMessage,
	}
	var out domain.TaskRun
	path := "/api/v1/" + pathEscape(ws) + "/task-runs/" + pathEscape(taskRunID) + "/finish"
	headers := leaseTokenHeaders(finish.LeaseToken)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) Heartbeat(ctx context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	body := map[string]any{
		"node_id":          heartbeat.NodeID,
		"lease_id":         heartbeat.LeaseID,
		"fencing_token":    heartbeat.FencingToken,
		"runtime_metadata": heartbeat.RuntimeMetadata,
		"logs_ref":         heartbeat.LogsRef,
		"artifacts_ref":    heartbeat.ArtifactsRef,
	}
	var out domain.TaskRun
	path := "/api/v1/" + pathEscape(ws) + "/task-runs/" + pathEscape(taskRunID) + "/heartbeat"
	headers := leaseTokenHeaders(heartbeat.LeaseToken)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) Complete(ctx context.Context, ws, taskRunID string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	body := map[string]any{
		"completion_id":         complete.CompletionID,
		"node_id":               complete.NodeID,
		"lease_id":              complete.LeaseID,
		"fencing_token":         complete.FencingToken,
		"status":                complete.Status,
		"exit_code":             complete.ExitCode,
		"logs_ref":              complete.LogsRef,
		"artifacts_ref":         complete.ArtifactsRef,
		"required_artifact_ids": complete.RequiredArtifactIDs,
		"require_artifacts":     complete.RequireArtifacts,
		"input_tokens":          complete.InputTokens,
		"output_tokens":         complete.OutputTokens,
		"cache_read_tokens":     complete.CacheReadTokens,
		"cache_write_tokens":    complete.CacheWriteTokens,
		"estimated_cost_usd":    complete.EstimatedCostUSD,
		"runtime_metadata":      complete.RuntimeMetadata,
		"error_class":           complete.ErrorClass,
		"error_message":         complete.ErrorMessage,
		"close_task":            complete.CloseTask,
		"close_reason":          complete.CloseReason,
	}
	var resp struct {
		TaskRun *domain.TaskRun `json:"task_run"`
	}
	path := "/api/v1/" + pathEscape(ws) + "/task-runs/" + pathEscape(taskRunID) + "/complete"
	headers := leaseTokenHeaders(complete.LeaseToken)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &resp, headers); err != nil {
		return nil, err
	}
	if resp.TaskRun == nil {
		return nil, fmt.Errorf("complete task run %q: empty task_run response", taskRunID)
	}
	return resp.TaskRun, nil
}

func (s *taskRunStore) AppendLog(ctx context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	body := map[string]any{
		"node_id":       appendLog.NodeID,
		"lease_id":      appendLog.LeaseID,
		"fencing_token": appendLog.FencingToken,
		"stream":        appendLog.Stream,
		"text":          appendLog.Text,
	}
	if !appendLog.Timestamp.IsZero() {
		body["timestamp"] = appendLog.Timestamp
	}
	var out domain.TaskRunLogEntry
	path := "/api/v1/" + pathEscape(ws) + "/task-runs/" + pathEscape(taskRunID) + "/logs"
	headers := leaseTokenHeaders(appendLog.LeaseToken)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) ListLogs(ctx context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	q := url.Values{}
	if filter.AfterSequence > 0 {
		q.Set("after_sequence", strconv.FormatInt(filter.AfterSequence, 10))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/task-runs/"+pathEscape(taskRunID)+"/logs", q)
	var resp struct {
		Logs []*domain.TaskRunLogEntry `json:"logs"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Logs == nil {
		resp.Logs = []*domain.TaskRunLogEntry{}
	}
	return resp.Logs, nil
}

func driverUpdateBody(patch store.DriverUpdate) map[string]any {
	body := map[string]any{}
	if patch.Name != nil {
		body["name"] = *patch.Name
	}
	if patch.OwnerType != nil {
		body["owner_type"] = *patch.OwnerType
	}
	if patch.OwnerRef != nil {
		body["owner_ref"] = *patch.OwnerRef
	}
	if patch.Description != nil {
		body["description"] = *patch.Description
	}
	if patch.ActiveVersionID != nil {
		body["active_version_id"] = *patch.ActiveVersionID
	}
	if patch.Status != nil {
		body["status"] = *patch.Status
	}
	if patch.Metadata != nil {
		body["metadata"] = *patch.Metadata
	}
	return body
}

func triggerBindingUpdateBody(patch store.TriggerBindingUpdate) map[string]any {
	body := map[string]any{}
	if patch.Name != nil {
		body["name"] = *patch.Name
	}
	if patch.SourceKind != nil {
		body["source_kind"] = *patch.SourceKind
	}
	if patch.SourceRef != nil {
		body["source_ref"] = *patch.SourceRef
	}
	if patch.SourceConfigRef != nil {
		body["source_config_ref"] = *patch.SourceConfigRef
	}
	if patch.RouteKey != nil {
		body["route_key"] = *patch.RouteKey
	}
	if patch.Method != nil {
		body["method"] = *patch.Method
	}
	if patch.PathTemplate != nil {
		body["path_template"] = *patch.PathTemplate
	}
	if patch.Topic != nil {
		body["topic"] = *patch.Topic
	}
	if patch.EventTypePatterns != nil {
		body["event_type_patterns"] = *patch.EventTypePatterns
	}
	if patch.FilterRef != nil {
		body["filter_ref"] = *patch.FilterRef
	}
	if patch.DriverID != nil {
		body["driver_id"] = *patch.DriverID
	}
	if patch.DriverVersionID != nil {
		body["driver_version_id"] = *patch.DriverVersionID
	}
	if patch.TargetEntrypoint != nil {
		body["target_entrypoint"] = *patch.TargetEntrypoint
	}
	if patch.TargetAgentServiceID != nil {
		body["target_agent_service_id"] = *patch.TargetAgentServiceID
	}
	if patch.ConcurrencyPolicy != nil {
		body["concurrency_policy"] = *patch.ConcurrencyPolicy
	}
	if patch.IdempotencyPolicy != nil {
		body["idempotency_policy"] = *patch.IdempotencyPolicy
	}
	if patch.AuthPolicy != nil {
		body["auth_policy"] = *patch.AuthPolicy
	}
	if patch.Permissions != nil {
		body["permissions"] = *patch.Permissions
	}
	if patch.Enabled != nil {
		body["enabled"] = *patch.Enabled
	}
	return body
}
