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
	if err := loadDir(filepath.Join(loomDir, "agents"), func(path string, data []byte) error {
		mod, err := parseAgent(path, data)
		if err != nil {
			return err
		}
		plan.Agents = append(plan.Agents, mod)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadDir(filepath.Join(loomDir, "workflows"), func(path string, data []byte) error {
		mod, err := parseWorkflow(path, data)
		if err != nil {
			return err
		}
		plan.Workflows = append(plan.Workflows, mod)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadDir(filepath.Join(loomDir, "runtimes"), func(path string, data []byte) error {
		mod, err := parseRuntime(path, data)
		if err != nil {
			return err
		}
		plan.Runtimes = append(plan.Runtimes, mod)
		return nil
	}); err != nil {
		return nil, err
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
		capability := mustJSON(map[string]any{
			"model_tools":      agent.Tools,
			"sandbox_commands": agent.AllowedCommands,
			"denied_commands":  agent.DeniedCommands,
			"repos":            agent.Repos,
			"env":              agent.Env,
		})
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
		capability := mustJSON(map[string]any{
			"workflow_tools": wf.Tools,
			"repos":          wf.Repos,
			"env":            wf.Env,
			"builtin":        wf.Builtin,
		})
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
		if prior := seen["agent:"+agent.Name]; prior != "" {
			return fmt.Errorf("duplicate agent definition %q in %s and %s", agent.Name, prior, agent.SourcePath)
		}
		seen["agent:"+agent.Name] = agent.SourcePath
	}
	for _, wf := range plan.Workflows {
		if prior := seen["workflow:"+wf.Name]; prior != "" {
			return fmt.Errorf("duplicate workflow definition %q in %s and %s", wf.Name, prior, wf.SourcePath)
		}
		seen["workflow:"+wf.Name] = wf.SourcePath
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

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
