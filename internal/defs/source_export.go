package defs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type SourceExportFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

func ExportSourceFiles(plan *Plan) ([]SourceExportFile, error) {
	if plan == nil {
		return nil, fmt.Errorf("definition plan required")
	}
	var files []SourceExportFile
	for _, agent := range plan.Agents {
		files = append(files, SourceExportFile{
			Path:     filepath.ToSlash(filepath.Join(".loom", "agents", sourceFileName(agent.Name))),
			Contents: renderAgentSource(agent),
		})
	}
	for _, workflow := range plan.Workflows {
		files = append(files, SourceExportFile{
			Path:     filepath.ToSlash(filepath.Join(".loom", "workflows", sourceFileName(workflow.Name))),
			Contents: renderWorkflowSource(workflow),
		})
	}
	for _, runtime := range plan.Runtimes {
		files = append(files, SourceExportFile{
			Path:     filepath.ToSlash(filepath.Join(".loom", "runtimes", sourceFileName(runtime.Name))),
			Contents: renderRuntimeSource(runtime),
		})
	}
	for _, tool := range plan.Tools {
		files = append(files, SourceExportFile{
			Path:     filepath.ToSlash(filepath.Join(".loom", "tools", sourceFileName(tool.Name))),
			Contents: renderToolSource(tool),
		})
	}
	for _, skill := range plan.Skills {
		files = append(files, SourceExportFile{
			Path:     filepath.ToSlash(filepath.Join(".loom", "skills", sourcePathPart(skill.Name), "SKILL.md")),
			Contents: renderSkillSource(skill),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := 1; i < len(files); i++ {
		if files[i].Path == files[i-1].Path {
			return nil, fmt.Errorf("multiple definitions export to %s", files[i].Path)
		}
	}
	return files, nil
}

func WriteSourceExport(root string, plan *Plan, force bool) ([]SourceExportFile, error) {
	files, err := ExportSourceFiles(plan)
	if err != nil {
		return nil, err
	}
	return writeExportFiles(root, files, force)
}

func writeExportFiles(root string, files []SourceExportFile, force bool) ([]SourceExportFile, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	for _, file := range files {
		target := filepath.Clean(filepath.Join(root, filepath.FromSlash(file.Path)))
		if !isPathWithin(root, target) {
			return nil, fmt.Errorf("source export path escapes target root: %s", file.Path)
		}
		if existing, err := os.ReadFile(target); err == nil {
			if string(existing) == file.Contents {
				continue
			}
			if !force {
				return nil, fmt.Errorf("%s already exists; pass --force to overwrite", target)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, []byte(file.Contents), 0o644); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func renderAgentSource(agent AgentModule) string {
	var b bytes.Buffer
	b.WriteString("import { createAgent } from '@loom/runtime';\n\n")
	b.WriteString("export default createAgent({\n")
	writeStringProp(&b, "name", agent.Name, 1)
	writeStringProp(&b, "description", agent.Description, 1)
	writeStringProp(&b, "backend", agent.Backend, 1)
	writeStringProp(&b, "model", agent.Model, 1)
	writeStringProp(&b, "profileName", agent.ProfileName, 1)
	writeTemplateProp(&b, "instructions", agent.Instructions, 1)
	writeStringArrayProp(&b, "skills", agent.Skills, 1)
	writeStringArrayProp(&b, "tools", agent.Tools, 1)
	writeStringArrayProp(&b, "allowedCommands", agent.AllowedCommands, 1)
	writeStringArrayProp(&b, "deniedCommands", agent.DeniedCommands, 1)
	writeStringArrayProp(&b, "repos", agent.Repos, 1)
	writeStringArrayProp(&b, "env", agent.Env, 1)
	writeIntProp(&b, "maxConcurrency", agent.MaxConcurrency, 1)
	if agent.MaxBudgetUSD != nil {
		writeRawProp(&b, "maxBudgetUSD", fmt.Sprintf("%g", *agent.MaxBudgetUSD), 1)
	}
	writeBoolProp(&b, "readOnly", agent.ReadOnly, 1)
	b.WriteString("});\n")
	return b.String()
}

func renderWorkflowSource(workflow WorkflowModule) string {
	var b bytes.Buffer
	b.WriteString("import { defineWorkflow } from '@loom/runtime';\n\n")
	b.WriteString("export default defineWorkflow({\n")
	writeStringProp(&b, "name", workflow.Name, 1)
	writeStringProp(&b, "description", workflow.Description, 1)
	writeStringProp(&b, "runtimeProfile", workflow.RuntimeProfileName, 1)
	writeStringProp(&b, "builtin", workflow.Builtin, 1)
	writeStringProp(&b, "runner", workflow.Runner, 1)
	writeStringProp(&b, "singleton", workflow.SingletonPolicy, 1)
	writeStringProp(&b, "path", workflow.RoutePath, 1)
	writeStringProp(&b, "auth", workflow.RouteAuth, 1)
	if workflow.TriggerEvent == "issue.label_added" && len(workflow.TriggerFilter) > 0 {
		writeRawProp(&b, "issueLabelAdded", renderStringMap(workflow.TriggerFilter, 1), 1)
	} else if workflow.TriggerEvent != "" {
		writeStringProp(&b, "triggerEvent", workflow.TriggerEvent, 1)
		writeRawProp(&b, "triggerFilter", renderStringMap(workflow.TriggerFilter, 1), 1)
	}
	writeStringArrayProp(&b, "tools", workflow.Tools, 1)
	writeStringArrayProp(&b, "repos", workflow.Repos, 1)
	writeStringArrayProp(&b, "env", workflow.Env, 1)
	b.WriteString("});\n")
	return b.String()
}

func renderRuntimeSource(runtime RuntimeModule) string {
	var b bytes.Buffer
	b.WriteString("import { runtime } from '@loom/runtime';\n\n")
	b.WriteString("export default ")
	switch runtime.Provider {
	case domain.RuntimeProviderLocal, "":
		b.WriteString("runtime.local")
	case domain.RuntimeProviderOther:
		b.WriteString("runtime.podman")
	default:
		b.WriteString("runtime.remote")
	}
	b.WriteString("({\n")
	writeStringProp(&b, "name", runtime.Name, 1)
	if runtime.Provider != "" && runtime.Provider != domain.RuntimeProviderLocal && runtime.Provider != domain.RuntimeProviderOther {
		writeStringProp(&b, "provider", string(runtime.Provider), 1)
	}
	writeStringProp(&b, "image", runtime.Image, 1)
	writeStringProp(&b, "cwd", runtime.CWD, 1)
	writeStringProp(&b, "cpu", runtime.CPU, 1)
	writeStringProp(&b, "memory", runtime.Memory, 1)
	writeStringArrayProp(&b, "repos", runtime.Repos, 1)
	writeStringArrayProp(&b, "env", runtime.Env, 1)
	writeStringArrayProp(&b, "workspaceSkillDirs", runtime.WorkspaceSkillDirs, 1)
	writeRuntimeWorkspaceProp(&b, runtime.Workspace, 1)
	writeRuntimeCapabilitiesProp(&b, runtime.Capabilities, 1)
	b.WriteString("});\n")
	return b.String()
}

func renderToolSource(tool ToolModule) string {
	var b bytes.Buffer
	b.WriteString("import { defineTool } from '@loom/runtime';\n\n")
	b.WriteString("export default defineTool({\n")
	writeStringProp(&b, "name", tool.Name, 1)
	writeStringProp(&b, "description", tool.Description, 1)
	writeRawProp(&b, "parameters", renderJSONValue(tool.Parameters, 1), 1)
	writeStringProp(&b, "handler", tool.Handler, 1)
	writeStringProp(&b, "runtime", tool.Runtime, 1)
	writeStringProp(&b, "timeout", tool.Timeout, 1)
	writeBoolProp(&b, "cancellable", tool.Cancellable, 1)
	writeStringArrayProp(&b, "repos", tool.Repos, 1)
	writeStringArrayProp(&b, "env", tool.Env, 1)
	writeBoolProp(&b, "readOnly", tool.ReadOnly, 1)
	b.WriteString("});\n")
	return b.String()
}

func renderSkillSource(skill SkillModule) string {
	var b bytes.Buffer
	b.WriteString("---\n")
	b.WriteString("name: " + skill.Name + "\n")
	if skill.Description != "" {
		b.WriteString("description: " + skill.Description + "\n")
	}
	b.WriteString("---\n\n")
	if strings.TrimSpace(skill.Instructions) != "" {
		b.WriteString(strings.TrimRight(skill.Instructions, "\n") + "\n")
	}
	return b.String()
}

func writeRuntimeWorkspaceProp(b *bytes.Buffer, workspace *RuntimeWorkspace, indent int) {
	if workspace == nil {
		return
	}
	var inner bytes.Buffer
	writeStringProp(&inner, "providerWorkspaceId", workspace.ProviderWorkspaceID, indent+1)
	writeStringProp(&inner, "owner", workspace.Owner, indent+1)
	if workspace.Cleanup != nil {
		writeRawProp(&inner, "cleanup", renderRuntimeCleanup(workspace.Cleanup, indent+1), indent+1)
	}
	if workspace.Filesystem != nil {
		writeRawProp(&inner, "filesystem", renderRuntimeFilesystem(workspace.Filesystem, indent+1), indent+1)
	}
	if inner.Len() == 0 {
		return
	}
	writeRawProp(b, "workspace", "{\n"+inner.String()+indentString(indent)+"}", indent)
}

func renderRuntimeCleanup(policy *RuntimeCleanupPolicy, indent int) string {
	var b bytes.Buffer
	b.WriteString("{\n")
	writeStringProp(&b, "mode", policy.Mode, indent+1)
	writeStringProp(&b, "ttl", policy.TTL, indent+1)
	writeStringProp(&b, "retention", policy.Retention, indent+1)
	b.WriteString(indentString(indent) + "}")
	return b.String()
}

func renderRuntimeFilesystem(spec *RuntimeFilesystemSpec, indent int) string {
	var b bytes.Buffer
	b.WriteString("{\n")
	writeStringProp(&b, "persistence", spec.Persistence, indent+1)
	writeStringProp(&b, "durability", spec.Durability, indent+1)
	writeStringProp(&b, "retention", spec.Retention, indent+1)
	b.WriteString(indentString(indent) + "}")
	return b.String()
}

func writeRuntimeCapabilitiesProp(b *bytes.Buffer, caps *RuntimeCapabilities, indent int) {
	if caps == nil {
		return
	}
	var inner bytes.Buffer
	writeRawProp(&inner, "filesystem", renderJSONValue(caps.Filesystem, indent+1), indent+1)
	writeRawProp(&inner, "shell", renderJSONValue(caps.Shell, indent+1), indent+1)
	writeRawProp(&inner, "network", renderJSONValue(caps.Network, indent+1), indent+1)
	writeRawProp(&inner, "env", renderJSONValue(caps.Env, indent+1), indent+1)
	writeRawProp(&inner, "workspace", renderJSONValue(caps.Workspace, indent+1), indent+1)
	writeRawProp(&inner, "lifecycle", renderJSONValue(caps.Lifecycle, indent+1), indent+1)
	if inner.Len() == 0 {
		return
	}
	writeRawProp(b, "capabilities", "{\n"+inner.String()+indentString(indent)+"}", indent)
}

func writeStringProp(b *bytes.Buffer, name, value string, indent int) {
	if strings.TrimSpace(value) == "" {
		return
	}
	writeRawProp(b, name, quoteTS(value), indent)
}

func writeTemplateProp(b *bytes.Buffer, name, value string, indent int) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if strings.Contains(value, "\n") {
		writeRawProp(b, name, "`"+strings.ReplaceAll(value, "`", "\\`")+"`", indent)
		return
	}
	writeStringProp(b, name, value, indent)
}

func writeStringArrayProp(b *bytes.Buffer, name string, values []string, indent int) {
	values = compactStrings(values)
	if len(values) == 0 {
		return
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteTS(value))
	}
	writeRawProp(b, name, "["+strings.Join(quoted, ", ")+"]", indent)
}

func writeBoolProp(b *bytes.Buffer, name string, value bool, indent int) {
	if !value {
		return
	}
	writeRawProp(b, name, "true", indent)
}

func writeIntProp(b *bytes.Buffer, name string, value int, indent int) {
	if value == 0 {
		return
	}
	writeRawProp(b, name, fmt.Sprintf("%d", value), indent)
}

func writeRawProp(b *bytes.Buffer, name, raw string, indent int) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" || strings.TrimSpace(raw) == "{}" {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(raw)
	b.WriteString(",\n")
}

func renderStringMap(values map[string]string, indent int) string {
	if len(values) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("{\n")
	for _, key := range keys {
		writeStringProp(&b, key, values[key], indent+1)
	}
	b.WriteString(indentString(indent) + "}")
	return b.String()
}

func renderJSONValue(value any, indent int) string {
	if value == nil {
		return ""
	}
	data, err := json.MarshalIndent(value, indentString(indent), "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func quoteTS(value string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'", "\n", "\\n", "\r", "\\r").Replace(value) + "'"
}

func indentString(indent int) string {
	return strings.Repeat("  ", indent)
}

func sourceFileName(name string) string {
	return sourcePathPart(name) + ".ts"
}

func sourcePathPart(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "definition"
	}
	return out
}

func isPathWithin(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
