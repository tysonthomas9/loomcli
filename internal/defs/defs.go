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
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Plan struct {
	Root      string           `json:"root"`
	Agents    []AgentModule    `json:"agents,omitempty"`
	Workflows []WorkflowModule `json:"workflows,omitempty"`
	Runtimes  []RuntimeModule  `json:"runtimes,omitempty"`
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

func Apply(ctx context.Context, st store.Store, workspaceKey, actor string, plan *Plan) error {
	if st == nil {
		return fmt.Errorf("store not configured")
	}
	if plan == nil {
		return fmt.Errorf("definition plan required")
	}
	for _, agent := range plan.Agents {
		manifest := mustJSON(agent)
		capability := agentCapabilityManifest(agent)
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
		capability := workflowCapabilityManifest(wf)
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

func agentCapabilityManifest(agent AgentModule) json.RawMessage {
	out := map[string]any{
		"manifest_version": "loom.capabilities.v1",
		"definition": map[string]any{
			"type":    string(domain.DefinitionTypeAgent),
			"name":    agent.Name,
			"version": agent.Version,
		},
		"model": map[string]any{
			"tools":  compactStrings(agent.Tools),
			"skills": compactStrings(agent.Skills),
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

func workflowCapabilityManifest(wf WorkflowModule) json.RawMessage {
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
			"tools": compactStrings(wf.Tools),
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
	return fmt.Sprintf("agents=%d workflows=%d runtimes=%d", len(plan.Agents), len(plan.Workflows), len(plan.Runtimes))
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

func parseAgent(path string, data []byte) (AgentModule, error) {
	src := string(data)
	hash := hashSource(data)
	mod := AgentModule{
		Name:            stringField(src, "name"),
		Description:     stringField(src, "description"),
		Backend:         stringField(src, "backend"),
		Model:           stringField(src, "model"),
		SourcePath:      path,
		SourceHash:      hash,
		Version:         version(hash),
		Instructions:    templateField(src, "instructions"),
		Skills:          arrayField(src, "skills"),
		Tools:           dottedArrayField(src, "tools"),
		AllowedCommands: arrayField(src, "allowedCommands"),
		DeniedCommands:  arrayField(src, "deniedCommands"),
		Repos:           arrayField(src, "repos"),
		Env:             arrayField(src, "env"),
		MaxConcurrency:  intField(src, "maxConcurrency"),
		ReadOnly:        boolField(src, "readOnly"),
	}
	if v, ok := floatField(src, "maxBudgetUSD"); ok {
		mod.MaxBudgetUSD = &v
	}
	if mod.Name == "" {
		return mod, fmt.Errorf("%s: defineAgent name is required", path)
	}
	return mod, nil
}

func parseWorkflow(path string, data []byte) (WorkflowModule, error) {
	src := string(data)
	hash := hashSource(data)
	mod := WorkflowModule{
		Name:            stringField(src, "name"),
		Description:     stringField(src, "description"),
		SourcePath:      path,
		SourceHash:      hash,
		Version:         version(hash),
		SingletonPolicy: singletonPolicy(src),
		Builtin:         stringField(src, "builtin"),
		RoutePath:       firstPathField(src),
		RouteAuth:       stringField(src, "auth"),
		TriggerEvent:    triggerEvent(src),
		TriggerFilter:   triggerFilter(src),
		Tools:           dottedArrayField(src, "tools"),
		Repos:           arrayField(src, "repos"),
		Env:             arrayField(src, "env"),
	}
	if mod.Name == "" {
		return mod, fmt.Errorf("%s: defineWorkflow name is required", path)
	}
	return mod, nil
}

func parseRuntime(path string, data []byte) (RuntimeModule, error) {
	src := string(data)
	hash := hashSource(data)
	provider := domain.RuntimeProviderLocal
	switch {
	case strings.Contains(src, "runtime.podman"):
		provider = domain.RuntimeProviderOther
	case strings.Contains(src, "runtime.remote"):
		provider = domain.RuntimeProviderE2B
	}
	name := stringField(src, "name")
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return RuntimeModule{
		Name:       name,
		Version:    version(hash),
		SourcePath: path,
		SourceHash: hash,
		Provider:   provider,
		Image:      stringField(src, "image"),
		Repos:      arrayField(src, "repos"),
		Env:        arrayField(src, "env"),
		CPU:        stringField(src, "cpu"),
		Memory:     stringField(src, "memory"),
	}, nil
}

func validatePlan(plan *Plan) error {
	seen := make(map[string]string)
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
	allowed = append(allowed, agent.Tools...)
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

func stringField(src, name string) string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*:\s*['"]([^'"]+)['"]`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func templateField(src, name string) string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*:\s*` + "`" + `([^` + "`" + `]*)` + "`")
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return stringField(src, name)
}

func arrayField(src, name string) []string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*:\s*\[([^\]]*)\]`)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		return nil
	}
	itemRe := regexp.MustCompile(`['"]([^'"]+)['"]`)
	items := itemRe.FindAllStringSubmatch(m[1], -1)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, strings.TrimSpace(item[1]))
	}
	return compactStrings(out)
}

func dottedArrayField(src, name string) []string {
	values := arrayField(src, name)
	if len(values) > 0 {
		return values
	}
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*:\s*\[([^\]]*)\]`)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		return nil
	}
	tokenRe := regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)+`)
	return compactStrings(tokenRe.FindAllString(m[1], -1))
}

func intField(src, name string) int {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*:\s*([0-9]+)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0
}

func floatField(src, name string) (float64, bool) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*:\s*([0-9]+(?:\.[0-9]+)?)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		v, _ := strconv.ParseFloat(m[1], 64)
		return v, true
	}
	return 0, false
}

func boolField(src, name string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*:\s*(true|false)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return m[1] == "true"
	}
	return false
}

func singletonPolicy(src string) string {
	re := regexp.MustCompile("(?s)singleton\\s*:\\s*\\([^)]*\\)\\s*=>\\s*`([^`]+)`")
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return m[1]
	}
	return stringField(src, "singleton")
}

func firstPathField(src string) string {
	return stringField(src, "path")
}

func triggerEvent(src string) string {
	if strings.Contains(src, "issueLabelAdded") {
		return "issue.label_added"
	}
	return stringField(src, "eventType")
}

func triggerFilter(src string) map[string]string {
	out := map[string]string{}
	if label := stringField(src, "label"); label != "" {
		out["label"] = label
	}
	if typ := stringField(src, "type"); typ != "" {
		out["type"] = typ
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hashSource(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
	default:
		return value, true
	}
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
