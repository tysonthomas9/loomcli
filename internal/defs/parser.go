package defs

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func parseAgent(path string, data []byte) (AgentModule, error) {
	src := string(data)
	hash := hashSource(data)
	mod := AgentModule{
		Name:            stringField(src, "name"),
		Description:     stringField(src, "description"),
		Backend:         stringField(src, "backend"),
		Model:           stringField(src, "model"),
		ProfileName:     firstNonEmpty(stringField(src, "profileName"), stringField(src, "profile_name")),
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
		Name:               stringField(src, "name"),
		Description:        stringField(src, "description"),
		SourcePath:         path,
		SourceHash:         hash,
		Version:            version(hash),
		SingletonPolicy:    singletonPolicy(src),
		RuntimeProfileName: firstNonEmpty(stringField(src, "runtimeProfile"), stringField(src, "runtime_profile"), stringField(src, "runtime")),
		Builtin:            stringField(src, "builtin"),
		Runner:             workflowRunner(src),
		RoutePath:          firstPathField(src),
		RouteAuth:          stringField(src, "auth"),
		TriggerEvent:       triggerEvent(src),
		TriggerFilter:      triggerFilter(src),
		Tools:              dottedArrayField(src, "tools"),
		Repos:              arrayField(src, "repos"),
		Env:                arrayField(src, "env"),
	}
	if mod.Name == "" {
		return mod, fmt.Errorf("%s: defineWorkflow name is required", path)
	}
	return mod, nil
}

func workflowRunner(src string) string {
	if strings.Contains(src, "async run(") || strings.Contains(src, "run(") {
		return "workflow-context-v1"
	}
	return stringField(src, "runner")
}

func parseRuntime(path string, data []byte) RuntimeModule {
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
	runtime := RuntimeModule{
		Name:               name,
		Version:            version(hash),
		SourcePath:         path,
		SourceHash:         hash,
		Provider:           provider,
		Image:              stringField(src, "image"),
		Repos:              arrayField(src, "repos"),
		Env:                arrayField(src, "env"),
		CPU:                stringField(src, "cpu"),
		Memory:             stringField(src, "memory"),
		CWD:                stringField(src, "cwd"),
		WorkspaceSkillDirs: runtimeWorkspaceSkillDirs(src),
		Workspace:          runtimeWorkspacePolicy(src),
	}
	runtime.Capabilities = runtimeCapabilities(src, runtime)
	return runtime
}

func runtimeWorkspaceSkillDirs(src string) []string {
	return compactStrings(append(arrayField(src, "workspaceSkillDirs"), arrayField(src, "workspace_skill_dirs")...))
}

func runtimeWorkspacePolicy(src string) *RuntimeWorkspace {
	workspace := &RuntimeWorkspace{
		ProviderWorkspaceID: firstNonEmpty(
			stringField(src, "providerWorkspaceId"),
			stringField(src, "provider_workspace_id"),
			stringField(src, "workspaceId"),
			stringField(src, "workspace_id"),
		),
		Owner: firstNonEmpty(stringField(src, "workspaceOwner"), stringField(src, "workspace_owner"), stringField(src, "owner")),
		Cleanup: &RuntimeCleanupPolicy{
			Mode:      firstNonEmpty(stringField(src, "cleanupMode"), stringField(src, "cleanup_mode")),
			TTL:       firstNonEmpty(stringField(src, "cleanupTTL"), stringField(src, "cleanup_ttl")),
			Retention: firstNonEmpty(stringField(src, "cleanupRetention"), stringField(src, "cleanup_retention")),
		},
		Filesystem: &RuntimeFilesystemSpec{
			Persistence: firstNonEmpty(stringField(src, "filesystemPersistence"), stringField(src, "filesystem_persistence")),
			Durability:  firstNonEmpty(stringField(src, "filesystemDurability"), stringField(src, "filesystem_durability")),
			Retention:   firstNonEmpty(stringField(src, "filesystemRetention"), stringField(src, "filesystem_retention")),
		},
	}
	if workspace.Cleanup.Mode == "" && workspace.Cleanup.TTL == "" && workspace.Cleanup.Retention == "" {
		workspace.Cleanup = nil
	}
	if workspace.Filesystem.Persistence == "" && workspace.Filesystem.Durability == "" && workspace.Filesystem.Retention == "" {
		workspace.Filesystem = nil
	}
	if workspace.ProviderWorkspaceID == "" && workspace.Owner == "" && workspace.Cleanup == nil && workspace.Filesystem == nil {
		return nil
	}
	return workspace
}

func runtimeCapabilities(src string, rt RuntimeModule) *RuntimeCapabilities {
	local := rt.Provider == domain.RuntimeProviderLocal
	providerDefault := "provider_default"
	policy := providerDefault
	if local {
		policy = "local"
	}
	filesystemPolicy := firstNonEmpty(capabilityStringField(src, "filesystem", "policy"), policy)
	shellPolicy := firstNonEmpty(capabilityStringField(src, "shell", "policy"), policy)
	networkPolicy := firstNonEmpty(capabilityStringField(src, "network", "policy"), policy)
	lifecyclePolicy := firstNonEmpty(capabilityStringField(src, "lifecycle", "policy"), policy)
	return &RuntimeCapabilities{
		Filesystem: &RuntimeFilesystemCapabilities{
			Read:        capabilityBoolPointer(src, "filesystem", "read", boolPointer(local)),
			Write:       capabilityBoolPointer(src, "filesystem", "write", boolPointer(local)),
			ArtifactURI: capabilityBoolPointer(src, "filesystem", "artifactURI", boolPointer(true)),
			Policy:      filesystemPolicy,
			Persistence: firstNonEmpty(runtimeFilesystemPersistence(rt.Workspace), capabilityStringField(src, "filesystem", "persistence")),
			Durability:  firstNonEmpty(runtimeFilesystemDurability(rt.Workspace), capabilityStringField(src, "filesystem", "durability")),
			Retention:   firstNonEmpty(runtimeFilesystemRetention(rt.Workspace), capabilityStringField(src, "filesystem", "retention")),
		},
		Shell: &RuntimeShellCapabilities{
			Enabled:  capabilityBoolPointer(src, "shell", "enabled", boolPointer(local)),
			Commands: compactStrings(append(capabilityArrayField(src, "shell", "commands"), arrayField(src, "shellCommands")...)),
			Policy:   shellPolicy,
		},
		Network: &RuntimeNetworkCapabilities{
			Enabled: capabilityBoolPointer(src, "network", "enabled", boolPointer(local)),
			Policy:  networkPolicy,
		},
		Env: &RuntimeEnvCapabilities{
			Forwarded: cloneStringSlice(rt.Env),
			Policy:    "allowlist",
		},
		Workspace: &RuntimeWorkspaceCapabilities{
			ProviderWorkspaceID: runtimeProviderWorkspaceID(rt.Workspace),
			Owner:               runtimeWorkspaceOwner(rt.Workspace),
			CWD:                 rt.CWD,
			Repos:               cloneStringSlice(rt.Repos),
			SkillDirs:           cloneStringSlice(rt.WorkspaceSkillDirs),
		},
		Lifecycle: &RuntimeLifecycleCapabilities{
			Materialize:    capabilityBoolPointer(src, "lifecycle", "materialize", boolPointer(local)),
			Cleanup:        capabilityBoolPointer(src, "lifecycle", "cleanup", boolPointer(local)),
			Release:        capabilityBoolPointer(src, "lifecycle", "release", boolPointer(local)),
			Cancellation:   capabilityBoolPointer(src, "lifecycle", "cancellation", boolPointer(true)),
			DefaultTimeout: firstNonEmpty(capabilityStringField(src, "lifecycle", "defaultTimeout"), capabilityStringField(src, "lifecycle", "default_timeout")),
			Policy:         lifecyclePolicy,
		},
	}
}

func runtimeFilesystemPersistence(workspace *RuntimeWorkspace) string {
	if workspace == nil || workspace.Filesystem == nil {
		return ""
	}
	return workspace.Filesystem.Persistence
}

func runtimeFilesystemDurability(workspace *RuntimeWorkspace) string {
	if workspace == nil || workspace.Filesystem == nil {
		return ""
	}
	return workspace.Filesystem.Durability
}

func runtimeFilesystemRetention(workspace *RuntimeWorkspace) string {
	if workspace == nil || workspace.Filesystem == nil {
		return ""
	}
	return workspace.Filesystem.Retention
}

func runtimeProviderWorkspaceID(workspace *RuntimeWorkspace) string {
	if workspace == nil {
		return ""
	}
	return workspace.ProviderWorkspaceID
}

func runtimeWorkspaceOwner(workspace *RuntimeWorkspace) string {
	if workspace == nil {
		return ""
	}
	return workspace.Owner
}

func capabilityBoolPointer(src, objectName, fieldName string, fallback *bool) *bool {
	if value, ok := capabilityBoolField(src, objectName, fieldName); ok {
		return boolPointer(value)
	}
	return fallback
}

func capabilityBoolField(src, objectName, fieldName string) (bool, bool) {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(objectName) + `\s*:\s*\{[^{}]*\b` + regexp.QuoteMeta(fieldName) + `\s*:\s*(true|false)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return m[1] == "true", true
	}
	return false, false
}

func capabilityStringField(src, objectName, fieldName string) string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(objectName) + `\s*:\s*\{[^{}]*\b` + regexp.QuoteMeta(fieldName) + `\s*:\s*['"]([^'"]+)['"]`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func capabilityArrayField(src, objectName, fieldName string) []string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(objectName) + `\s*:\s*\{[^{}]*\b` + regexp.QuoteMeta(fieldName) + `\s*:\s*\[([^\]]*)\]`)
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

func boolPointer(value bool) *bool {
	return &value
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
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
