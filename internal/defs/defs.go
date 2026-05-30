package defs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Plan struct {
	Root      string           `json:"root"`
	Agents    []AgentModule    `json:"agents,omitempty"`
	Workflows []WorkflowModule `json:"workflows,omitempty"`
	Runtimes  []RuntimeModule  `json:"runtimes,omitempty"`
	Skills    []SkillModule    `json:"skills,omitempty"`
	Tools     []ToolModule     `json:"tools,omitempty"`
}

type AgentModule struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Backend         string   `json:"backend,omitempty"`
	Model           string   `json:"model,omitempty"`
	SourcePath      string   `json:"source_path"`
	SourceHash      string   `json:"source_hash"`
	Version         string   `json:"version"`
	Instructions    string   `json:"instructions,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	DeniedCommands  []string `json:"denied_commands,omitempty"`
	Repos           []string `json:"repos,omitempty"`
	Env             []string `json:"env,omitempty"`
	MaxConcurrency  int      `json:"max_concurrency,omitempty"`
	MaxBudgetUSD    *float64 `json:"max_budget_usd,omitempty"`
	ReadOnly        bool     `json:"read_only,omitempty"`
}

type WorkflowModule struct {
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	SourcePath      string            `json:"source_path"`
	SourceHash      string            `json:"source_hash"`
	Version         string            `json:"version"`
	SingletonPolicy string            `json:"singleton_policy,omitempty"`
	Builtin         string            `json:"builtin,omitempty"`
	RoutePath       string            `json:"route_path,omitempty"`
	RouteAuth       string            `json:"route_auth,omitempty"`
	TriggerEvent    string            `json:"trigger_event,omitempty"`
	TriggerFilter   map[string]string `json:"trigger_filter,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	Env             []string          `json:"env,omitempty"`
	Repos           []string          `json:"repos,omitempty"`
}

type RuntimeModule struct {
	Name       string                 `json:"name"`
	Version    string                 `json:"version"`
	SourcePath string                 `json:"source_path"`
	SourceHash string                 `json:"source_hash"`
	Provider   domain.RuntimeProvider `json:"provider"`
	Image      string                 `json:"image,omitempty"`
	Repos      []string               `json:"repos,omitempty"`
	Env        []string               `json:"env,omitempty"`
	CPU        string                 `json:"cpu,omitempty"`
	Memory     string                 `json:"memory,omitempty"`
}

type SkillModule struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Version      string   `json:"version"`
	SourcePath   string   `json:"source_path"`
	SourceHash   string   `json:"source_hash"`
	Instructions string   `json:"instructions,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}

type ToolModule struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version"`
	SourcePath  string         `json:"source_path"`
	SourceHash  string         `json:"source_hash"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Handler     string         `json:"handler,omitempty"`
	Runtime     string         `json:"runtime,omitempty"`
	Repos       []string       `json:"repos,omitempty"`
	Env         []string       `json:"env,omitempty"`
	ReadOnly    bool           `json:"read_only,omitempty"`
}

func Load(root string) (*Plan, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	loomDir := filepath.Join(abs, ".loom")
	plan := &Plan{Root: abs}
	if _, err := os.Stat(loomDir); errors.Is(err, os.ErrNotExist) {
		return plan, nil
	}
	if err != nil {
		return nil, err
	}
	plan, err = loadWithTypeScriptCompiler(abs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(plan.Root) == "" {
		plan.Root = abs
	}
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

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
	if err := appendControlPlaneWorkflows(ctx, st, workspaceKey, plan, index.hasWorkflow); err != nil {
		return nil, err
	}
	if err := appendControlPlaneRuntimes(ctx, st, workspaceKey, plan, index.hasRuntime); err != nil {
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
		Name:            role.Name,
		Description:     role.Description,
		Backend:         role.Backend,
		Model:           role.Model,
		SourcePath:      "control-plane:role/" + role.Name,
		Instructions:    role.PromptFile,
		Skills:          compactStrings(role.Skills),
		Tools:           compactStrings(role.AllowedTools),
		DeniedCommands:  compactStrings(role.DeniedTools),
		MaxConcurrency:  maxConcurrency,
		MaxBudgetUSD:    role.MaxBudgetUSD,
		ReadOnly:        role.ReadOnly,
		AllowedCommands: nil,
	}
	agent.SourceHash = workspaceHash(agent)
	agent.Version = version(agent.SourceHash)
	return agent
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
	sort.Slice(plan.Workflows, func(i, j int) bool { return plan.Workflows[i].Name < plan.Workflows[j].Name })
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

//nolint:funlen // Applying a plan is intentionally linear so each durable record write stays visible.
func Apply(ctx context.Context, st store.Store, workspaceKey, actor string, plan *Plan) error {
	if st == nil {
		return fmt.Errorf("store not configured")
	}
	if plan == nil {
		return fmt.Errorf("definition plan required")
	}
	if err := applySkillDefinitions(ctx, st, workspaceKey, actor, plan.Skills); err != nil {
		return err
	}
	skillIndex := indexSkills(plan.Skills)
	if err := applyToolDefinitions(ctx, st, workspaceKey, actor, plan.Tools); err != nil {
		return err
	}
	toolIndex := indexTools(plan.Tools)
	for _, agent := range plan.Agents {
		manifest := mustJSON(agent)
		capability := agentCapabilityManifest(agent, skillIndex, toolIndex)
		if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
			WorkspaceKey:       workspaceKey,
			DefinitionType:     domain.DefinitionTypeAgent,
			DefinitionName:     agent.Name,
			Version:            agent.Version,
			SourceHash:         agent.SourceHash,
			BundleHash:         agent.SourceHash,
			Manifest:           manifest,
			CapabilityManifest: capability,
			CreatedBy:          actor,
			Status:             domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("apply agent definition %s: %w", agent.Name, err)
		}
		if err := upsertRole(ctx, st, workspaceKey, agent); err != nil {
			return err
		}
	}
	for _, rt := range plan.Runtimes {
		manifest := mustJSON(rt)
		if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
			WorkspaceKey:   workspaceKey,
			DefinitionType: domain.DefinitionTypeRuntime,
			DefinitionName: rt.Name,
			Version:        rt.Version,
			SourceHash:     rt.SourceHash,
			BundleHash:     rt.SourceHash,
			Manifest:       manifest,
			CreatedBy:      actor,
			Status:         domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("apply runtime definition %s: %w", rt.Name, err)
		}
		if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
			WorkspaceKey: workspaceKey,
			Name:         rt.Name,
			Version:      rt.Version,
			Provider:     rt.Provider,
			Image:        rt.Image,
			Repos:        rt.Repos,
			Env:          rt.Env,
			CPU:          rt.CPU,
			Memory:       rt.Memory,
			Manifest:     manifest,
			Status:       domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("upsert runtime profile %s: %w", rt.Name, err)
		}
	}
	for _, wf := range plan.Workflows {
		manifest := mustJSON(wf)
		capability := workflowCapabilityManifest(wf, toolIndex)
		if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
			WorkspaceKey:       workspaceKey,
			DefinitionType:     domain.DefinitionTypeWorkflow,
			DefinitionName:     wf.Name,
			Version:            wf.Version,
			SourceHash:         wf.SourceHash,
			BundleHash:         wf.SourceHash,
			Manifest:           manifest,
			CapabilityManifest: capability,
			CreatedBy:          actor,
			Status:             domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("apply workflow definition %s: %w", wf.Name, err)
		}
		if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
			WorkspaceKey:       workspaceKey,
			Name:               wf.Name,
			Version:            wf.Version,
			Description:        wf.Description,
			SingletonPolicy:    wf.SingletonPolicy,
			SourceRef:          wf.SourcePath,
			BundleHash:         wf.SourceHash,
			Manifest:           manifest,
			CapabilityManifest: capability,
			Status:             domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("upsert workflow definition %s: %w", wf.Name, err)
		}
		if wf.RoutePath != "" {
			if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
				WorkspaceKey:   workspaceKey,
				BindingID:      routeBindingID(wf.Name, "POST", wf.RoutePath),
				DefinitionName: wf.Name,
				DefinitionType: domain.DefinitionTypeWorkflow,
				Path:           wf.RoutePath,
				Method:         "POST",
				AuthPolicy:     wf.RouteAuth,
				Status:         domain.DefinitionStatusActive,
			}); err != nil {
				return fmt.Errorf("upsert route binding %s: %w", wf.Name, err)
			}
		}
		if wf.TriggerEvent != "" {
			if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
				WorkspaceKey: workspaceKey,
				BindingID:    "workflow:" + wf.Name + ":" + wf.TriggerEvent,
				WorkflowName: wf.Name,
				EventType:    wf.TriggerEvent,
				Filter:       mustJSON(wf.TriggerFilter),
				Status:       domain.DefinitionStatusActive,
			}); err != nil {
				return fmt.Errorf("upsert trigger binding %s: %w", wf.Name, err)
			}
		}
	}
	return nil
}

func applySkillDefinitions(ctx context.Context, st store.Store, workspaceKey, actor string, skills []SkillModule) error {
	for _, skill := range skills {
		manifest := mustJSON(skill)
		capability := skillCapabilityManifest(skill)
		if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
			WorkspaceKey:       workspaceKey,
			DefinitionType:     domain.DefinitionTypeSkill,
			DefinitionName:     skill.Name,
			Version:            skill.Version,
			SourceHash:         skill.SourceHash,
			BundleHash:         skill.SourceHash,
			Manifest:           manifest,
			CapabilityManifest: capability,
			CreatedBy:          actor,
			Status:             domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("apply skill definition %s: %w", skill.Name, err)
		}
	}
	return nil
}

func applyToolDefinitions(ctx context.Context, st store.Store, workspaceKey, actor string, tools []ToolModule) error {
	for _, tool := range tools {
		manifest := mustJSON(tool)
		capability := toolCapabilityManifest(tool)
		if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
			WorkspaceKey:       workspaceKey,
			DefinitionType:     domain.DefinitionTypeTool,
			DefinitionName:     tool.Name,
			Version:            tool.Version,
			SourceHash:         tool.SourceHash,
			BundleHash:         tool.SourceHash,
			Manifest:           manifest,
			CapabilityManifest: capability,
			CreatedBy:          actor,
			Status:             domain.DefinitionStatusActive,
		}); err != nil {
			return fmt.Errorf("apply tool definition %s: %w", tool.Name, err)
		}
	}
	return nil
}

func agentCapabilityManifest(agent AgentModule, skills map[string]SkillModule, tools map[string]ToolModule) json.RawMessage {
	out := map[string]any{
		"manifest_version": "loom.capabilities.v1",
		"definition": map[string]any{
			"type":    string(domain.DefinitionTypeAgent),
			"name":    agent.Name,
			"version": agent.Version,
		},
		"model": map[string]any{
			"tools":              compactStrings(agent.Tools),
			"tool_definitions":   referencedToolDefinitions(agent.Tools, tools),
			"skills":             compactStrings(agent.Skills),
			"skill_bundles":      agentSkillBundles(agent, skills),
			"prompt_bundle_hash": agentPromptBundleHash(agent, skills),
		},
		"sandbox": map[string]any{
			"allowed_commands": compactStrings(agent.AllowedCommands),
			"denied_commands":  compactStrings(agent.DeniedCommands),
		},
		"runtime": map[string]any{
			"repos": compactStrings(agent.Repos),
			"env":   compactStrings(agent.Env),
		},
		"limits": map[string]any{
			"max_concurrency": agent.MaxConcurrency,
			"max_budget_usd":  agent.MaxBudgetUSD,
			"read_only":       agent.ReadOnly,
		},
	}
	return mustJSON(compactMap(out))
}

func toolCapabilityManifest(tool ToolModule) json.RawMessage {
	out := map[string]any{
		"manifest_version": "loom.capabilities.v1",
		"definition": map[string]any{
			"type":    string(domain.DefinitionTypeTool),
			"name":    tool.Name,
			"version": tool.Version,
		},
		"tool": map[string]any{
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"read_only":   tool.ReadOnly,
		},
		"execution": map[string]any{
			"handler": tool.Handler,
			"runtime": tool.Runtime,
		},
		"runtime": map[string]any{
			"repos": compactStrings(tool.Repos),
			"env":   compactStrings(tool.Env),
		},
	}
	return mustJSON(compactMap(out))
}

func skillCapabilityManifest(skill SkillModule) json.RawMessage {
	out := map[string]any{
		"manifest_version": "loom.capabilities.v1",
		"definition": map[string]any{
			"type":    string(domain.DefinitionTypeSkill),
			"name":    skill.Name,
			"version": skill.Version,
		},
		"skill": map[string]any{
			"instructions_hash": hashString(skill.Instructions),
			"resources":         compactStrings(skill.Resources),
		},
	}
	return mustJSON(compactMap(out))
}

func agentSkillBundles(agent AgentModule, skills map[string]SkillModule) []map[string]any {
	out := make([]map[string]any, 0, len(agent.Skills))
	for _, name := range compactStrings(agent.Skills) {
		skill, ok := skills[name]
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":        skill.Name,
			"version":     skill.Version,
			"source_hash": skill.SourceHash,
			"resources":   compactStrings(skill.Resources),
		})
	}
	return out
}

func agentPromptBundleHash(agent AgentModule, skills map[string]SkillModule) string {
	h := sha256.New()
	_, _ = h.Write([]byte(agent.Instructions))
	for _, name := range compactStrings(agent.Skills) {
		if skill, ok := skills[name]; ok {
			_, _ = h.Write([]byte(name))
			_, _ = h.Write([]byte(skill.SourceHash))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func referencedToolDefinitions(names []string, tools map[string]ToolModule) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, name := range compactStrings(names) {
		tool, ok := tools[name]
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":        tool.Name,
			"version":     tool.Version,
			"source_hash": tool.SourceHash,
			"handler":     tool.Handler,
			"runtime":     tool.Runtime,
			"read_only":   tool.ReadOnly,
			"parameters":  tool.Parameters,
		})
	}
	return out
}

func indexSkills(skills []SkillModule) map[string]SkillModule {
	out := make(map[string]SkillModule, len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" {
			continue
		}
		out[skill.Name] = skill
	}
	return out
}

func indexTools(tools []ToolModule) map[string]ToolModule {
	out := make(map[string]ToolModule, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out[tool.Name] = tool
	}
	return out
}

func workflowCapabilityManifest(wf WorkflowModule, tools map[string]ToolModule) json.RawMessage {
	ingress := map[string]any{}
	if wf.RoutePath != "" {
		ingress["route"] = map[string]any{
			"method": "POST",
			"path":   wf.RoutePath,
			"auth":   wf.RouteAuth,
		}
	}
	if wf.TriggerEvent != "" {
		ingress["trigger"] = map[string]any{
			"event":  wf.TriggerEvent,
			"filter": wf.TriggerFilter,
		}
	}
	out := map[string]any{
		"manifest_version": "loom.capabilities.v1",
		"definition": map[string]any{
			"type":    string(domain.DefinitionTypeWorkflow),
			"name":    wf.Name,
			"version": wf.Version,
		},
		"workflow": map[string]any{
			"tools":            compactStrings(wf.Tools),
			"tool_definitions": referencedToolDefinitions(wf.Tools, tools),
		},
		"runtime": map[string]any{
			"repos": compactStrings(wf.Repos),
			"env":   compactStrings(wf.Env),
		},
		"runner": map[string]any{
			"builtin": wf.Builtin,
		},
		"ingress": ingress,
		"idempotency": map[string]any{
			"singleton_policy": wf.SingletonPolicy,
		},
	}
	return mustJSON(compactMap(out))
}

func FindAgent(plan *Plan, name string) (AgentModule, bool) {
	if plan == nil {
		return AgentModule{}, false
	}
	for _, agent := range plan.Agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return AgentModule{}, false
}

func FindWorkflow(plan *Plan, name string) (WorkflowModule, bool) {
	if plan == nil {
		return WorkflowModule{}, false
	}
	for _, workflow := range plan.Workflows {
		if workflow.Name == name {
			return workflow, true
		}
	}
	return WorkflowModule{}, false
}

//nolint:funlen // Instance upsert and optional start command must remain one transactional-looking flow.
func ApplyAgentInstance(ctx context.Context, st store.Store, workspaceKey string, agent AgentModule, instanceName string, start bool) (*domain.Agent, error) {
	if st == nil || st.Agents() == nil {
		return nil, fmt.Errorf("agent store not configured")
	}
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		instanceName = agent.Name
	}
	desired := domain.AgentDesiredIdle
	state := domain.AgentStateIdle
	if start {
		desired = domain.AgentDesiredRunning
		state = domain.AgentStateActive
	}
	mode := domain.AgentModeService
	created, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:   workspaceKey,
		Name:           instanceName,
		RoleName:       agent.Name,
		Auto:           start,
		Backend:        agent.Backend,
		Repos:          durableAgentRepos(agent.Repos),
		Mode:           mode,
		MaxConcurrency: agent.MaxConcurrency,
		DesiredState:   desired,
	})
	if err == nil {
		if start && st.AgentCommands() != nil {
			if _, cmdErr := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  workspaceKey,
				TargetAgentID: created.Name,
				Type:          "start",
				Payload: map[string]string{
					"source":          "typescript-first",
					"definition_name": agent.Name,
					"definition_ver":  agent.Version,
				},
			}); cmdErr != nil {
				return nil, fmt.Errorf("create start command: %w", cmdErr)
			}
		}
		if start && created.State != state {
			return st.Agents().Update(ctx, workspaceKey, created.Name, store.AgentUpdate{
				State:        &state,
				DesiredState: &desired,
			})
		}
		return created, nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, fmt.Errorf("create agent instance %s: %w", instanceName, err)
	}
	patch := store.AgentUpdate{
		RoleName:       &agent.Name,
		Auto:           &start,
		Backend:        &agent.Backend,
		Repos:          ptrSlice(durableAgentRepos(agent.Repos)),
		Mode:           &mode,
		MaxConcurrency: &agent.MaxConcurrency,
		DesiredState:   &desired,
	}
	if start {
		patch.State = &state
	}
	updated, err := st.Agents().Update(ctx, workspaceKey, instanceName, patch)
	if err != nil {
		return nil, fmt.Errorf("update agent instance %s: %w", instanceName, err)
	}
	if start && st.AgentCommands() != nil {
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  workspaceKey,
			TargetAgentID: updated.Name,
			Type:          "start",
			Payload: map[string]string{
				"source":          "typescript-first",
				"definition_name": agent.Name,
				"definition_ver":  agent.Version,
			},
		}); err != nil {
			return nil, fmt.Errorf("create start command: %w", err)
		}
	}
	return updated, nil
}

func durableAgentRepos(repos []string) []string {
	out := make([]string, 0, len(repos))
	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" || repo == "." {
			continue
		}
		out = append(out, repo)
	}
	return compactStrings(out)
}

func ptrSlice(values []string) *[]string {
	return &values
}

func Summary(plan *Plan) string {
	if plan == nil {
		return "No definition plan loaded"
	}
	summary := fmt.Sprintf("agents=%d workflows=%d runtimes=%d", len(plan.Agents), len(plan.Workflows), len(plan.Runtimes))
	if len(plan.Skills) > 0 {
		summary += fmt.Sprintf(" skills=%d", len(plan.Skills))
	}
	if len(plan.Tools) > 0 {
		summary += fmt.Sprintf(" tools=%d", len(plan.Tools))
	}
	return summary
}

func loadDir(dir string, fn func(string, []byte) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ts") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path) //nolint:gosec // user-authored local definition file.
		if err != nil {
			return err
		}
		if err := fn(path, data); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocognit,cyclop,funlen // Validation is kept centralized so duplicate-name and capability errors share one pass.
func validatePlan(plan *Plan) error {
	seen := make(map[string]string)
	for _, skill := range plan.Skills {
		if strings.TrimSpace(skill.Name) == "" {
			return fmt.Errorf("%s: skill definition name is required", skill.SourcePath)
		}
		if !definitionNamePattern.MatchString(skill.Name) {
			return fmt.Errorf("%s: skill definition name %q must be lower-kebab-case", skill.SourcePath, skill.Name)
		}
		if prior := seen["skill:"+skill.Name]; prior != "" {
			return fmt.Errorf("duplicate skill definition %q in %s and %s", skill.Name, prior, skill.SourcePath)
		}
		seen["skill:"+skill.Name] = skill.SourcePath
		if err := validateUniqueStrings(skill.SourcePath, "skill resources", skill.Resources); err != nil {
			return err
		}
		if err := validateSkillResources(skill.SourcePath, skill.Resources); err != nil {
			return err
		}
	}
	for _, tool := range plan.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("%s: tool definition name is required", tool.SourcePath)
		}
		if !toolNamePattern.MatchString(tool.Name) {
			return fmt.Errorf("%s: tool definition name %q must use lower_snake_case or lower-kebab-case", tool.SourcePath, tool.Name)
		}
		if reservedModelToolNames[tool.Name] {
			return fmt.Errorf("%s: tool definition name %q collides with a built-in sandbox tool", tool.SourcePath, tool.Name)
		}
		if prior := seen["tool:"+tool.Name]; prior != "" {
			return fmt.Errorf("duplicate tool definition %q in %s and %s", tool.Name, prior, tool.SourcePath)
		}
		seen["tool:"+tool.Name] = tool.SourcePath
		if strings.TrimSpace(tool.Description) == "" {
			return fmt.Errorf("%s: tool definition %q must declare a description", tool.SourcePath, tool.Name)
		}
		if len(tool.Parameters) == 0 {
			return fmt.Errorf("%s: tool definition %q must declare parameters", tool.SourcePath, tool.Name)
		}
		if strings.TrimSpace(tool.Handler) == "" {
			return fmt.Errorf("%s: tool definition %q must declare an execution handler", tool.SourcePath, tool.Name)
		}
		if err := validateRepoAndEnvPolicy(tool.SourcePath, tool.Repos, tool.Env); err != nil {
			return err
		}
	}
	for _, agent := range plan.Agents {
		if strings.TrimSpace(agent.Name) == "" {
			return fmt.Errorf("%s: agent definition name is required", agent.SourcePath)
		}
		if !definitionNamePattern.MatchString(agent.Name) {
			return fmt.Errorf("%s: agent definition name %q must be lower-kebab-case", agent.SourcePath, agent.Name)
		}
		if prior := seen["agent:"+agent.Name]; prior != "" {
			return fmt.Errorf("duplicate agent definition %q in %s and %s", agent.Name, prior, agent.SourcePath)
		}
		seen["agent:"+agent.Name] = agent.SourcePath
		if err := validateUniqueStrings(agent.SourcePath, "agent model tools", agent.Tools); err != nil {
			return err
		}
		if err := validateUniqueStrings(agent.SourcePath, "agent skills", agent.Skills); err != nil {
			return err
		}
		if err := validateUniqueStrings(agent.SourcePath, "agent allowed commands", agent.AllowedCommands); err != nil {
			return err
		}
		if err := validateUniqueStrings(agent.SourcePath, "agent denied commands", agent.DeniedCommands); err != nil {
			return err
		}
		if err := validateRepoAndEnvPolicy(agent.SourcePath, agent.Repos, agent.Env); err != nil {
			return err
		}
		if err := validateNoExactCollision(agent.SourcePath, "model tool", agent.Tools, "sandbox command", agent.AllowedCommands); err != nil {
			return err
		}
	}
	for _, wf := range plan.Workflows {
		if strings.TrimSpace(wf.Name) == "" {
			return fmt.Errorf("%s: workflow definition name is required", wf.SourcePath)
		}
		if !definitionNamePattern.MatchString(wf.Name) {
			return fmt.Errorf("%s: workflow definition name %q must be lower-kebab-case", wf.SourcePath, wf.Name)
		}
		if prior := seen["workflow:"+wf.Name]; prior != "" {
			return fmt.Errorf("duplicate workflow definition %q in %s and %s", wf.Name, prior, wf.SourcePath)
		}
		seen["workflow:"+wf.Name] = wf.SourcePath
		if err := validateUniqueStrings(wf.SourcePath, "workflow tools", wf.Tools); err != nil {
			return err
		}
		if err := validateRepoAndEnvPolicy(wf.SourcePath, wf.Repos, wf.Env); err != nil {
			return err
		}
		if wf.RoutePath != "" && strings.TrimSpace(wf.RouteAuth) == "" {
			return fmt.Errorf("%s: workflow route %q must declare an auth policy", wf.SourcePath, wf.RoutePath)
		}
	}
	for _, rt := range plan.Runtimes {
		if strings.TrimSpace(rt.Name) == "" {
			return fmt.Errorf("%s: runtime definition name is required", rt.SourcePath)
		}
		if !definitionNamePattern.MatchString(rt.Name) {
			return fmt.Errorf("%s: runtime definition name %q must be lower-kebab-case", rt.SourcePath, rt.Name)
		}
		if prior := seen["runtime:"+rt.Name]; prior != "" {
			return fmt.Errorf("duplicate runtime definition %q in %s and %s", rt.Name, prior, rt.SourcePath)
		}
		seen["runtime:"+rt.Name] = rt.SourcePath
		if err := validateRepoAndEnvPolicy(rt.SourcePath, rt.Repos, rt.Env); err != nil {
			return err
		}
	}
	return nil
}

func validateSkillResources(sourcePath string, resources []string) error {
	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if resource == "" {
			continue
		}
		if filepath.IsAbs(resource) || strings.HasPrefix(resource, "../") || strings.Contains(resource, "/../") || resource == ".." {
			return fmt.Errorf("%s: skill resource %q must stay inside the skill directory", sourcePath, resource)
		}
	}
	return nil
}

func validateUniqueStrings(sourcePath, label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s: duplicate %s capability %q", sourcePath, label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateRepoAndEnvPolicy(sourcePath string, repos, env []string) error {
	for _, repo := range repos {
		if strings.TrimSpace(repo) == "*" {
			return fmt.Errorf("%s: wildcard repo mounts are not allowed", sourcePath)
		}
	}
	for _, name := range env {
		if strings.TrimSpace(name) == "*" {
			return fmt.Errorf("%s: wildcard environment grants are not allowed", sourcePath)
		}
	}
	return nil
}

func validateNoExactCollision(sourcePath, leftLabel string, left []string, rightLabel string, right []string) error {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value != "" {
			rightSet[value] = struct{}{}
		}
	}
	for _, value := range left {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := rightSet[value]; ok {
			return fmt.Errorf("%s: %s %q collides with %s capability", sourcePath, leftLabel, value, rightLabel)
		}
	}
	return nil
}

func upsertRole(ctx context.Context, st store.Store, ws string, agent AgentModule) error {
	allowed := append([]string(nil), agent.AllowedCommands...)
	in := store.RoleCreate{
		WorkspaceKey:   ws,
		Name:           agent.Name,
		Description:    agent.Description,
		PromptFile:     agent.SourcePath,
		Model:          agent.Model,
		Backend:        agent.Backend,
		Skills:         agent.Skills,
		MaxConcurrency: nil,
		ReadOnly:       agent.ReadOnly,
		AllowedTools:   compactStrings(allowed),
		DeniedTools:    compactStrings(agent.DeniedCommands),
		MaxBudgetUSD:   agent.MaxBudgetUSD,
	}
	if agent.MaxConcurrency > 0 {
		v := agent.MaxConcurrency
		in.MaxConcurrency = &v
	}
	if _, err := st.Roles().Create(ctx, in); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create role %s: %w", agent.Name, err)
	}
	patch := store.RoleUpdate{
		Description:  &agent.Description,
		PromptFile:   &agent.SourcePath,
		Model:        &agent.Model,
		Backend:      &agent.Backend,
		Skills:       &agent.Skills,
		ReadOnly:     &agent.ReadOnly,
		AllowedTools: &in.AllowedTools,
		DeniedTools:  &in.DeniedTools,
		MaxBudgetUSD: &agent.MaxBudgetUSD,
	}
	if in.MaxConcurrency != nil {
		patch.MaxConcurrency = &in.MaxConcurrency
	}
	if _, err := st.Roles().Update(ctx, ws, agent.Name, patch); err != nil {
		return fmt.Errorf("update role %s: %w", agent.Name, err)
	}
	return nil
}

func hashSource(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashString(value string) string {
	return hashSource([]byte(value))
}

func version(hash string) string {
	if len(hash) < 12 {
		return hash
	}
	return hash[:12]
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func routeBindingID(name, method, routePath string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "POST"
	}
	safePath := strings.Trim(strings.TrimSpace(routePath), "/")
	if safePath == "" {
		safePath = "root"
	}
	safePath = strings.NewReplacer("/", ".", " ", "-").Replace(safePath)
	return "workflow:" + name + ":" + method + ":" + safePath
}

func compactMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		compact, ok := compactValue(value)
		if !ok {
			continue
		}
		out[key] = compact
	}
	return out
}

func compactValue(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, false
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, false
		}
		return v, true
	case []string:
		items := compactStrings(v)
		if len(items) == 0 {
			return nil, false
		}
		return items, true
	case map[string]string:
		if len(v) == 0 {
			return nil, false
		}
		return v, true
	case map[string]any:
		nested := compactMap(v)
		if len(nested) == 0 {
			return nil, false
		}
		return nested, true
	case []map[string]any:
		if len(v) == 0 {
			return nil, false
		}
		return v, true
	default:
		return value, true
	}
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
