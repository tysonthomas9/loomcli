package fleetdb

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type definitionVersionStore struct{ client *Client }
type workflowDefinitionStore struct{ client *Client }
type workflowRunStore struct{ client *Client }
type taskRunStore struct{ client *Client }
type runEventStore struct{ client *Client }
type runtimeProfileStore struct{ client *Client }
type routeBindingStore struct{ client *Client }
type triggerBindingStore struct{ client *Client }

var (
	_ store.DefinitionVersionStore  = (*definitionVersionStore)(nil)
	_ store.WorkflowDefinitionStore = (*workflowDefinitionStore)(nil)
	_ store.WorkflowRunStore        = (*workflowRunStore)(nil)
	_ store.TaskRunStore            = (*taskRunStore)(nil)
	_ store.RunEventStore           = (*runEventStore)(nil)
	_ store.RuntimeProfileStore     = (*runtimeProfileStore)(nil)
	_ store.RouteBindingStore       = (*routeBindingStore)(nil)
	_ store.TriggerBindingStore     = (*triggerBindingStore)(nil)
)

func (s *definitionVersionStore) Apply(ctx context.Context, in store.DefinitionVersionApply) (*domain.DefinitionVersion, error) {
	body := map[string]any{
		"definition_type":     in.DefinitionType,
		"definition_name":     in.DefinitionName,
		"version":             in.Version,
		"source_hash":         in.SourceHash,
		"bundle_hash":         in.BundleHash,
		"manifest":            rawOrNil(in.Manifest),
		"capability_manifest": rawOrNil(in.CapabilityManifest),
		"created_by":          in.CreatedBy,
		"status":              in.Status,
	}
	var out domain.DefinitionVersion
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/definition-versions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *definitionVersionStore) Get(ctx context.Context, ws string, typ domain.DefinitionType, name, version string) (*domain.DefinitionVersion, error) {
	var out domain.DefinitionVersion
	path := "/api/v1/" + pathEscape(ws) + "/definition-versions/" + pathEscape(string(typ)) + "/" + pathEscape(name) + "/" + pathEscape(version)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *definitionVersionStore) List(ctx context.Context, ws string, filter store.DefinitionVersionFilter) ([]*domain.DefinitionVersion, error) {
	q := url.Values{}
	if filter.DefinitionType != "" {
		q.Set("definition_type", string(filter.DefinitionType))
	}
	if filter.DefinitionName != "" {
		q.Set("definition_name", filter.DefinitionName)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		DefinitionVersions []*domain.DefinitionVersion `json:"definition_versions"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/definition-versions", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.DefinitionVersions == nil {
		resp.DefinitionVersions = []*domain.DefinitionVersion{}
	}
	return resp.DefinitionVersions, nil
}

func (s *workflowDefinitionStore) Upsert(ctx context.Context, in store.WorkflowDefinitionUpsert) (*domain.WorkflowDefinition, error) {
	body := map[string]any{
		"name":                 in.Name,
		"version":              in.Version,
		"description":          in.Description,
		"input_schema":         rawOrNil(in.InputSchema),
		"result_schema":        rawOrNil(in.ResultSchema),
		"singleton_policy":     in.SingletonPolicy,
		"runtime_profile_name": in.RuntimeProfileName,
		"source_ref":           in.SourceRef,
		"bundle_hash":          in.BundleHash,
		"manifest":             rawOrNil(in.Manifest),
		"capability_manifest":  rawOrNil(in.CapabilityManifest),
		"status":               in.Status,
	}
	var out domain.WorkflowDefinition
	if err := s.client.do(ctx, "PUT", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/workflow-definitions/"+pathEscape(in.Name), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workflowDefinitionStore) Get(ctx context.Context, ws, name string) (*domain.WorkflowDefinition, error) {
	var out domain.WorkflowDefinition
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/workflow-definitions/"+pathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workflowDefinitionStore) List(ctx context.Context, ws string, filter store.WorkflowDefinitionFilter) ([]*domain.WorkflowDefinition, error) {
	q := url.Values{}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		WorkflowDefinitions []*domain.WorkflowDefinition `json:"workflow_definitions"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/workflow-definitions", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.WorkflowDefinitions == nil {
		resp.WorkflowDefinitions = []*domain.WorkflowDefinition{}
	}
	return resp.WorkflowDefinitions, nil
}

func (s *workflowRunStore) CreateOrResume(ctx context.Context, in store.WorkflowRunCreate) (*domain.WorkflowRun, error) {
	body := map[string]any{
		"run_id":           in.RunID,
		"workflow_name":    in.WorkflowName,
		"workflow_version": in.WorkflowVersion,
		"bundle_hash":      in.BundleHash,
		"idempotency_key":  in.IdempotencyKey,
		"input":            rawOrNil(in.Input),
		"status":           in.Status,
		"lease_owner":      in.LeaseOwner,
		"lease_token":      in.LeaseToken,
		"started_at":       in.StartedAt,
	}
	var out domain.WorkflowRun
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/workflow-runs", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workflowRunStore) Get(ctx context.Context, ws, runID string) (*domain.WorkflowRun, error) {
	var out domain.WorkflowRun
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/workflow-runs/"+pathEscape(runID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workflowRunStore) List(ctx context.Context, ws string, filter store.WorkflowRunFilter) ([]*domain.WorkflowRun, error) {
	q := url.Values{}
	if filter.WorkflowName != "" {
		q.Set("workflow_name", filter.WorkflowName)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.IdempotencyKey != "" {
		q.Set("idempotency_key", filter.IdempotencyKey)
	}
	if filter.Live {
		q.Set("live", "true")
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		WorkflowRuns []*domain.WorkflowRun `json:"workflow_runs"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/workflow-runs", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.WorkflowRuns == nil {
		resp.WorkflowRuns = []*domain.WorkflowRun{}
	}
	return resp.WorkflowRuns, nil
}

func (s *workflowRunStore) Update(ctx context.Context, ws, runID string, patch store.WorkflowRunUpdate) (*domain.WorkflowRun, error) {
	var out domain.WorkflowRun
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/workflow-runs/"+pathEscape(runID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) Ensure(ctx context.Context, in store.TaskRunEnsure) (*domain.TaskRun, error) {
	body := map[string]any{
		"task_run_id":       in.TaskRunID,
		"idempotency_key":   in.IdempotencyKey,
		"workflow_run_id":   in.WorkflowRunID,
		"work_item_id":      in.WorkItemID,
		"role_name":         in.RoleName,
		"claim_actor":       in.ClaimActor,
		"claim_event_id":    in.ClaimEventID,
		"status":            in.Status,
		"agent_id":          in.AgentID,
		"node_id":           in.NodeID,
		"command_id":        in.CommandID,
		"session_id":        in.SessionID,
		"lease_id":          in.LeaseID,
		"parent_session_id": in.ParentSessionID,
		"reason":            in.Reason,
		"metadata":          in.Metadata,
	}
	var out domain.TaskRun
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/task-runs/ensure", body, &out); err != nil {
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
	if filter.WorkflowRunID != "" {
		q.Set("workflow_run_id", filter.WorkflowRunID)
	}
	if filter.WorkItemID != "" {
		q.Set("work_item_id", filter.WorkItemID)
	}
	if filter.RoleName != "" {
		q.Set("role_name", filter.RoleName)
	}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Live {
		q.Set("live", "true")
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		TaskRuns []*domain.TaskRun `json:"task_runs"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/task-runs", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.TaskRuns == nil {
		resp.TaskRuns = []*domain.TaskRun{}
	}
	return resp.TaskRuns, nil
}

func (s *taskRunStore) Update(ctx context.Context, ws, taskRunID string, patch store.TaskRunUpdate) (*domain.TaskRun, error) {
	var out domain.TaskRun
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/task-runs/"+pathEscape(taskRunID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *runEventStore) Append(ctx context.Context, in store.RunEventAppend) (*domain.RunEvent, error) {
	body := map[string]any{
		"event_id":        in.EventID,
		"workflow_run_id": in.WorkflowRunID,
		"task_run_id":     in.TaskRunID,
		"type":            in.Type,
		"message":         in.Message,
		"data":            rawOrNil(in.Data),
	}
	var out domain.RunEvent
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/workflow-runs/"+pathEscape(in.WorkflowRunID)+"/events", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *runEventStore) List(ctx context.Context, ws string, filter store.RunEventFilter) ([]*domain.RunEvent, error) {
	q := url.Values{}
	if filter.TaskRunID != "" {
		q.Set("task_run_id", filter.TaskRunID)
	}
	if filter.AfterIndex > 0 {
		q.Set("after_index", strconv.FormatInt(filter.AfterIndex, 10))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/run-events"
	if filter.WorkflowRunID != "" {
		path = "/api/v1/" + pathEscape(ws) + "/workflow-runs/" + pathEscape(filter.WorkflowRunID) + "/events"
	}
	var resp struct {
		RunEvents []*domain.RunEvent `json:"run_events"`
	}
	if err := s.client.do(ctx, "GET", withQuery(path, q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.RunEvents == nil {
		resp.RunEvents = []*domain.RunEvent{}
	}
	return resp.RunEvents, nil
}

func (s *runtimeProfileStore) Upsert(ctx context.Context, in store.RuntimeProfileUpsert) (*domain.RuntimeProfile, error) {
	body := map[string]any{
		"name":     in.Name,
		"version":  in.Version,
		"provider": in.Provider,
		"image":    in.Image,
		"repos":    in.Repos,
		"env":      in.Env,
		"cpu":      in.CPU,
		"memory":   in.Memory,
		"manifest": rawOrNil(in.Manifest),
		"status":   in.Status,
	}
	var out domain.RuntimeProfile
	if err := s.client.do(ctx, "PUT", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/runtime-profiles/"+pathEscape(in.Name), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *runtimeProfileStore) Get(ctx context.Context, ws, name string) (*domain.RuntimeProfile, error) {
	var out domain.RuntimeProfile
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/runtime-profiles/"+pathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *runtimeProfileStore) List(ctx context.Context, ws string, filter store.RuntimeProfileFilter) ([]*domain.RuntimeProfile, error) {
	q := url.Values{}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		RuntimeProfiles []*domain.RuntimeProfile `json:"runtime_profiles"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/runtime-profiles", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.RuntimeProfiles == nil {
		resp.RuntimeProfiles = []*domain.RuntimeProfile{}
	}
	return resp.RuntimeProfiles, nil
}

func (s *routeBindingStore) Upsert(ctx context.Context, in store.RouteBindingUpsert) (*domain.RouteBinding, error) {
	body := map[string]any{
		"binding_id":      in.BindingID,
		"definition_name": in.DefinitionName,
		"definition_type": in.DefinitionType,
		"path":            in.Path,
		"method":          in.Method,
		"auth_policy":     in.AuthPolicy,
		"status":          in.Status,
	}
	var out domain.RouteBinding
	if err := s.client.do(ctx, "PUT", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/route-bindings/"+pathEscape(in.BindingID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *routeBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.RouteBinding, error) {
	var out domain.RouteBinding
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/route-bindings/"+pathEscape(bindingID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *routeBindingStore) List(ctx context.Context, ws string, filter store.RouteBindingFilter) ([]*domain.RouteBinding, error) {
	q := url.Values{}
	if filter.DefinitionName != "" {
		q.Set("definition_name", filter.DefinitionName)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		RouteBindings []*domain.RouteBinding `json:"route_bindings"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/route-bindings", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.RouteBindings == nil {
		resp.RouteBindings = []*domain.RouteBinding{}
	}
	return resp.RouteBindings, nil
}

func (s *triggerBindingStore) Upsert(ctx context.Context, in store.TriggerBindingUpsert) (*domain.TriggerBinding, error) {
	body := map[string]any{
		"binding_id":    in.BindingID,
		"workflow_name": in.WorkflowName,
		"event_type":    in.EventType,
		"filter":        rawOrNil(in.Filter),
		"status":        in.Status,
	}
	var out domain.TriggerBinding
	if err := s.client.do(ctx, "PUT", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/trigger-bindings/"+pathEscape(in.BindingID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	var out domain.TriggerBinding
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/trigger-bindings/"+pathEscape(bindingID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	q := url.Values{}
	if filter.WorkflowName != "" {
		q.Set("workflow_name", filter.WorkflowName)
	}
	if filter.EventType != "" {
		q.Set("event_type", filter.EventType)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		TriggerBindings []*domain.TriggerBinding `json:"trigger_bindings"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/trigger-bindings", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerBindings == nil {
		resp.TriggerBindings = []*domain.TriggerBinding{}
	}
	return resp.TriggerBindings, nil
}

func withQuery(path string, q url.Values) string {
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func rawOrNil(in []byte) any {
	if len(in) == 0 {
		return nil
	}
	return json.RawMessage(in)
}
