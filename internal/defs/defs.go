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
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Plan struct {
	Root                 string                      `json:"root"`
	ModelPolicy          *ModelPolicy                `json:"model_policy,omitempty"`
	Agents               []AgentModule               `json:"agents,omitempty"`
	AgentInstances       []AgentInstanceModule       `json:"agent_instances,omitempty"`
	AgentSessions        []AgentSessionModule        `json:"agent_sessions,omitempty"`
	AgentLeases          []AgentLeaseModule          `json:"agent_leases,omitempty"`
	AgentOwnershipLeases []AgentOwnershipLeaseModule `json:"agent_ownership_leases,omitempty"`
	AgentCommands        []AgentCommandModule        `json:"agent_commands,omitempty"`
	TerminalSessions     []TerminalSessionModule     `json:"terminal_sessions,omitempty"`
	Artifacts            []ArtifactModule            `json:"artifacts,omitempty"`
	Workflows            []WorkflowModule            `json:"workflows,omitempty"`
	WorkflowRuns         []WorkflowRunModule         `json:"workflow_runs,omitempty"`
	TaskRuns             []TaskRunModule             `json:"task_runs,omitempty"`
	RunEvents            []RunEventModule            `json:"run_events,omitempty"`
	Runtimes             []RuntimeModule             `json:"runtimes,omitempty"`
	Skills               []SkillModule               `json:"skills,omitempty"`
	Tools                []ToolModule                `json:"tools,omitempty"`
}

type ModelPolicy struct {
	AllowedModels    []string `json:"allowed_models,omitempty"`
	AllowedProviders []string `json:"allowed_providers,omitempty"`
	AllowUnknown     bool     `json:"allow_unknown,omitempty"`
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
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	SourcePath         string            `json:"source_path"`
	SourceHash         string            `json:"source_hash"`
	Version            string            `json:"version"`
	SingletonPolicy    string            `json:"singleton_policy,omitempty"`
	RuntimeProfileName string            `json:"runtime_profile_name,omitempty"`
	Builtin            string            `json:"builtin,omitempty"`
	Runner             string            `json:"runner,omitempty"`
	RoutePath          string            `json:"route_path,omitempty"`
	RouteAuth          string            `json:"route_auth,omitempty"`
	TriggerEvent       string            `json:"trigger_event,omitempty"`
	TriggerFilter      map[string]string `json:"trigger_filter,omitempty"`
	Tools              []string          `json:"tools,omitempty"`
	Env                []string          `json:"env,omitempty"`
	Repos              []string          `json:"repos,omitempty"`
}

type RuntimeModule struct {
	Name               string                 `json:"name"`
	Version            string                 `json:"version"`
	SourcePath         string                 `json:"source_path"`
	SourceHash         string                 `json:"source_hash"`
	Provider           domain.RuntimeProvider `json:"provider"`
	Image              string                 `json:"image,omitempty"`
	Repos              []string               `json:"repos,omitempty"`
	Env                []string               `json:"env,omitempty"`
	CPU                string                 `json:"cpu,omitempty"`
	Memory             string                 `json:"memory,omitempty"`
	CWD                string                 `json:"cwd,omitempty"`
	WorkspaceSkillDirs []string               `json:"workspace_skill_dirs,omitempty"`
	Workspace          *RuntimeWorkspace      `json:"workspace,omitempty"`
}

type RuntimeWorkspace struct {
	ProviderWorkspaceID string                 `json:"provider_workspace_id,omitempty"`
	Owner               string                 `json:"owner,omitempty"`
	Cleanup             *RuntimeCleanupPolicy  `json:"cleanup,omitempty"`
	Filesystem          *RuntimeFilesystemSpec `json:"filesystem,omitempty"`
}

type RuntimeCleanupPolicy struct {
	Mode      string `json:"mode,omitempty"`
	TTL       string `json:"ttl,omitempty"`
	Retention string `json:"retention,omitempty"`
}

type RuntimeFilesystemSpec struct {
	Persistence string `json:"persistence,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Retention   string `json:"retention,omitempty"`
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
	loomExists, err := pathExists(loomDir)
	if err != nil {
		return nil, err
	}
	configExists, err := pathExists(filepath.Join(abs, "loom.config.ts"))
	if err != nil {
		return nil, err
	}
	rootEntrypoints, err := hasRootSourceEntrypoints(abs)
	if err != nil {
		return nil, err
	}
	if !loomExists && !configExists && !rootEntrypoints {
		return plan, nil
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

func pathExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

func hasRootSourceEntrypoints(root string) (bool, error) {
	for _, dir := range []string{"agents", "workflows", "runtimes", "tools"} {
		ok, err := hasImmediateTypeScriptFile(filepath.Join(root, dir))
		if err != nil || ok {
			return ok, err
		}
	}
	return hasSkillEntrypoint(filepath.Join(root, "skills"))
}

func hasImmediateTypeScriptFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ts") {
			return true, nil
		}
	}
	return false, nil
}

func hasSkillEntrypoint(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ts") {
			return true, nil
		}
		if entry.IsDir() {
			ok, err := pathExists(filepath.Join(dir, entry.Name(), "SKILL.md"))
			if err != nil || ok {
				return ok, err
			}
		}
	}
	return false, nil
}

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
	if err := applyAgentDefinitions(ctx, st, workspaceKey, actor, plan.Agents, skillIndex, toolIndex); err != nil {
		return err
	}
	if err := applyAgentInstances(ctx, st, workspaceKey, plan.AgentInstances); err != nil {
		return err
	}
	if err := applyRuntimeDefinitions(ctx, st, workspaceKey, actor, plan.Runtimes); err != nil {
		return err
	}
	if err := applyWorkflowDefinitions(ctx, st, workspaceKey, actor, plan.Workflows, toolIndex); err != nil {
		return err
	}
	return applyRuntimeStateRecords(ctx, st, workspaceKey, plan)
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
			"profile": wf.RuntimeProfileName,
			"repos":   compactStrings(wf.Repos),
			"env":     compactStrings(wf.Env),
		},
		"runner": map[string]any{
			"builtin": wf.Builtin,
			"context": wf.Runner,
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
	if err := validateModelPolicy(plan.ModelPolicy); err != nil {
		return err
	}
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
	toolIndex := indexTools(plan.Tools)
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
		if shouldValidateModelSpecifiers(plan) {
			err := validateAgentModel(agent, plan.ModelPolicy)
			if err != nil {
				return err
			}
		}
		if err := validateNoExactCollision(agent.SourcePath, "model tool", agent.Tools, "sandbox command", agent.AllowedCommands); err != nil {
			return err
		}
		if err := validateAgentToolReferences(agent, toolIndex); err != nil {
			return err
		}
	}
	for _, instance := range plan.AgentInstances {
		if strings.TrimSpace(instance.Name) == "" {
			return fmt.Errorf("%s: agent instance name is required", instance.SourcePath)
		}
		if strings.TrimSpace(instance.RoleName) == "" {
			return fmt.Errorf("%s: agent instance %q must declare a role_name", instance.SourcePath, instance.Name)
		}
		if prior := seen["agent-instance:"+instance.Name]; prior != "" {
			return fmt.Errorf("duplicate agent instance %q in %s and %s", instance.Name, prior, instance.SourcePath)
		}
		seen["agent-instance:"+instance.Name] = instance.SourcePath
	}
	for _, session := range plan.AgentSessions {
		sourcePath := firstNonEmpty(session.SourcePath, "agent_session:"+session.SessionID)
		if strings.TrimSpace(session.SessionID) == "" {
			return fmt.Errorf("%s: agent session id is required", sourcePath)
		}
		if strings.TrimSpace(session.AgentID) == "" {
			return fmt.Errorf("%s: agent session %q must declare an agent_id", sourcePath, session.SessionID)
		}
		if prior := seen["agent-session:"+session.SessionID]; prior != "" {
			return fmt.Errorf("duplicate agent session %q in %s and %s", session.SessionID, prior, sourcePath)
		}
		seen["agent-session:"+session.SessionID] = sourcePath
	}
	for _, command := range plan.AgentCommands {
		sourcePath := firstNonEmpty(command.SourcePath, "agent_command:"+command.CommandID)
		if strings.TrimSpace(command.CommandID) == "" {
			return fmt.Errorf("%s: agent command id is required", sourcePath)
		}
		if strings.TrimSpace(command.Type) == "" {
			return fmt.Errorf("%s: agent command %q must declare a type", sourcePath, command.CommandID)
		}
		if prior := seen["agent-command:"+command.CommandID]; prior != "" {
			return fmt.Errorf("duplicate agent command %q in %s and %s", command.CommandID, prior, sourcePath)
		}
		seen["agent-command:"+command.CommandID] = sourcePath
	}
	for _, terminal := range plan.TerminalSessions {
		sourcePath := firstNonEmpty(terminal.SourcePath, "terminal_session:"+terminal.TerminalID)
		if strings.TrimSpace(terminal.TerminalID) == "" {
			return fmt.Errorf("%s: terminal session id is required", sourcePath)
		}
		if prior := seen["terminal-session:"+terminal.TerminalID]; prior != "" {
			return fmt.Errorf("duplicate terminal session %q in %s and %s", terminal.TerminalID, prior, sourcePath)
		}
		seen["terminal-session:"+terminal.TerminalID] = sourcePath
	}
	for _, artifact := range plan.Artifacts {
		sourcePath := firstNonEmpty(artifact.SourcePath, "artifact:"+artifact.ArtifactID)
		if strings.TrimSpace(artifact.ArtifactID) == "" {
			return fmt.Errorf("%s: artifact id is required", sourcePath)
		}
		if strings.TrimSpace(artifact.Type) == "" {
			return fmt.Errorf("%s: artifact %q must declare a type", sourcePath, artifact.ArtifactID)
		}
		if strings.TrimSpace(artifact.URI) == "" {
			return fmt.Errorf("%s: artifact %q must declare a uri", sourcePath, artifact.ArtifactID)
		}
		if prior := seen["artifact:"+artifact.ArtifactID]; prior != "" {
			return fmt.Errorf("duplicate artifact %q in %s and %s", artifact.ArtifactID, prior, sourcePath)
		}
		seen["artifact:"+artifact.ArtifactID] = sourcePath
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
		if err := validateWorkflowToolReferences(wf, toolIndex); err != nil {
			return err
		}
		if err := validateRepoAndEnvPolicy(wf.SourcePath, wf.Repos, wf.Env); err != nil {
			return err
		}
		if wf.RoutePath != "" && strings.TrimSpace(wf.RouteAuth) == "" {
			return fmt.Errorf("%s: workflow route %q must declare an auth policy", wf.SourcePath, wf.RoutePath)
		}
	}
	for _, run := range plan.WorkflowRuns {
		sourcePath := firstNonEmpty(run.SourcePath, "workflow_run:"+run.RunID)
		if strings.TrimSpace(run.RunID) == "" {
			return fmt.Errorf("%s: workflow run id is required", sourcePath)
		}
		if strings.TrimSpace(run.WorkflowName) == "" {
			return fmt.Errorf("%s: workflow run %q must declare a workflow_name", sourcePath, run.RunID)
		}
		if prior := seen["workflow-run:"+run.RunID]; prior != "" {
			return fmt.Errorf("duplicate workflow run %q in %s and %s", run.RunID, prior, sourcePath)
		}
		seen["workflow-run:"+run.RunID] = sourcePath
	}
	for _, run := range plan.TaskRuns {
		sourcePath := firstNonEmpty(run.SourcePath, "task_run:"+run.TaskRunID)
		if strings.TrimSpace(run.TaskRunID) == "" {
			return fmt.Errorf("%s: task run id is required", sourcePath)
		}
		if strings.TrimSpace(run.WorkflowRunID) == "" {
			return fmt.Errorf("%s: task run %q must declare a workflow_run_id", sourcePath, run.TaskRunID)
		}
		if strings.TrimSpace(run.WorkItemID) == "" {
			return fmt.Errorf("%s: task run %q must declare a work_item_id", sourcePath, run.TaskRunID)
		}
		if strings.TrimSpace(run.RoleName) == "" {
			return fmt.Errorf("%s: task run %q must declare a role_name", sourcePath, run.TaskRunID)
		}
		if prior := seen["task-run:"+run.TaskRunID]; prior != "" {
			return fmt.Errorf("duplicate task run %q in %s and %s", run.TaskRunID, prior, sourcePath)
		}
		seen["task-run:"+run.TaskRunID] = sourcePath
	}
	if err := validateAgentLeaseModules(plan, seen); err != nil {
		return err
	}
	for _, event := range plan.RunEvents {
		sourcePath := firstNonEmpty(event.SourcePath, "run_event:"+event.EventID)
		if strings.TrimSpace(event.EventID) == "" {
			return fmt.Errorf("%s: run event id is required", sourcePath)
		}
		if strings.TrimSpace(event.WorkflowRunID) == "" {
			return fmt.Errorf("%s: run event %q must declare a workflow_run_id", sourcePath, event.EventID)
		}
		if strings.TrimSpace(event.Type) == "" {
			return fmt.Errorf("%s: run event %q must declare a type", sourcePath, event.EventID)
		}
		if prior := seen["run-event:"+event.EventID]; prior != "" {
			return fmt.Errorf("duplicate run event %q in %s and %s", event.EventID, prior, sourcePath)
		}
		seen["run-event:"+event.EventID] = sourcePath
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

func shouldValidateModelSpecifiers(plan *Plan) bool {
	if plan == nil {
		return false
	}
	return plan.ModelPolicy != nil || !strings.HasPrefix(strings.TrimSpace(plan.Root), "workspace:")
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
