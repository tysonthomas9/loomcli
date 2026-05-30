package defs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func PlanFromWorkspace(ctx context.Context, st store.Store, workspaceKey string) (*Plan, error) {
	if st == nil {
		return nil, fmt.Errorf("store not configured")
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return nil, fmt.Errorf("workspace key required")
	}
	plan := &Plan{Root: "workspace:" + workspaceKey}
	index, err := activeDefinitionIndex(ctx, st, workspaceKey)
	if err != nil {
		return nil, err
	}
	plan.Skills = index.skills
	plan.Tools = index.tools
	plan.Agents = index.agents
	plan.Workflows = index.workflows
	plan.Runtimes = index.runtimes
	if err := appendControlPlaneRoles(ctx, st, workspaceKey, plan, index.hasAgent); err != nil {
		return nil, err
	}
	if err := appendControlPlaneAgentInstances(ctx, st, workspaceKey, plan); err != nil {
		return nil, err
	}
	if err := appendControlPlaneAgentSessions(ctx, st, workspaceKey, plan); err != nil {
		return nil, err
	}
	if err := appendControlPlaneWorkflows(ctx, st, workspaceKey, plan, index.hasWorkflow); err != nil {
		return nil, err
	}
	if err := appendControlPlaneRuntimes(ctx, st, workspaceKey, plan, index.hasRuntime); err != nil {
		return nil, err
	}
	if err := appendControlPlaneWorkflowRuns(ctx, st, workspaceKey, plan); err != nil {
		return nil, err
	}
	if err := appendControlPlaneTaskRuns(ctx, st, workspaceKey, plan); err != nil {
		return nil, err
	}
	if err := applyWorkspaceWorkflowBindings(ctx, st, workspaceKey, plan); err != nil {
		return nil, err
	}
	sortPlan(plan)
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

type workspaceDefinitionIndex struct {
	agents      []AgentModule
	workflows   []WorkflowModule
	runtimes    []RuntimeModule
	skills      []SkillModule
	tools       []ToolModule
	hasAgent    map[string]bool
	hasWorkflow map[string]bool
	hasRuntime  map[string]bool
}

func activeDefinitionIndex(ctx context.Context, st store.Store, workspaceKey string) (workspaceDefinitionIndex, error) {
	out := workspaceDefinitionIndex{
		hasAgent:    map[string]bool{},
		hasWorkflow: map[string]bool{},
		hasRuntime:  map[string]bool{},
	}
	versions, err := st.DefinitionVersions().List(ctx, workspaceKey, store.DefinitionVersionFilter{Status: domain.DefinitionStatusActive})
	if err != nil {
		return out, fmt.Errorf("list definition versions: %w", err)
	}
	for _, version := range latestDefinitionVersions(versions) {
		if err := addDefinitionVersionToPlan(&out, version); err != nil {
			return out, err
		}
	}
	return out, nil
}

func latestDefinitionVersions(versions []*domain.DefinitionVersion) []*domain.DefinitionVersion {
	latest := make(map[string]*domain.DefinitionVersion, len(versions))
	for _, version := range versions {
		if version == nil {
			continue
		}
		key := string(version.DefinitionType) + ":" + version.DefinitionName
		if existing := latest[key]; existing != nil && !definitionVersionNewer(version, existing) {
			continue
		}
		latest[key] = version
	}
	out := make([]*domain.DefinitionVersion, 0, len(latest))
	for _, version := range latest {
		out = append(out, version)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DefinitionType == out[j].DefinitionType {
			return out[i].DefinitionName < out[j].DefinitionName
		}
		return out[i].DefinitionType < out[j].DefinitionType
	})
	return out
}

func definitionVersionNewer(left, right *domain.DefinitionVersion) bool {
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	return left.Version > right.Version
}

func addDefinitionVersionToPlan(index *workspaceDefinitionIndex, version *domain.DefinitionVersion) error {
	switch version.DefinitionType {
	case domain.DefinitionTypeAgent, domain.DefinitionTypeLead:
		agent, err := agentFromDefinitionVersion(version)
		if err != nil {
			return err
		}
		index.agents = append(index.agents, agent)
		index.hasAgent[agent.Name] = true
	case domain.DefinitionTypeWorkflow:
		workflow, err := workflowFromDefinitionVersion(version)
		if err != nil {
			return err
		}
		index.workflows = append(index.workflows, workflow)
		index.hasWorkflow[workflow.Name] = true
	case domain.DefinitionTypeRuntime:
		runtime, err := runtimeFromDefinitionVersion(version)
		if err != nil {
			return err
		}
		index.runtimes = append(index.runtimes, runtime)
		index.hasRuntime[runtime.Name] = true
	case domain.DefinitionTypeSkill:
		skill, err := skillFromDefinitionVersion(version)
		if err != nil {
			return err
		}
		index.skills = append(index.skills, skill)
	case domain.DefinitionTypeTool:
		tool, err := toolFromDefinitionVersion(version)
		if err != nil {
			return err
		}
		index.tools = append(index.tools, tool)
	}
	return nil
}

func agentFromDefinitionVersion(version *domain.DefinitionVersion) (AgentModule, error) {
	var agent AgentModule
	if err := json.Unmarshal(version.Manifest, &agent); err != nil {
		return agent, fmt.Errorf("decode agent definition %s: %w", version.DefinitionName, err)
	}
	agent.Name = firstNonEmpty(agent.Name, version.DefinitionName)
	agent.Version = firstNonEmpty(agent.Version, version.Version)
	agent.SourcePath = firstNonEmpty(agent.SourcePath, workspaceSourceRef(version))
	agent.SourceHash = firstNonEmpty(agent.SourceHash, version.SourceHash, version.BundleHash, workspaceHash(agent))
	return agent, nil
}

func workflowFromDefinitionVersion(version *domain.DefinitionVersion) (WorkflowModule, error) {
	var workflow WorkflowModule
	if err := json.Unmarshal(version.Manifest, &workflow); err != nil {
		return workflow, fmt.Errorf("decode workflow definition %s: %w", version.DefinitionName, err)
	}
	workflow.Name = firstNonEmpty(workflow.Name, version.DefinitionName)
	workflow.Version = firstNonEmpty(workflow.Version, version.Version)
	workflow.SourcePath = firstNonEmpty(workflow.SourcePath, workspaceSourceRef(version))
	workflow.SourceHash = firstNonEmpty(workflow.SourceHash, version.SourceHash, version.BundleHash, workspaceHash(workflow))
	return workflow, nil
}

func runtimeFromDefinitionVersion(version *domain.DefinitionVersion) (RuntimeModule, error) {
	var runtime RuntimeModule
	if err := json.Unmarshal(version.Manifest, &runtime); err != nil {
		return runtime, fmt.Errorf("decode runtime definition %s: %w", version.DefinitionName, err)
	}
	runtime.Name = firstNonEmpty(runtime.Name, version.DefinitionName)
	runtime.Version = firstNonEmpty(runtime.Version, version.Version)
	runtime.SourcePath = firstNonEmpty(runtime.SourcePath, workspaceSourceRef(version))
	runtime.SourceHash = firstNonEmpty(runtime.SourceHash, version.SourceHash, version.BundleHash, workspaceHash(runtime))
	return runtime, nil
}

func skillFromDefinitionVersion(version *domain.DefinitionVersion) (SkillModule, error) {
	var skill SkillModule
	if err := json.Unmarshal(version.Manifest, &skill); err != nil {
		return skill, fmt.Errorf("decode skill definition %s: %w", version.DefinitionName, err)
	}
	skill.Name = firstNonEmpty(skill.Name, version.DefinitionName)
	skill.Version = firstNonEmpty(skill.Version, version.Version)
	skill.SourcePath = firstNonEmpty(skill.SourcePath, workspaceSourceRef(version))
	skill.SourceHash = firstNonEmpty(skill.SourceHash, version.SourceHash, version.BundleHash, workspaceHash(skill))
	return skill, nil
}

func toolFromDefinitionVersion(version *domain.DefinitionVersion) (ToolModule, error) {
	var tool ToolModule
	if err := json.Unmarshal(version.Manifest, &tool); err != nil {
		return tool, fmt.Errorf("decode tool definition %s: %w", version.DefinitionName, err)
	}
	tool.Name = firstNonEmpty(tool.Name, version.DefinitionName)
	tool.Version = firstNonEmpty(tool.Version, version.Version)
	tool.SourcePath = firstNonEmpty(tool.SourcePath, workspaceSourceRef(version))
	tool.SourceHash = firstNonEmpty(tool.SourceHash, version.SourceHash, version.BundleHash, workspaceHash(tool))
	return tool, nil
}

func appendControlPlaneRoles(ctx context.Context, st store.Store, workspaceKey string, plan *Plan, skip map[string]bool) error {
	roles, err := st.Roles().List(ctx, workspaceKey)
	if err != nil {
		return fmt.Errorf("list roles: %w", err)
	}
	for _, role := range roles {
		if role == nil || skip[role.Name] {
			continue
		}
		plan.Agents = append(plan.Agents, agentFromRole(role))
	}
	return nil
}

func agentFromRole(role *domain.Role) AgentModule {
	maxConcurrency := 0
	if role.MaxConcurrency != nil {
		maxConcurrency = *role.MaxConcurrency
	}
	agent := AgentModule{
		Name:           role.Name,
		Description:    role.Description,
		Backend:        role.Backend,
		Model:          role.Model,
		SourcePath:     "control-plane:role/" + role.Name,
		Instructions:   role.PromptFile,
		Skills:         compactStrings(role.Skills),
		Tools:          compactStrings(role.AllowedTools),
		DeniedCommands: compactStrings(role.DeniedTools),
		MaxConcurrency: maxConcurrency,
		MaxBudgetUSD:   role.MaxBudgetUSD,
		ReadOnly:       role.ReadOnly,
	}
	agent.SourceHash = workspaceHash(agent)
	agent.Version = version(agent.SourceHash)
	return agent
}

func appendControlPlaneAgentInstances(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	agents, err := st.Agents().List(ctx, workspaceKey)
	if err != nil {
		return fmt.Errorf("list agent instances: %w", err)
	}
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		plan.AgentInstances = append(plan.AgentInstances, agentInstanceFromControlPlane(agent))
	}
	return nil
}

func agentInstanceFromControlPlane(agent *domain.Agent) AgentInstanceModule {
	instance := AgentInstanceModule{
		Name:             agent.Name,
		RoleName:         agent.RoleName,
		SourcePath:       "control-plane:agent/" + agent.Name,
		Auto:             agent.Auto,
		Backend:          agent.Backend,
		FallbackBackends: compactStrings(agent.FallbackBackends),
		Repos:            compactStrings(agent.Repos),
		RepoGroups:       compactStrings(agent.RepoGroups),
		CrossRepo:        agent.CrossRepo,
		Parent:           agent.Parent,
		State:            agent.State,
		Mode:             agent.Mode,
		TaskFilter:       agent.TaskFilter,
		MaxConcurrency:   agent.MaxConcurrency,
		BudgetPolicy:     agent.BudgetPolicy,
		DesiredState:     agent.DesiredState,
	}
	instance.SourceHash = workspaceHash(instance)
	instance.Version = version(instance.SourceHash)
	return instance
}

func appendControlPlaneAgentSessions(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	sessionStore := st.AgentSessions()
	if sessionStore == nil {
		return fmt.Errorf("agent session store not configured")
	}
	sessions, err := sessionStore.List(ctx, workspaceKey, store.AgentSessionFilter{Limit: 10000})
	if err != nil {
		return fmt.Errorf("list agent sessions: %w", err)
	}
	for _, session := range sessions {
		if session == nil {
			continue
		}
		plan.AgentSessions = append(plan.AgentSessions, agentSessionFromControlPlane(session))
	}
	return nil
}

func agentSessionFromControlPlane(session *domain.AgentSession) AgentSessionModule {
	module := AgentSessionModule{
		SessionID:       session.SessionID,
		AgentID:         session.AgentID,
		SourcePath:      "control-plane:agent-session/" + session.SessionID,
		NodeID:          session.NodeID,
		Kind:            session.Kind,
		TaskID:          session.TaskID,
		TerminalID:      session.TerminalID,
		ParentSessionID: session.ParentSessionID,
		Status:          session.Status,
		Phase:           session.Phase,
		Attempt:         session.Attempt,
		Summary:         session.Summary,
		ErrorClass:      session.ErrorClass,
		ExitCode:        cloneInt(session.ExitCode),
		Metadata:        cloneStringMap(session.Metadata),
	}
	if !session.LastHeartbeat.IsZero() {
		lastHeartbeat := session.LastHeartbeat
		module.LastHeartbeat = &lastHeartbeat
	}
	module.FinishedAt = cloneWorkflowRunTime(session.FinishedAt)
	module.SourceHash = workspaceHash(module)
	module.Version = version(module.SourceHash)
	return module
}

func appendControlPlaneWorkflows(ctx context.Context, st store.Store, workspaceKey string, plan *Plan, skip map[string]bool) error {
	definitions, err := st.WorkflowDefinitions().List(ctx, workspaceKey, store.WorkflowDefinitionFilter{Status: domain.DefinitionStatusActive})
	if err != nil {
		return fmt.Errorf("list workflow definitions: %w", err)
	}
	for _, definition := range definitions {
		if definition == nil || skip[definition.Name] {
			continue
		}
		workflow, err := workflowFromDefinition(definition)
		if err != nil {
			return err
		}
		plan.Workflows = append(plan.Workflows, workflow)
	}
	return nil
}

func workflowFromDefinition(definition *domain.WorkflowDefinition) (WorkflowModule, error) {
	workflow := WorkflowModule{}
	if len(definition.Manifest) > 0 {
		if err := json.Unmarshal(definition.Manifest, &workflow); err != nil {
			return workflow, fmt.Errorf("decode workflow manifest %s: %w", definition.Name, err)
		}
	}
	workflow.Name = firstNonEmpty(workflow.Name, definition.Name)
	workflow.Description = firstNonEmpty(workflow.Description, definition.Description)
	workflow.Version = firstNonEmpty(workflow.Version, definition.Version)
	workflow.SourcePath = firstNonEmpty(workflow.SourcePath, definition.SourceRef, "control-plane:workflow/"+definition.Name)
	workflow.SingletonPolicy = firstNonEmpty(workflow.SingletonPolicy, definition.SingletonPolicy)
	workflow.SourceHash = firstNonEmpty(workflow.SourceHash, definition.BundleHash, workspaceHash(workflow))
	return workflow, nil
}

func appendControlPlaneRuntimes(ctx context.Context, st store.Store, workspaceKey string, plan *Plan, skip map[string]bool) error {
	profiles, err := st.RuntimeProfiles().List(ctx, workspaceKey, store.RuntimeProfileFilter{Status: domain.DefinitionStatusActive})
	if err != nil {
		return fmt.Errorf("list runtime profiles: %w", err)
	}
	for _, profile := range profiles {
		if profile == nil || skip[profile.Name] {
			continue
		}
		runtime, err := runtimeFromProfile(profile)
		if err != nil {
			return err
		}
		plan.Runtimes = append(plan.Runtimes, runtime)
	}
	return nil
}

func runtimeFromProfile(profile *domain.RuntimeProfile) (RuntimeModule, error) {
	runtime := RuntimeModule{}
	if len(profile.Manifest) > 0 {
		if err := json.Unmarshal(profile.Manifest, &runtime); err != nil {
			return runtime, fmt.Errorf("decode runtime manifest %s: %w", profile.Name, err)
		}
	}
	runtime.Name = firstNonEmpty(runtime.Name, profile.Name)
	runtime.Version = firstNonEmpty(runtime.Version, profile.Version)
	runtime.SourcePath = firstNonEmpty(runtime.SourcePath, "control-plane:runtime/"+profile.Name)
	runtime.Provider = profile.Provider
	runtime.Image = firstNonEmpty(runtime.Image, profile.Image)
	runtime.Repos = compactStrings(append(runtime.Repos, profile.Repos...))
	runtime.Env = compactStrings(append(runtime.Env, profile.Env...))
	runtime.CPU = firstNonEmpty(runtime.CPU, profile.CPU)
	runtime.Memory = firstNonEmpty(runtime.Memory, profile.Memory)
	runtime.SourceHash = firstNonEmpty(runtime.SourceHash, workspaceHash(runtime))
	return runtime, nil
}

func appendControlPlaneWorkflowRuns(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	runStore := st.WorkflowRuns()
	if runStore == nil {
		return fmt.Errorf("workflow run store not configured")
	}
	runs, err := runStore.List(ctx, workspaceKey, store.WorkflowRunFilter{Limit: 10000})
	if err != nil {
		return fmt.Errorf("list workflow runs: %w", err)
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		plan.WorkflowRuns = append(plan.WorkflowRuns, workflowRunFromControlPlane(run))
	}
	return nil
}

func workflowRunFromControlPlane(run *domain.WorkflowRun) WorkflowRunModule {
	module := WorkflowRunModule{
		RunID:           run.RunID,
		WorkflowName:    run.WorkflowName,
		WorkflowVersion: run.WorkflowVersion,
		SourcePath:      "control-plane:workflow-run/" + run.RunID,
		BundleHash:      run.BundleHash,
		IdempotencyKey:  run.IdempotencyKey,
		Input:           cloneWorkflowRunRaw(run.Input),
		Status:          run.Status,
		Result:          cloneWorkflowRunRaw(run.Result),
		ErrorClass:      run.ErrorClass,
		ErrorMessage:    run.ErrorMessage,
		WaitCondition:   run.WaitCondition,
		LeaseOwner:      run.LeaseOwner,
		LeaseToken:      run.LeaseToken,
		FencingToken:    run.FencingToken,
		FinishedAt:      cloneWorkflowRunTime(run.FinishedAt),
	}
	if !run.StartedAt.IsZero() {
		startedAt := run.StartedAt
		module.StartedAt = &startedAt
	}
	module.SourceHash = workspaceHash(module)
	module.Version = version(module.SourceHash)
	return module
}

func appendControlPlaneTaskRuns(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	taskRunStore := st.TaskRuns()
	if taskRunStore == nil {
		return fmt.Errorf("task run store not configured")
	}
	runs, err := taskRunStore.List(ctx, workspaceKey, store.TaskRunFilter{Limit: 10000})
	if err != nil {
		return fmt.Errorf("list task runs: %w", err)
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		plan.TaskRuns = append(plan.TaskRuns, taskRunFromControlPlane(run))
	}
	return nil
}

func taskRunFromControlPlane(run *domain.TaskRun) TaskRunModule {
	module := TaskRunModule{
		TaskRunID:       run.TaskRunID,
		IdempotencyKey:  run.IdempotencyKey,
		WorkflowRunID:   run.WorkflowRunID,
		WorkItemID:      run.WorkItemID,
		RoleName:        run.RoleName,
		SourcePath:      "control-plane:task-run/" + run.TaskRunID,
		ClaimActor:      run.ClaimActor,
		ClaimEventID:    run.ClaimEventID,
		Status:          run.Status,
		AgentID:         run.AgentID,
		NodeID:          run.NodeID,
		CommandID:       run.CommandID,
		SessionID:       run.SessionID,
		LeaseID:         run.LeaseID,
		ParentSessionID: run.ParentSessionID,
		Reason:          run.Reason,
		ErrorClass:      run.ErrorClass,
		ErrorMessage:    run.ErrorMessage,
		Metadata:        cloneStringMap(run.Metadata),
	}
	if !run.StartedAt.IsZero() {
		startedAt := run.StartedAt
		module.StartedAt = &startedAt
	}
	module.FinishedAt = cloneWorkflowRunTime(run.FinishedAt)
	module.SourceHash = workspaceHash(module)
	module.Version = version(module.SourceHash)
	return module
}

func applyWorkspaceWorkflowBindings(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	for i := range plan.Workflows {
		workflow := &plan.Workflows[i]
		if err := applyWorkspaceRouteBinding(ctx, st, workspaceKey, workflow); err != nil {
			return err
		}
		if err := applyWorkspaceTriggerBinding(ctx, st, workspaceKey, workflow); err != nil {
			return err
		}
	}
	return nil
}

func applyWorkspaceRouteBinding(ctx context.Context, st store.Store, workspaceKey string, workflow *WorkflowModule) error {
	routes, err := st.RouteBindings().List(ctx, workspaceKey, store.RouteBindingFilter{
		DefinitionName: workflow.Name,
		Status:         domain.DefinitionStatusActive,
		Limit:          1,
	})
	if err != nil {
		return fmt.Errorf("list route bindings for %s: %w", workflow.Name, err)
	}
	if len(routes) == 0 {
		return nil
	}
	workflow.RoutePath = routes[0].Path
	workflow.RouteAuth = routes[0].AuthPolicy
	return nil
}

func applyWorkspaceTriggerBinding(ctx context.Context, st store.Store, workspaceKey string, workflow *WorkflowModule) error {
	triggers, err := st.TriggerBindings().List(ctx, workspaceKey, store.TriggerBindingFilter{
		WorkflowName: workflow.Name,
		Status:       domain.DefinitionStatusActive,
		Limit:        1,
	})
	if err != nil {
		return fmt.Errorf("list trigger bindings for %s: %w", workflow.Name, err)
	}
	if len(triggers) == 0 {
		return nil
	}
	workflow.TriggerEvent = triggers[0].EventType
	workflow.TriggerFilter = stringMapFromRaw(triggers[0].Filter)
	return nil
}

func stringMapFromRaw(raw json.RawMessage) map[string]string {
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	out := make(map[string]string, len(generic))
	for key, value := range generic {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func sortPlan(plan *Plan) {
	sort.Slice(plan.Skills, func(i, j int) bool { return plan.Skills[i].Name < plan.Skills[j].Name })
	sort.Slice(plan.Tools, func(i, j int) bool { return plan.Tools[i].Name < plan.Tools[j].Name })
	sort.Slice(plan.Agents, func(i, j int) bool { return plan.Agents[i].Name < plan.Agents[j].Name })
	sort.Slice(plan.AgentInstances, func(i, j int) bool { return plan.AgentInstances[i].Name < plan.AgentInstances[j].Name })
	sort.Slice(plan.AgentSessions, func(i, j int) bool { return plan.AgentSessions[i].SessionID < plan.AgentSessions[j].SessionID })
	sort.Slice(plan.Workflows, func(i, j int) bool { return plan.Workflows[i].Name < plan.Workflows[j].Name })
	sort.Slice(plan.WorkflowRuns, func(i, j int) bool { return plan.WorkflowRuns[i].RunID < plan.WorkflowRuns[j].RunID })
	sort.Slice(plan.TaskRuns, func(i, j int) bool { return plan.TaskRuns[i].TaskRunID < plan.TaskRuns[j].TaskRunID })
	sort.Slice(plan.Runtimes, func(i, j int) bool { return plan.Runtimes[i].Name < plan.Runtimes[j].Name })
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func workspaceSourceRef(version *domain.DefinitionVersion) string {
	return "workspace:definition/" + string(version.DefinitionType) + "/" + version.DefinitionName
}

func workspaceHash(value any) string {
	data, _ := json.Marshal(value)
	return hashSource(data)
}
