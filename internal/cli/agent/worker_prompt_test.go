package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// fullWorkerPromptData is a PromptData with every field populated, so a render
// that silently drops a field is visible in the output.
func fullWorkerPromptData() PromptData {
	return PromptData{
		AgentName:       "falcon",
		WorktreeName:    "falcon",
		Role:            "app-architect",
		TaskID:          "proj-abc.5",
		EpicID:          "proj-abc",
		WorkspaceBlock:  "WORKSPACE-BLOCK",
		EpicScope:       "EPIC-SCOPE",
		SafetyBlock:     "SAFETY-BLOCK",
		CheckpointBlock: "CHECKPOINT-BLOCK",
		TaskDetail:      "TASK-DETAIL",
		DesignFormat:    "markdown",
	}
}

// generateWorkerPrompt renders a worker prompt with a fixed context. Production
// goes through generateWorkerPromptWith, which builds the context lazily from
// the fields the body references; the tests below supply it eagerly instead.
func generateWorkerPrompt(id string, data PromptData) (string, error) {
	return generateWorkerPromptWith(id, func(promptFieldRefs) PromptData { return data })
}

// TestGenerateWorkerPromptRendersEveryRegisteredPrompt is the guard §10.8 asks
// for: every embedded team-*.md must EXECUTE against PromptData, not merely
// parse. A body copied from planning.md would reference a promptTemplateData
// field (.DesignFormat aside, e.g. .ReadyJSON or .TestStep) that PromptData
// does not carry, and text/template only discovers that at execution time —
// i.e. at agent spawn, in production, if this test did not exist.
func TestGenerateWorkerPromptRendersEveryRegisteredPrompt(t *testing.T) {
	isolatePromptOverrides(t)

	for _, p := range domain.BuiltinWorkerPrompts() {
		t.Run(p.ID, func(t *testing.T) {
			prompt, err := generateWorkerPrompt(p.ID, fullWorkerPromptData())
			if err != nil {
				t.Fatalf("generateWorkerPrompt(%q) error = %v", p.ID, err)
			}
			if strings.TrimSpace(prompt) == "" {
				t.Fatalf("generateWorkerPrompt(%q) rendered empty", p.ID)
			}
			if strings.Contains(prompt, "{{") || strings.Contains(prompt, "}}") {
				t.Errorf("generateWorkerPrompt(%q) left an unrendered template action", p.ID)
			}
			for _, want := range []string{"falcon", "SAFETY-BLOCK", "loom complete"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("generateWorkerPrompt(%q) missing %q", p.ID, want)
				}
			}
		})
	}
}

// TestWorkerPromptsCarryWorkerContext pins the context decision: the worker
// bodies render with PromptData, so the worker-only fields (a pre-claimed task
// and its detail, the crash checkpoint) reach the model. Rendering them through
// the interactive renderer would drop all of these.
func TestWorkerPromptsCarryWorkerContext(t *testing.T) {
	isolatePromptOverrides(t)

	for _, p := range domain.BuiltinWorkerPrompts() {
		t.Run(p.ID, func(t *testing.T) {
			prompt, err := generateWorkerPrompt(p.ID, fullWorkerPromptData())
			if err != nil {
				t.Fatalf("generateWorkerPrompt(%q) error = %v", p.ID, err)
			}
			for _, want := range []string{"proj-abc.5", "TASK-DETAIL", "CHECKPOINT-BLOCK", "WORKSPACE-BLOCK"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("generateWorkerPrompt(%q) dropped worker context %q", p.ID, want)
				}
			}
		})
	}
}

// TestWorkerPromptsWithoutPreClaimedTask covers the other half of the same
// branch: in one-shot and auto mode no task is claimed yet, so the body has to
// tell the agent how to select one instead of pointing at an empty task ID.
func TestWorkerPromptsWithoutPreClaimedTask(t *testing.T) {
	isolatePromptOverrides(t)

	data := fullWorkerPromptData()
	data.TaskID = ""
	data.TaskDetail = ""

	for _, p := range domain.BuiltinWorkerPrompts() {
		t.Run(p.ID, func(t *testing.T) {
			prompt, err := generateWorkerPrompt(p.ID, data)
			if err != nil {
				t.Fatalf("generateWorkerPrompt(%q) error = %v", p.ID, err)
			}
			if !strings.Contains(prompt, "loom data claim") {
				t.Errorf("generateWorkerPrompt(%q) has no claim instructions for an unclaimed run", p.ID)
			}
		})
	}
}

func TestImplementationPromptsHandOffToQA(t *testing.T) {
	isolatePromptOverrides(t)
	for _, id := range []string{"team-frontend-dev", "team-backend-dev", "team-content-writer", "team-agent-dev", "team-data-engineer"} {
		t.Run(id, func(t *testing.T) {
			prompt, err := generateWorkerPrompt(id, fullWorkerPromptData())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompt, "--add-label delivery-pending") || !strings.Contains(prompt, "supervisor publishes") {
				t.Fatalf("%s has no supervisor-owned ready-for-qa handoff", id)
			}
		})
	}
}

func TestImplementationPromptsTreatRoutingStateAsApproval(t *testing.T) {
	isolatePromptOverrides(t)
	tests := []struct {
		id    string
		label string
	}{
		{id: "team-frontend-dev", label: "frontend"},
		{id: "team-backend-dev", label: "backend"},
		{id: "team-content-writer", label: "content"},
		{id: "team-data-engineer", label: "data"},
	}
	for _, tt := range tests {
		id := tt.id
		t.Run(id, func(t *testing.T) {
			prompt, err := generateWorkerPrompt(id, fullWorkerPromptData())
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"authoritative approval contract",
				"status is `open`",
				"does not carry `architect`",
				"do not re-add `architect`",
				"carries `" + tt.label + "`",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s missing approval rule %q", id, want)
				}
			}
		})
	}
}

func TestDesignPromptsClassifyImplementationLane(t *testing.T) {
	isolatePromptOverrides(t)

	tests := []struct {
		id   string
		role string
		want []string
	}{
		{id: "team-architect", role: "api-architect", want: []string{"exactly one", "`backend`", "`data`", "--add-label", "--remove-label"}},
		{id: "team-architect", role: "app-architect", want: []string{"exactly one", "`frontend`", "`backend`", "--add-label", "--remove-label"}},
		{id: "team-web-designer", role: "web-designer", want: []string{"exactly one", "`frontend`", "`content`", "--add-label", "--remove-label"}},
	}

	for _, tt := range tests {
		t.Run(tt.id+"/"+tt.role, func(t *testing.T) {
			data := fullWorkerPromptData()
			data.Role = tt.role
			prompt, err := generateWorkerPrompt(tt.id, data)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s as %s missing lane-classification rule %q", tt.id, tt.role, want)
				}
			}
		})
	}
}

func TestQAPromptRoutesFiledDefectsToArchitect(t *testing.T) {
	isolatePromptOverrides(t)
	prompt, err := generateWorkerPrompt("team-qa", fullWorkerPromptData())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "--label architect") {
		t.Fatal("team-qa defect command does not route defects to an architect")
	}
}

// TestDesignPromptsUseWorkspaceDesignFormat is the .DesignFormat trap from
// §10.8, from the other direction: the design-producing bodies DO need the
// field, which is why PromptData now carries it.
func TestDesignPromptsUseWorkspaceDesignFormat(t *testing.T) {
	isolatePromptOverrides(t)

	for _, id := range []string{"team-architect", "team-web-designer"} {
		t.Run(id, func(t *testing.T) {
			data := fullWorkerPromptData()
			data.DesignFormat = "html"
			prompt, err := generateWorkerPrompt(id, data)
			if err != nil {
				t.Fatalf("generateWorkerPrompt(%q) error = %v", id, err)
			}
			if !strings.Contains(prompt, "--design-format=html") {
				t.Errorf("%s did not pass the workspace design format to loom data update", id)
			}
			if !strings.Contains(prompt, "Design format: HTML") {
				t.Errorf("%s did not switch to the HTML authoring instructions", id)
			}
		})
	}
}

// TestWorkerPromptRoleLens covers the shared-body mechanism: one embedded file
// serves several agent roles and selects its domain lens from {{.Role}}.
func TestWorkerPromptRoleLens(t *testing.T) {
	isolatePromptOverrides(t)

	tests := []struct {
		id         string
		role       string
		want       string
		notWant    string
		wantReason string
	}{
		{id: "team-architect", role: "agent-architect", want: "agent systems", notWant: "full-stack application"},
		{id: "team-architect", role: "api-architect", want: "API contract", notWant: "full-stack application"},
		{id: "team-architect", role: "app-architect", want: "full-stack application", notWant: "agent systems"},
		{id: "team-qa", role: "site-qa", want: "accessibility and cross-browser", notWant: "integration and contract"},
		{id: "team-qa", role: "qa-engineer", want: "integration and contract", notWant: "accessibility and cross-browser"},
	}

	for _, tt := range tests {
		t.Run(tt.id+"/"+tt.role, func(t *testing.T) {
			data := fullWorkerPromptData()
			data.Role = tt.role
			prompt, err := generateWorkerPrompt(tt.id, data)
			if err != nil {
				t.Fatalf("generateWorkerPrompt(%q) error = %v", tt.id, err)
			}
			if !strings.Contains(prompt, tt.want) {
				t.Errorf("%s as %s: missing lens text %q", tt.id, tt.role, tt.want)
			}
			if strings.Contains(prompt, tt.notWant) {
				t.Errorf("%s as %s: leaked the other lens %q", tt.id, tt.role, tt.notWant)
			}
			if !strings.Contains(prompt, tt.role) {
				t.Errorf("%s as %s: prompt never names the agent role", tt.id, tt.role)
			}
		})
	}
}

func TestGenerateWorkerPromptUnknownID(t *testing.T) {
	isolatePromptOverrides(t)

	_, err := generateWorkerPrompt("team-nope", fullWorkerPromptData())
	if err == nil {
		t.Fatal("generateWorkerPrompt(\"team-nope\") error = nil, want unknown-prompt error")
	}
	if !strings.Contains(err.Error(), "team-nope") {
		t.Errorf("error %q does not name the unknown prompt", err)
	}
	if !strings.Contains(err.Error(), "team-architect") {
		t.Errorf("error %q does not list the registered prompts", err)
	}
}

// TestGenerateWorkerPromptRejectsInteractiveID keeps the two registries apart at
// the renderer: lead.md is embedded right next to the team bodies, so nothing
// but the registry check stops a worker agent role from booting the terminal
// lead prompt.
func TestGenerateWorkerPromptRejectsInteractiveID(t *testing.T) {
	isolatePromptOverrides(t)

	if _, err := generateWorkerPrompt("lead", fullWorkerPromptData()); err == nil {
		t.Fatal("generateWorkerPrompt(\"lead\") error = nil, want rejection of an interactive prompt ID")
	}
}

// TestGenerateWorkerPromptOverrideFallback covers the per-project override hook
// and its failure mode: an override that names a field PromptData does not
// carry (ReadyJSON belongs to the built-in context) must not take the agent
// down — the shipped body is used instead.
func TestGenerateWorkerPromptOverrideFallback(t *testing.T) {
	overrideDir := filepath.Join(t.TempDir(), "project")
	promptDir := filepath.Join(overrideDir, "loom-prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "team-qa.md"), []byte("QA override for {{ .ReadyJSON }}"), 0o600); err != nil {
		t.Fatal(err)
	}
	promptOverrideDir = overrideDir
	t.Cleanup(func() { promptOverrideDir = "" })

	prompt, err := generateWorkerPrompt("team-qa", fullWorkerPromptData())
	if err != nil {
		t.Fatalf("generateWorkerPrompt(\"team-qa\") error = %v, want fallback to the embedded body", err)
	}
	if !strings.Contains(prompt, "WORKFLOW: QA Task") {
		t.Errorf("expected the embedded QA body after the override failed, got: %q", truncateForError(prompt))
	}
}

func TestGenerateWorkerPromptUsesValidOverride(t *testing.T) {
	overrideDir := filepath.Join(t.TempDir(), "project")
	promptDir := filepath.Join(overrideDir, "loom-prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "team-qa.md"), []byte("QA override for {{ .AgentName }}"), 0o600); err != nil {
		t.Fatal(err)
	}
	promptOverrideDir = overrideDir
	t.Cleanup(func() { promptOverrideDir = "" })

	prompt, err := generateWorkerPrompt("team-qa", fullWorkerPromptData())
	if err != nil {
		t.Fatalf("generateWorkerPrompt(\"team-qa\") error = %v", err)
	}
	if prompt != "QA override for falcon" {
		t.Errorf("prompt = %q, want the rendered override", prompt)
	}
}

// TestGenerateWorkerPromptGatesExpensiveFields proves the embedded bodies go
// through the same lazy context build as custom prompt files: the renderer
// reports which fields the body names, which is what lets a spawn skip the
// issue-backend round trip {{.TaskDetail}} would cost.
func TestGenerateWorkerPromptGatesExpensiveFields(t *testing.T) {
	isolatePromptOverrides(t)

	var seen promptFieldRefs
	if _, err := generateWorkerPromptWith("team-backend-dev", func(refs promptFieldRefs) PromptData {
		seen = refs
		return fullWorkerPromptData()
	}); err != nil {
		t.Fatalf("generateWorkerPromptWith error = %v", err)
	}
	if seen == nil {
		t.Fatal("the context builder was never called")
	}
	for _, field := range []string{"AgentName", "SafetyBlock", "TaskDetail"} {
		if !seen.has(field) {
			t.Errorf("field refs missing %q; the body references it", field)
		}
	}
	if seen.has("ReadyJSON") {
		t.Error("field refs claim ReadyJSON; that field belongs to the built-in context, not PromptData")
	}
}

// --- call site: `loom agent --prompt` flag validation ---

func TestValidatePromptRef(t *testing.T) {
	realPrompt := filepath.Join(t.TempDir(), "reviewer.md")
	if err := os.WriteFile(realPrompt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	tests := []struct {
		name        string
		value       string
		wantErr     bool
		errContains string
	}{
		{name: "registered builtin reference", value: "builtin:team-architect"},
		{name: "registered builtin reference with spaces", value: "builtin: team-qa"},
		{name: "existing file still works", value: realPrompt},
		{
			name:        "unknown builtin id",
			value:       "builtin:team-nope",
			wantErr:     true,
			errContains: "team-nope",
		},
		{
			name:        "interactive id is not a worker prompt",
			value:       "builtin:pr-review",
			wantErr:     true,
			errContains: "pr-review",
		},
		{
			name:        "missing file",
			value:       filepath.Join(dir, "nope.md"),
			wantErr:     true,
			errContains: "cannot access prompt file",
		},
		{
			name:        "directory",
			value:       dir,
			wantErr:     true,
			errContains: "is a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePromptRef(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validatePromptRef(%q) error = nil, want error", tt.value)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want it to contain %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("validatePromptRef(%q) error = %v, want nil", tt.value, err)
			}
		})
	}
}

// --- call site: the `loom agent` prompt loader ---

// TestMakeCustomPromptGenResolvesBuiltin is the failure §10.8 warns about:
// fixing the flag check without fixing the loader produces an agent role that
// validates and then cannot read its prompt. The generator must render the
// embedded body, not stat the reference.
func TestMakeCustomPromptGenResolvesBuiltin(t *testing.T) {
	isolatePromptOverrides(t)
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "")
	t.Setenv("LOOM_ROLE", "site-qa")

	prompt := makeCustomPromptGen("builtin:team-qa")("site-qa-1", nil)

	if strings.HasPrefix(prompt, "Error:") {
		t.Fatalf("builtin prompt generator failed: %s", truncateForError(prompt))
	}
	for _, want := range []string{"WORKFLOW: QA Task", "site-qa-1", "accessibility and cross-browser", "Never push"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("rendered prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Error("rendered prompt still contains a template action")
	}
}

// TestMakeCustomPromptGenPassesWorkspaceDesignFormat proves the design format
// reaches a worker body from the workspace config, which is the reason
// PromptData grew the field rather than the bodies hardcoding "markdown".
func TestMakeCustomPromptGenPassesWorkspaceDesignFormat(t *testing.T) {
	isolatePromptOverrides(t)
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "")
	t.Setenv("LOOM_ROLE", "app-architect")

	ws := &config.WorkspaceConfig{DesignFormat: "html"}
	prompt := makeCustomPromptGen("builtin:team-architect")("app-architect-1", ws)

	if !strings.Contains(prompt, "--design-format=html") {
		t.Error("workspace design format did not reach the built-in architect prompt")
	}
}

// TestMakeCustomPromptGenStillLoadsFiles guards the unchanged path: an ordinary
// --prompt path must behave exactly as before.
func TestMakeCustomPromptGenStillLoadsFiles(t *testing.T) {
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "")
	t.Setenv("LOOM_ROLE", "critic")

	path := filepath.Join(t.TempDir(), "critic.md")
	if err := os.WriteFile(path, []byte("Critic {{ .AgentName }} as {{ .Role }}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := makeCustomPromptGen(path)("nova", nil); got != "Critic nova as critic" {
		t.Errorf("prompt = %q, want the rendered file", got)
	}
}

// isolatePromptOverrides points the per-project override lookup at an empty
// directory so a stray ./loom-prompts in the working tree cannot change what a
// test renders.
func isolatePromptOverrides(t *testing.T) {
	t.Helper()
	promptOverrideDir = t.TempDir()
	t.Cleanup(func() { promptOverrideDir = "" })
}

func truncateForError(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "..."
}
