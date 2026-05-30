package defs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWriteSourceExportCodifiesWorkspaceDefinitions(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CODEGEN", Name: "Codify"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxConcurrency := 3
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:   "CODEGEN",
		Name:           "triage",
		Description:    "Direct control-plane triage role.",
		Backend:        "codex",
		Model:          "openai/gpt-5",
		Skills:         []string{"triage"},
		AllowedTools:   []string{"github_issue_read", "github.issue.read"},
		MaxConcurrency: &maxConcurrency,
		ReadOnly:       true,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	runtime := RuntimeModule{
		Name:       "remote-e2b",
		Version:    "runtime-v1",
		SourcePath: "control-plane:runtime/remote-e2b",
		SourceHash: "runtime-hash",
		Provider:   domain.RuntimeProviderE2B,
		Image:      "node:22",
		CWD:        ".",
		Repos:      []string{"app"},
		Env:        []string{"NODE_ENV"},
		Workspace: &RuntimeWorkspace{
			ProviderWorkspaceID: "e2b-workspace-1",
			Owner:               "loom",
			Cleanup:             &RuntimeCleanupPolicy{Mode: "after_ttl", TTL: "24h"},
			Filesystem:          &RuntimeFilesystemSpec{Persistence: "session", Durability: "provider", Retention: "2d"},
		},
	}
	if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
		WorkspaceKey: "CODEGEN",
		Name:         runtime.Name,
		Version:      runtime.Version,
		Provider:     runtime.Provider,
		Image:        runtime.Image,
		Repos:        runtime.Repos,
		Env:          runtime.Env,
		Manifest:     mustJSON(runtime),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert runtime profile: %v", err)
	}
	workflowManifest := mustJSON(WorkflowModule{
		Name:               "slack-runner",
		Version:            "workflow-v1",
		SourcePath:         "control-plane:workflow/slack-runner",
		SourceHash:         "workflow-hash",
		Description:        "Run Slack child tasks.",
		RuntimeProfileName: "remote-e2b",
		Builtin:            "run-parent-work-items",
		SingletonPolicy:    "parent:${parentId}",
		Tools:              []string{"github_issue_read", "taskRuns.ensure"},
		Repos:              []string{"app"},
		Env:                []string{"NODE_ENV"},
	})
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:       "CODEGEN",
		Name:               "slack-runner",
		Version:            "workflow-v1",
		Description:        "Run Slack child tasks.",
		RuntimeProfileName: "remote-e2b",
		Manifest:           workflowManifest,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}
	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   "CODEGEN",
		DefinitionName: "slack-runner",
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           "/workflows/slack-runner/run",
		Method:         "POST",
		AuthPolicy:     "workspace",
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert route binding: %v", err)
	}
	if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
		WorkspaceKey: "CODEGEN",
		WorkflowName: "slack-runner",
		EventType:    "issue.label_added",
		Filter:       json.RawMessage(`{"label":"slack","type":"epic"}`),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert trigger binding: %v", err)
	}
	tool := ToolModule{
		Name:        "github_issue_read",
		Description: "Read one GitHub issue.",
		Version:     "tool-v1",
		SourcePath:  "control-plane:tool/github_issue_read",
		SourceHash:  "tool-hash",
		Parameters:  map[string]any{"type": "object", "required": []string{"number"}},
		Handler:     "workflow",
		Runtime:     "remote-e2b",
		Env:         []string{"GITHUB_TOKEN"},
		ReadOnly:    true,
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CODEGEN",
		DefinitionType:     domain.DefinitionTypeTool,
		DefinitionName:     tool.Name,
		Version:            tool.Version,
		SourceHash:         tool.SourceHash,
		Manifest:           mustJSON(tool),
		CapabilityManifest: toolCapabilityManifest(tool),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply tool definition: %v", err)
	}
	skill := SkillModule{
		Name:         "triage",
		Description:  "Triage incoming work.",
		Version:      "skill-v1",
		SourcePath:   "control-plane:skill/triage",
		SourceHash:   "skill-hash",
		Instructions: "Review priority, labels, and blocked state.",
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CODEGEN",
		DefinitionType:     domain.DefinitionTypeSkill,
		DefinitionName:     skill.Name,
		Version:            skill.Version,
		SourceHash:         skill.SourceHash,
		Manifest:           mustJSON(skill),
		CapabilityManifest: skillCapabilityManifest(skill),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply skill definition: %v", err)
	}

	workspacePlan, err := PlanFromWorkspace(ctx, st, "CODEGEN")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	exportRoot := t.TempDir()
	files, err := WriteSourceExport(exportRoot, workspacePlan, false)
	if err != nil {
		t.Fatalf("WriteSourceExport() error = %v", err)
	}
	paths := sourceExportPaths(files)
	for _, want := range []string{
		".loom/agents/triage.ts",
		".loom/runtimes/remote-e2b.ts",
		".loom/skills/triage/SKILL.md",
		".loom/tools/github_issue_read.ts",
		".loom/workflows/slack-runner.ts",
	} {
		if !containsString(paths, want) {
			t.Fatalf("exported paths = %+v, missing %s", paths, want)
		}
	}

	exportedPlan, err := Load(exportRoot)
	if err != nil {
		t.Fatalf("Load(exportRoot) error = %v", err)
	}
	if got := Summary(exportedPlan); got != "agents=1 workflows=1 runtimes=1 skills=1 tools=1" {
		t.Fatalf("Summary(exportedPlan) = %q, want source-codified definitions", got)
	}
	agent := exportedPlan.Agents[0]
	if agent.Name != "triage" || agent.Model != "openai/gpt-5" || agent.Backend != "codex" ||
		!containsString(agent.Tools, "github_issue_read") || !containsString(agent.Tools, "github.issue.read") ||
		!containsString(agent.Skills, "triage") ||
		agent.MaxConcurrency != 3 || !agent.ReadOnly {
		t.Fatalf("exported agent = %+v, want control-plane role codified as source", agent)
	}
	workflow := exportedPlan.Workflows[0]
	if workflow.Name != "slack-runner" || workflow.RuntimeProfileName != "remote-e2b" ||
		workflow.Builtin != "run-parent-work-items" ||
		workflow.RoutePath != "/workflows/slack-runner/run" ||
		workflow.RouteAuth != "workspace" ||
		workflow.TriggerEvent != "issue.label_added" ||
		workflow.TriggerFilter["label"] != "slack" ||
		!containsString(workflow.Tools, "github_issue_read") {
		t.Fatalf("exported workflow = %+v, want route/trigger/tool source codification", workflow)
	}
	exportedRuntime := exportedPlan.Runtimes[0]
	if exportedRuntime.Name != "remote-e2b" || exportedRuntime.Provider != domain.RuntimeProviderE2B ||
		exportedRuntime.Workspace == nil ||
		exportedRuntime.Workspace.ProviderWorkspaceID != "e2b-workspace-1" ||
		exportedRuntime.Workspace.Filesystem == nil ||
		exportedRuntime.Workspace.Filesystem.Durability != "provider" {
		t.Fatalf("exported runtime = %+v, want runtime workspace policy codified", exportedRuntime)
	}
	if exportedPlan.Tools[0].Name != "github_issue_read" || exportedPlan.Tools[0].Handler != "workflow" ||
		exportedPlan.Tools[0].Runtime != "remote-e2b" || !exportedPlan.Tools[0].ReadOnly {
		t.Fatalf("exported tools = %+v, want typed tool source codification", exportedPlan.Tools)
	}
	if exportedPlan.Skills[0].Name != "triage" || !strings.Contains(exportedPlan.Skills[0].Instructions, "Review priority") {
		t.Fatalf("exported skills = %+v, want skill source codification", exportedPlan.Skills)
	}

	agentPath := filepath.Join(exportRoot, ".loom", "agents", "triage.ts")
	if err := os.WriteFile(agentPath, []byte("// user edit\n"), 0o644); err != nil {
		t.Fatalf("mutate exported agent: %v", err)
	}
	if _, err := WriteSourceExport(exportRoot, workspacePlan, false); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("WriteSourceExport() error = %v, want non-force collision", err)
	}
	if _, err := WriteSourceExport(exportRoot, workspacePlan, true); err != nil {
		t.Fatalf("WriteSourceExport(force) error = %v", err)
	}
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read force-overwritten agent: %v", err)
	}
	if !strings.Contains(string(data), "createAgent") || strings.Contains(string(data), "user edit") {
		t.Fatalf("force-overwritten agent source = %s, want generated source", data)
	}
}

func TestExportSourceFilesRejectsSanitizedPathCollisions(t *testing.T) {
	_, err := ExportSourceFiles(&Plan{
		Agents: []AgentModule{
			{Name: "review/bot"},
			{Name: "review bot"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), ".loom/agents/review-bot.ts") {
		t.Fatalf("ExportSourceFiles() error = %v, want sanitized path collision", err)
	}
}

func sourceExportPaths(files []SourceExportFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}
