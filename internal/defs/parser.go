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
	return RuntimeModule{
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
	}
}

func runtimeWorkspaceSkillDirs(src string) []string {
	return compactStrings(append(arrayField(src, "workspaceSkillDirs"), arrayField(src, "workspace_skill_dirs")...))
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
