package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/testutil"
)

func TestGeneratePlanningPrompt(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		wantParts []string
	}{
		{
			name:      "falcon agent",
			agentName: "falcon",
			wantParts: []string{
				"Your agent name is: falcon",
				"loom data claim",
				"Planning Task",
				"Do NOT write any implementation code",
				"--status review",
				"needs-revision",
			},
		},
		{
			name:      "nova agent",
			agentName: "nova",
			wantParts: []string{
				"Your agent name is: nova",
				"loom data claim",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GeneratePlanningPrompt(tc.agentName, nil, "")

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
	}
}

func TestGenerateTaskPrompt(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		wantParts []string
	}{
		{
			name:      "ember agent",
			agentName: "ember",
			wantParts: []string{
				"Your agent name is: ember",
				"loom data claim",  // Main task claiming uses the backend claim API.
				"--assignee ember", // Reclaiming stale tasks still sets assignee.
				"Implementation Task",
				"--design",
				"loom stack publish <stack-id>",
				"git branch -f <output-branch> HEAD",
				"loom plan",
			},
		},
		{
			name:      "zephyr agent",
			agentName: "zephyr",
			wantParts: []string{
				"Your agent name is: zephyr",
				"loom data claim",
				"--assignee zephyr",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateTaskPrompt(tc.agentName, nil, "", "claude")

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
	}
}

func TestGeneratedPromptsRecognizeArtifactBackedDesigns(t *testing.T) {
	planning := GeneratePlanningPrompt("planner", nil, "")
	task := GenerateTaskPrompt("coder", nil, "", "claude")

	for name, prompt := range map[string]string{"planning": planning, "task": task} {
		for _, field := range []string{".has_design", ".design_artifact_id", ".design"} {
			if !strings.Contains(prompt, field) {
				t.Errorf("%s prompt missing artifact-aware filter field %q", name, field)
			}
		}
	}
	if strings.Contains(task, "select(.design) |") {
		t.Error("task prompt still requires an inline design body")
	}
}

func TestGeneratePlanningPrompt_WithParent(t *testing.T) {
	prompt := GeneratePlanningPrompt("falcon", nil, "my-epic-abc")

	wantParts := []string{
		"loom data ready --parent my-epic-abc --limit 200 --output json",
		"loom data ready --parent my-epic-abc --limit 200",
		"Epic scope: my-epic-abc",
		"MUST only select tasks from this epic",
	}
	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("planning prompt with parentID missing expected part: %q", part)
		}
	}

	// Should NOT contain unscoped ready.
	if strings.Contains(prompt, "loom data ready --output json |") {
		t.Error("planning prompt with parentID should not contain unscoped 'loom data ready --output json |'")
	}
}

func TestGenerateTaskPrompt_WithParent(t *testing.T) {
	prompt := GenerateTaskPrompt("nova", nil, "proj-xyz", "claude")

	wantParts := []string{
		"loom data ready --parent proj-xyz --limit 200 --output json",
		"loom data ready --parent proj-xyz --limit 200",
		"Epic scope: proj-xyz",
		"MUST only select tasks from this epic",
	}
	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("task prompt with parentID missing expected part: %q", part)
		}
	}

	// Should NOT contain unscoped ready.
	if strings.Contains(prompt, "loom data ready --output json |") {
		t.Error("task prompt with parentID should not contain unscoped 'loom data ready --output json |'")
	}
}

func TestGeneratePrompts_NoParent_NoEpicScope(t *testing.T) {
	planPrompt := GeneratePlanningPrompt("test", nil, "")
	taskPrompt := GenerateTaskPrompt("test", nil, "", "claude")

	if strings.Contains(planPrompt, "Epic scope") {
		t.Error("planning prompt without parentID should not contain 'Epic scope'")
	}
	if strings.Contains(taskPrompt, "Epic scope") {
		t.Error("task prompt without parentID should not contain 'Epic scope'")
	}

	// Should contain unscoped backend-aware ready.
	if !strings.Contains(planPrompt, "loom data ready --limit 200 --output json") {
		t.Error("planning prompt without parentID should contain 'loom data ready --limit 200 --output json'")
	}
	if !strings.Contains(taskPrompt, "loom data ready --limit 200 --output json") {
		t.Error("task prompt without parentID should contain 'loom data ready --limit 200 --output json'")
	}
}

func TestGenerateConflictResolutionPrompt(t *testing.T) {
	tests := []struct {
		name         string
		sourceBranch string
		targetBranch string
		conflicts    []string
		wantParts    []string
	}{
		{
			name:         "single conflict",
			sourceBranch: "feature/test",
			targetBranch: "main",
			conflicts:    []string{"src/main.go"},
			wantParts: []string{
				"feature/test",
				"main",
				"src/main.go",
				"Resolve Merge Conflicts",
			},
		},
		{
			name:         "multiple conflicts",
			sourceBranch: "feature/auth",
			targetBranch: "develop",
			conflicts:    []string{"pkg/auth.go", "pkg/util.go", "README.md"},
			wantParts: []string{
				"feature/auth",
				"develop",
				"pkg/auth.go",
				"pkg/util.go",
				"README.md",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateConflictResolutionPrompt(tc.sourceBranch, tc.targetBranch, tc.conflicts)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
	}
}

func TestGenerateLeadPrompt(t *testing.T) {
	prompt := GenerateLeadPrompt()

	// Check for key sections
	wantParts := []string{
		"INTERACTIVE MODE: Project Lead",
		"On Startup",
		"Available Actions",
		"Review Plans",
		"Create Tickets",
		"Triage Backlog",
		"Check Status",
		"Manage Repos or Agents",
		"Runtime and Daemon Rules",
		"Interaction Style",
		"Loom CLI Reference",
		"Important Notes",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt missing expected part: %q", part)
		}
	}

	// Check for loom commands documentation
	loomCommands := []string{
		"loom plan",
		"loom task",
		"loom workspace ops",
		"loom repo list",
		"loom agentdef add",
		"loom monitor",
		"loom merge",
		"loom sync",
		"loom reset",
		"loom list",
		"--auto",
		"--help",
	}

	for _, cmd := range loomCommands {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt missing loom command: %q", cmd)
		}
	}

	// Check for agent status indicators
	statusIndicators := []string{
		"ready",
		"working:",
		"planning:",
		"review:",
		"idle",
		"error:",
	}

	for _, status := range statusIndicators {
		if !strings.Contains(prompt, status) {
			t.Errorf("prompt missing status indicator: %q", status)
		}
	}

	if strings.Contains(prompt, "loom data claim <id>") {
		t.Errorf("prompt should not include explicit claim placeholder:\n%s", prompt)
	}
}

func TestPromptStructure(t *testing.T) {
	t.Run("planning prompt has required sections", func(t *testing.T) {
		prompt := GeneratePlanningPrompt("test", nil, "")
		sections := []string{
			"Step 1:",
			"Step 2:",
			"Step 3:",
			"Step 4:",
			"Step 5:",
			"Step 6:",
			"CRITICAL:",
		}
		for _, section := range sections {
			if !strings.Contains(prompt, section) {
				t.Errorf("planning prompt missing section: %q", section)
			}
		}
	})

	t.Run("task prompt has required sections", func(t *testing.T) {
		prompt := GenerateTaskPrompt("test", nil, "", "claude")
		sections := []string{
			"Step 1:",
			"Step 2:",
			"Step 3:",
			"Step 4:",
			"Step 5:",
			"Step 6:",
			"Step 7:",
			"Step 8:",
			"CRITICAL:",
		}
		for _, section := range sections {
			if !strings.Contains(prompt, section) {
				t.Errorf("task prompt missing section: %q", section)
			}
		}
	})
}

func TestGeneratePlanningPrompt_Workspace(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "frontend", Path: "frontend", DefaultBranch: "develop", Remote: "origin"},
			{Name: "backend", Path: "services/backend", DefaultBranch: "main", Remote: "origin"},
		},
	}

	prompt := GeneratePlanningPrompt("falcon", ws, "")

	// Verify workspace context is present
	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"frontend",
		"./frontend",
		"develop",
		"backend",
		"./services/backend",
		"Run `loom data` commands from the workspace root",
		"Run git commands (git status, git add, git commit, git push) from the specific repo subdirectory",
		// Standard planning steps must still be present
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"Step 4:",
		"Step 5:",
		"Step 6:",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("planning prompt with workspace missing expected part: %q", part)
		}
	}
}

func TestGenerateTaskPrompt_Workspace(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "api", Path: "api", DefaultBranch: "main", Remote: "origin"},
			{Name: "web", Path: "web", DefaultBranch: "main", Remote: "origin"},
		},
	}

	prompt := GenerateTaskPrompt("nova", ws, "", "claude")

	// Verify workspace context block
	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"api",
		"./api",
		"web",
		"./web",
		"Run `loom data` commands from the workspace root",
		"Run git commands (git status, git add, git commit, git push) from the specific repo subdirectory",
		"Run build/test commands from the specific repo subdirectory",
		"Changes may span multiple repos",
		// Standard task steps must still be present
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"Step 4:",
		"Step 5:",
		"Step 6:",
		"Step 7:",
		"Step 8:",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("task prompt with workspace missing expected part: %q", part)
		}
	}
}

func TestGenerateTaskPrompt_BackendAware(t *testing.T) {
	tests := []struct {
		name         string
		backendName  string
		wantParts    []string
		notWantParts []string
	}{
		{
			name:        "claude backend uses subagent instructions",
			backendName: "claude",
			wantParts:   []string{"Task tool", "subagent_type", "spawn agent"},
		},
		{
			name:         "codex backend uses generic instructions",
			backendName:  "codex",
			wantParts:    []string{"Write Tests", "Code Review", "Write unit tests", "Review your own changes"},
			notWantParts: []string{"Task tool", "subagent_type", "spawn agent"},
		},
		{
			name:         "opencode backend uses generic instructions",
			backendName:  "opencode",
			wantParts:    []string{"Write Tests", "Code Review"},
			notWantParts: []string{"Task tool", "subagent_type"},
		},
		{
			name:         "unknown future backend uses generic instructions",
			backendName:  "some-future-backend",
			notWantParts: []string{"Task tool", "subagent_type"},
		},
		{
			name:         "empty backend uses generic instructions",
			backendName:  "",
			notWantParts: []string{"Task tool", "subagent_type"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateTaskPrompt("test", nil, "", tc.backendName)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
			for _, part := range tc.notWantParts {
				if strings.Contains(prompt, part) {
					t.Errorf("prompt should not contain: %q", part)
				}
			}
		})
	}
}

func TestBuildSafetyGuardrailsBlock(t *testing.T) {
	block := buildSafetyGuardrailsBlock()

	if block == "" {
		t.Fatal("expected non-empty safety block")
	}

	wantPhrases := []string{
		"Multi-Agent Safety Rules",
		"Only modify files directly related",
		"git stash",
		"git checkout main",
		"git clean",
		"force-push",
		"reset --hard",
		"encounter files/changes from another agent",
		"Commit only your changes",
		"unexpected state",
		"Do not switch branches",
		"worktree branch",
	}

	for _, phrase := range wantPhrases {
		if !strings.Contains(block, phrase) {
			t.Errorf("safety block missing phrase: %q", phrase)
		}
	}
}

func TestAllPromptsContainSafetyRules(t *testing.T) {
	prompts := map[string]string{
		"planning":            GeneratePlanningPrompt("test", nil, ""),
		"task":                GenerateTaskPrompt("test", nil, "", "claude"),
		"fleet_planning":      GenerateFleetPlanningPrompt("test", "loom-test.1", nil),
		"fleet_task":          GenerateFleetTaskPrompt("test", "loom-test.1", nil, "claude"),
		"conflict_resolution": GenerateConflictResolutionPrompt("feature", "main", []string{"file.go"}),
		"lead":                GenerateLeadPrompt(),
	}

	for name, prompt := range prompts {
		if !strings.Contains(prompt, "Multi-Agent Safety Rules") {
			t.Errorf("%s prompt missing 'Multi-Agent Safety Rules' section", name)
		}
		if !strings.Contains(prompt, "Do not switch branches") {
			t.Errorf("%s prompt missing safety rule about branch switching", name)
		}
	}
}

func TestBuildWorkspaceContextBlock_NilWorkspace(t *testing.T) {
	result := buildWorkspaceContextBlock(nil)
	if result != "" {
		t.Errorf("expected empty string for nil workspace, got: %q", result)
	}
}

func TestBuildWorkspaceContextBlock_EmptyRepos(t *testing.T) {
	ws := &WorkspaceConfig{
		Path:  "/home/user/emptyws",
		Repos: []RepoConfig{},
	}

	result := buildWorkspaceContextBlock(ws)

	if !strings.Contains(result, "Workspace Mode: Multi-Repo Environment") {
		t.Error("expected workspace header even with no repos")
	}
	if !strings.Contains(result, "No repositories configured") {
		t.Error("expected 'No repositories configured' note for empty repos")
	}
}

func TestBuildWorkspaceContextBlock_DefaultBranch(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/ws",
		Repos: []RepoConfig{
			{Name: "myrepo", Path: "myrepo", DefaultBranch: "", Remote: "origin"},
		},
	}

	result := buildWorkspaceContextBlock(ws)

	// Empty DefaultBranch should default to "main" in the output
	if !strings.Contains(result, "main") {
		t.Error("expected default branch 'main' when DefaultBranch is empty")
	}
	// Verify the repo row contains "main"
	if !strings.Contains(result, "| myrepo | ./myrepo | main |") {
		t.Errorf("expected table row with default branch 'main', got:\n%s", result)
	}
}

func TestGenerateConflictResolutionPromptWithPush(t *testing.T) {
	tests := []struct {
		name         string
		sourceBranch string
		targetBranch string
		conflicts    []string
		pushRef      string
		wantParts    []string
		notWantParts []string
	}{
		{
			name:         "custom pushRef HEAD:main for detached mode",
			sourceBranch: "feature/auth",
			targetBranch: "main",
			conflicts:    []string{"pkg/auth.go", "pkg/handler.go"},
			pushRef:      "HEAD:main",
			wantParts: []string{
				"feature/auth",
				"main",
				"pkg/auth.go",
				"pkg/handler.go",
				"git push origin HEAD:main",
				"Resolve Merge Conflicts",
			},
		},
		{
			name:         "standard pushRef equals target branch",
			sourceBranch: "feature/ui",
			targetBranch: "develop",
			conflicts:    []string{"src/app.go"},
			pushRef:      "develop",
			wantParts: []string{
				"feature/ui",
				"develop",
				"src/app.go",
				"git push origin develop",
			},
		},
		{
			name:         "pushRef with refspec format",
			sourceBranch: "hotfix",
			targetBranch: "release",
			conflicts:    []string{"main.go"},
			pushRef:      "loom-push-temp-123:release",
			wantParts: []string{
				"git push origin loom-push-temp-123:release",
			},
		},
		{
			name:         "empty pushRef keeps local-only conflict resolution local",
			sourceBranch: "feature/local",
			targetBranch: "Slack_UI",
			conflicts:    []string{"src/data.js"},
			pushRef:      "",
			wantParts: []string{
				"No remote is configured for this repo",
				"Do not run git push origin",
				"git commit -m \"Resolve merge conflicts: feature/local -> Slack_UI",
			},
			notWantParts: []string{
				"\ngit push origin",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateConflictResolutionPromptWithPush(tc.sourceBranch, tc.targetBranch, tc.conflicts, tc.pushRef)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
			for _, part := range tc.notWantParts {
				if strings.Contains(prompt, part) {
					t.Errorf("prompt should not contain: %q", part)
				}
			}
		})
	}
}

func TestGenerateConflictResolutionPrompt_DelegatesToInternal(t *testing.T) {
	// Verify that GenerateConflictResolutionPrompt delegates to
	// GenerateConflictResolutionPromptWithPush with pushRef = targetBranch
	conflicts := []string{"file.go"}

	publicPrompt := GenerateConflictResolutionPrompt("feature", "main", conflicts)
	internalPrompt := GenerateConflictResolutionPromptWithPush("feature", "main", conflicts, "main")

	if publicPrompt != internalPrompt {
		t.Error("GenerateConflictResolutionPrompt should produce identical output to GenerateConflictResolutionPromptWithPush with pushRef=targetBranch")
	}

	// The public function should use targetBranch as the push ref
	if !strings.Contains(publicPrompt, "git push origin main") {
		t.Error("expected public prompt to use targetBranch as push ref")
	}
}

func TestGenerateTerminalPromptUsesBuiltInLeadWhenPromptFileEmpty(t *testing.T) {
	prompt, err := GenerateTerminalPrompt(t.Context(), "")
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt empty: %v", err)
	}
	if prompt != GenerateLeadPrompt() {
		t.Fatal("GenerateTerminalPrompt empty did not preserve built-in lead prompt")
	}
}

func TestGenerateTerminalPromptBuiltinPRReview(t *testing.T) {
	t.Setenv("LOOM_AGENT_NAME", "review-nova")
	t.Setenv("LOOM_AGENT_ROLE", "pr-review")

	prompt, err := GenerateTerminalPrompt(t.Context(), "builtin:pr-review")
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt builtin pr-review: %v", err)
	}
	for _, want := range []string{
		"PR-REVIEW-READY",
		"You are review-nova (role pr-review)",
		"gh pr diff",
		"ASK before posting",
		"Multi-Agent Safety Rules",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("builtin pr-review prompt missing %q", want)
		}
	}
	if got := strings.Count(prompt, "Multi-Agent Safety Rules"); got != 1 {
		t.Fatalf("safety block count = %d, want 1", got)
	}
}

func TestGenerateTerminalPromptBuiltinLeadMatchesLeadPrompt(t *testing.T) {
	prompt, err := GenerateTerminalPrompt(t.Context(), "builtin:lead")
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt builtin lead: %v", err)
	}
	if prompt != GenerateLeadPrompt() {
		t.Fatal("builtin:lead did not render the built-in lead prompt")
	}
	if got := strings.Count(prompt, "Multi-Agent Safety Rules"); got != 1 {
		t.Fatalf("safety block count = %d, want 1", got)
	}
}

func TestGenerateTerminalPromptBuiltinUnknownErrors(t *testing.T) {
	_, err := GenerateTerminalPrompt(t.Context(), "builtin:nope")
	if err == nil {
		t.Fatal("GenerateTerminalPrompt builtin:nope error = nil, want error")
	}
	if !strings.Contains(err.Error(), `unknown built-in interactive prompt "nope"`) {
		t.Fatalf("error = %q, want unknown built-in prompt", err.Error())
	}
}

func TestGenerateTerminalPromptCustomReplacesBaseAndAppendsSafety(t *testing.T) {
	t.Setenv("LOOM_AGENT_NAME", "nova")
	t.Setenv("LOOM_AGENT_ROLE", "operator")
	promptFile := filepath.Join(t.TempDir(), "terminal.md")
	if err := os.WriteFile(promptFile, []byte("Custom terminal for {{.AgentName}}/{{.WorktreeName}} as {{.Role}}"), 0644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	prompt, err := GenerateTerminalPrompt(t.Context(), promptFile)
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt custom: %v", err)
	}
	if !strings.HasPrefix(prompt, "Custom terminal for nova/nova as operator") {
		t.Fatalf("prompt = %q, want custom prompt as base", prompt)
	}
	if strings.Contains(prompt, "Lead Mode") {
		t.Fatalf("prompt contains built-in lead template, want custom prompt replacement")
	}
	if !strings.Contains(prompt, "Multi-Agent Safety Rules") {
		t.Fatalf("prompt missing appended safety guardrails")
	}
}

func TestGenerateTerminalPromptTextPreservesLiteralTextAndAppendsSafetyOnce(t *testing.T) {
	text := "Literal {{ .AgentName }} marker"
	prompt, err := GenerateTerminalPromptText(text)
	if err != nil {
		t.Fatalf("GenerateTerminalPromptText: %v", err)
	}
	if !strings.HasPrefix(prompt, text) {
		t.Fatalf("prompt = %q, want literal prefix %q", prompt, text)
	}
	if got := strings.Count(prompt, "Multi-Agent Safety Rules"); got != 1 {
		t.Fatalf("safety block count = %d, want 1", got)
	}
}

func TestGenerateTerminalPromptMissingFileErrors(t *testing.T) {
	_, err := GenerateTerminalPrompt(t.Context(), filepath.Join(t.TempDir(), "missing.md"))
	if err == nil {
		t.Fatal("GenerateTerminalPrompt missing file error = nil, want error")
	}
	if !strings.Contains(err.Error(), "reading prompt template") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want clear prompt file error", err.Error())
	}
}

func TestGenerateFleetPlanningPrompt(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		taskID    string
		wantParts []string
	}{
		{
			name:      "spark agent with task",
			agentName: "spark",
			taskID:    "loomcli-kv6.4",
			wantParts: []string{
				"Your agent name is: spark",
				"loomcli-kv6.4",
				"Planning Task",
				"Do NOT write any implementation code",
				"pre-assigned",
				"Fleet API",
				"loom claim loomcli-kv6.4",
				"loom data show loomcli-kv6.4",
				"already claimed",
				"--status review",
				"needs-revision",
			},
		},
		{
			name:      "nova agent with different task",
			agentName: "nova",
			taskID:    "proj-abc.1",
			wantParts: []string{
				"Your agent name is: nova",
				"proj-abc.1",
				"loom claim proj-abc.1",
				"loom data show proj-abc.1",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateFleetPlanningPrompt(tc.agentName, tc.taskID, nil)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
	}
}

func TestGenerateFleetTaskPrompt(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		taskID    string
		wantParts []string
	}{
		{
			name:      "spark agent with task",
			agentName: "spark",
			taskID:    "loomcli-kv6.4",
			wantParts: []string{
				"Your agent name is: spark",
				"loomcli-kv6.4",
				"Implementation Task",
				"pre-assigned",
				"Fleet API",
				"loom claim loomcli-kv6.4",
				"loom data show loomcli-kv6.4 --output json",
				"already claimed",
				"JSON `design`",
				"loom stack publish <stack-id>",
				"git branch -f <output-branch> HEAD",
			},
		},
		{
			name:      "ember agent with different task",
			agentName: "ember",
			taskID:    "proj-xyz.2",
			wantParts: []string{
				"Your agent name is: ember",
				"proj-xyz.2",
				"loom claim proj-xyz.2",
				"loom data show proj-xyz.2",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateFleetTaskPrompt(tc.agentName, tc.taskID, nil, "claude")

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
	}
}

func TestTaskPromptRequiresStackedPRDelivery(t *testing.T) {
	prompt := GenerateTaskPrompt("test", nil, "", "claude")
	wantParts := []string{
		"Publish through Loom stacked PR delivery (MANDATORY)",
		"loom stack init <stack-id>",
		"loom stack add <task-id>",
		"loom stack publish <stack-id>",
		"git branch -f <output-branch> HEAD",
		"Do not use direct integration or direct branch pushes as the completion path.",
	}
	notWantParts := []string{
		"loom push \"test\"",
		"git push origin HEAD",
		"Stage and commit: git add -A",
		"git add -A && git commit",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("task prompt missing expected stacked PR instruction: %q", part)
		}
	}
	for _, part := range notWantParts {
		if strings.Contains(prompt, part) {
			t.Errorf("task prompt should not contain direct publish instruction: %q", part)
		}
	}
}

func TestFleetTaskPromptFallsBackToLocalReviewWhenPRDeliveryIsUnavailable(t *testing.T) {
	prompt := GenerateFleetTaskPrompt("test", "loom-test.1", nil, "claude")
	wantParts := []string{
		"Prefer Loom stacked PR delivery",
		"loom stack init <stack-id>",
		"loom stack add <task-id>",
		"loom stack publish <stack-id>",
		"git branch -f <output-branch> HEAD",
		"supported local review handoff, NOT an external blocker",
		`--external-ref "local-branch:<output-branch>@${head_sha}"`,
		"Do NOT mark the task blocked",
		"Do NOT close it",
		"Do not attempt a direct push",
	}
	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("fleet task prompt missing delivery instruction: %q", part)
		}
	}
	for _, part := range []string{
		"git push origin HEAD",
		"Stage and commit: git add -A",
		"git add -A && git commit",
	} {
		if strings.Contains(prompt, part) {
			t.Errorf("fleet task prompt should not contain unsafe publish instruction: %q", part)
		}
	}
}

func TestGenerateFleetPlanningPrompt_Workspace(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "frontend", Path: "frontend", DefaultBranch: "develop", Remote: "origin"},
			{Name: "backend", Path: "services/backend", DefaultBranch: "main", Remote: "origin"},
		},
	}

	prompt := GenerateFleetPlanningPrompt("falcon", "loom-abc.1", ws)

	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"frontend",
		"./frontend",
		"develop",
		"backend",
		"./services/backend",
		"Run `loom data` commands from the workspace root",
		// Standard planning steps must still be present
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"Step 4:",
		"Step 5:",
		"Step 6:",
		// Fleet-specific
		"pre-assigned",
		"loom-abc.1",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("fleet planning prompt with workspace missing expected part: %q", part)
		}
	}
}

func TestGenerateFleetTaskPrompt_Workspace(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "api", Path: "api", DefaultBranch: "main", Remote: "origin"},
			{Name: "web", Path: "web", DefaultBranch: "main", Remote: "origin"},
		},
	}

	prompt := GenerateFleetTaskPrompt("nova", "loom-xyz.3", ws, "claude")

	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"api",
		"./api",
		"web",
		"./web",
		"Run `loom data` commands from the workspace root",
		"Run build/test commands from the specific repo subdirectory",
		"Changes may span multiple repos",
		// Standard task steps must still be present
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"Step 4:",
		"Step 5:",
		"Step 6:",
		"Step 7:",
		"Step 8:",
		// Fleet-specific
		"pre-assigned",
		"loom-xyz.3",
	}

	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("fleet task prompt with workspace missing expected part: %q", part)
		}
	}
}

func TestGenerateFleetTaskPrompt_BackendAware(t *testing.T) {
	tests := []struct {
		name         string
		backendName  string
		wantParts    []string
		notWantParts []string
	}{
		{
			name:        "claude backend uses subagent instructions",
			backendName: "claude",
			wantParts:   []string{"Task tool", "subagent_type", "spawn agent"},
		},
		{
			name:         "codex backend uses generic instructions",
			backendName:  "codex",
			wantParts:    []string{"Write Tests", "Code Review", "Write unit tests", "Review your own changes"},
			notWantParts: []string{"Task tool", "subagent_type", "spawn agent"},
		},
		{
			name:         "opencode backend uses generic instructions",
			backendName:  "opencode",
			wantParts:    []string{"Write Tests", "Code Review"},
			notWantParts: []string{"Task tool", "subagent_type"},
		},
		{
			name:         "unknown future backend uses generic instructions",
			backendName:  "some-future-backend",
			notWantParts: []string{"Task tool", "subagent_type"},
		},
		{
			name:         "empty backend uses generic instructions",
			backendName:  "",
			notWantParts: []string{"Task tool", "subagent_type"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateFleetTaskPrompt("test", "loom-test.1", nil, tc.backendName)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
			for _, part := range tc.notWantParts {
				if strings.Contains(prompt, part) {
					t.Errorf("prompt should not contain: %q", part)
				}
			}
		})
	}
}

func TestFleetPromptStructure(t *testing.T) {
	t.Run("fleet planning prompt has required sections", func(t *testing.T) {
		prompt := GenerateFleetPlanningPrompt("test", "loom-test.1", nil)
		sections := []string{
			"Step 1:",
			"Step 1.5:",
			"Step 2:",
			"Step 3:",
			"Step 4:",
			"Step 5:",
			"Step 6:",
			"CRITICAL:",
		}
		for _, section := range sections {
			if !strings.Contains(prompt, section) {
				t.Errorf("fleet planning prompt missing section: %q", section)
			}
		}
	})

	t.Run("fleet task prompt has required sections", func(t *testing.T) {
		prompt := GenerateFleetTaskPrompt("test", "loom-test.1", nil, "claude")
		sections := []string{
			"Step 1:",
			"Step 2:",
			"Step 3:",
			"Step 4:",
			"Step 5:",
			"Step 6:",
			"Step 7:",
			"Step 8:",
			"Step 9:",
			"CRITICAL:",
		}
		for _, section := range sections {
			if !strings.Contains(prompt, section) {
				t.Errorf("fleet task prompt missing section: %q", section)
			}
		}
	})
}

func TestFleetPromptsNoClaimCommand(t *testing.T) {
	planPrompt := GenerateFleetPlanningPrompt("test", "loom-test.1", nil)
	taskPrompt := GenerateFleetTaskPrompt("test", "loom-test.1", nil, "claude")

	notWantParts := []string{
		"loom data claim <id>",
		"loom data ready --json",
	}

	for _, part := range notWantParts {
		if strings.Contains(planPrompt, part) {
			t.Errorf("fleet planning prompt should NOT contain: %q", part)
		}
		if strings.Contains(taskPrompt, part) {
			t.Errorf("fleet task prompt should NOT contain: %q", part)
		}
	}
}

func TestGenerateFleetPlanningPrompt_TaskIDSubstitution(t *testing.T) {
	taskID := "UNIQUE-TASK-XYZ-12345"
	prompt := GenerateFleetPlanningPrompt("agent", taskID, nil)
	count := strings.Count(prompt, taskID)
	if count < 2 {
		t.Errorf("expected taskID to appear at least 2 times, got %d", count)
	}
	if strings.Contains(prompt, "%!") {
		t.Error("prompt contains unsubstituted format directives")
	}
}

func TestGenerateFleetTaskPrompt_TaskIDSubstitution(t *testing.T) {
	taskID := "UNIQUE-TASK-XYZ-12345"
	prompt := GenerateFleetTaskPrompt("agent", taskID, nil, "")
	count := strings.Count(prompt, taskID)
	if count < 2 {
		t.Errorf("expected taskID to appear at least 2 times, got %d", count)
	}
	if strings.Contains(prompt, "%!") {
		t.Error("prompt contains unsubstituted format directives")
	}
}

func TestBuildWorkspaceContextBlock_RepoWithEmptyName(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/ws",
		Repos: []RepoConfig{
			{Name: "", Path: "somepath", DefaultBranch: "main", Remote: "origin"},
		},
	}
	result := buildWorkspaceContextBlock(ws)
	if !strings.Contains(result, "|  | ./somepath | main |") {
		t.Errorf("expected table row with empty name, got:\n%s", result)
	}
}

func TestGenerateConflictResolutionPrompt_EmptyConflicts(t *testing.T) {
	prompt := GenerateConflictResolutionPrompt("feature", "main", []string{})
	wantParts := []string{
		"Resolve Merge Conflicts",
		"Step 1",
		"Step 2",
		"Step 3",
		"Step 4",
		"Step 5",
	}
	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt missing expected part: %q", part)
		}
	}
}

func TestGenerateConflictResolutionPrompt_EmptyBranchNames(t *testing.T) {
	prompt := GenerateConflictResolutionPrompt("", "", []string{"file.go"})
	wantParts := []string{
		"Resolve Merge Conflicts",
		"file.go",
		"Step 1",
	}
	for _, part := range wantParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt missing expected part: %q", part)
		}
	}
}

func TestRenderPrompt_EmbeddedTemplate(t *testing.T) {
	// Verify each embedded template renders without errors
	templates := []struct {
		name string
		data promptTemplateData
	}{
		{"planning", promptTemplateData{AgentName: "test", ReadyJSON: "loom data ready --limit 200 --output json", ReadyFallback: "loom data ready --limit 200"}},
		{"task", promptTemplateData{AgentName: "test", ReadyJSON: "loom data ready --limit 200 --output json", ReadyFallback: "loom data ready --limit 200", TestStep: "test step", ReviewStep: "review step"}},
		{"fleet_planning", promptTemplateData{AgentName: "test", TaskID: "loom-test.1"}},
		{"fleet_task", promptTemplateData{AgentName: "test", TaskID: "loom-test.1", TestStep: "test step", ReviewStep: "review step"}},
		{"conflict_resolution", promptTemplateData{SourceBranch: "feature", TargetBranch: "main", ConflictList: "file.go", PushRef: "main"}},
		{"lead", promptTemplateData{}},
	}

	for _, tc := range templates {
		t.Run(tc.name, func(t *testing.T) {
			result := renderPrompt(tc.name, tc.data)
			if result == "" {
				t.Errorf("renderPrompt(%q) returned empty string", tc.name)
			}
		})
	}
}

func TestRenderPrompt_ProjectOverride(t *testing.T) {
	// Create temp override directory
	overrideDir := filepath.Join(t.TempDir(), "project")
	promptDir := filepath.Join(overrideDir, "loom-prompts")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}

	overrideContent := "Custom override: {{ .AgentName }} is here"
	if err := os.WriteFile(filepath.Join(promptDir, "planning.md"), []byte(overrideContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Point override dir to the temp project directory
	promptOverrideDir = overrideDir
	t.Cleanup(func() { promptOverrideDir = "" })

	result := renderPrompt("planning", promptTemplateData{AgentName: "falcon"})
	if !strings.Contains(result, "Custom override: falcon is here") {
		t.Errorf("expected override content, got: %s", result)
	}
}

func TestRenderPrompt_InvalidOverrideFallback(t *testing.T) {
	// Create temp override directory with invalid template
	overrideDir := filepath.Join(t.TempDir(), "project")
	promptDir := filepath.Join(overrideDir, "loom-prompts")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Invalid template syntax
	if err := os.WriteFile(filepath.Join(promptDir, "lead.md"), []byte("{{ .Invalid {{ broken"), 0644); err != nil {
		t.Fatal(err)
	}

	promptOverrideDir = overrideDir
	t.Cleanup(func() { promptOverrideDir = "" })

	// Should fall back to embedded default
	result := renderPrompt("lead", promptTemplateData{})
	if !strings.Contains(result, "INTERACTIVE MODE: Project Lead") {
		t.Errorf("expected fallback to embedded template, got: %s", result[:100])
	}
}

func TestRenderPrompt_OverrideFallbackOnBadExecution(t *testing.T) {
	overrideDir := filepath.Join(t.TempDir(), "project")
	promptDir := filepath.Join(overrideDir, "loom-prompts")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Template that parses but fails Execute: calls a method on a string field
	badTemplate := "Hello {{call .SafetyBlock}}"
	if err := os.WriteFile(filepath.Join(promptDir, "lead.md"), []byte(badTemplate), 0644); err != nil {
		t.Fatal(err)
	}

	promptOverrideDir = overrideDir
	t.Cleanup(func() { promptOverrideDir = "" })

	// Should NOT panic — should fall back to embedded default
	result := renderPrompt("lead", promptTemplateData{SafetyBlock: "test"})
	if !strings.Contains(result, "INTERACTIVE MODE: Project Lead") {
		t.Errorf("expected fallback to embedded template, got: %s", result[:min(100, len(result))])
	}
}

func TestRenderPrompt_OverrideNotFound(t *testing.T) {
	// When no override exists, embedded template is used
	result := renderPrompt("lead", promptTemplateData{})
	if !strings.Contains(result, "INTERACTIVE MODE: Project Lead") {
		t.Error("expected embedded template content when no override exists")
	}
}

func TestAllTemplatesRender(t *testing.T) {
	// Verify all 6 templates parse and render with fully-populated data
	data := promptTemplateData{
		AgentName:      "testAgent",
		WorkspaceBlock: "workspace block content",
		EpicScope:      "epic scope content",
		SafetyBlock:    "safety block content",
		ReadyJSON:      "loom data ready --parent epic-123 --limit 200 --output json",
		ReadyFallback:  "loom data ready --parent epic-123 --limit 200",
		TaskID:         "task-456",
		TestStep:       "### Step 5: Write Tests\n- test content",
		ReviewStep:     "### Step 6: Code Review\n- review content",
		SourceBranch:   "feature/test",
		TargetBranch:   "main",
		ConflictList:   "file1.go\nfile2.go",
		PushRef:        "HEAD:main",
	}

	templates := []string{"planning", "task", "fleet_planning", "fleet_task", "conflict_resolution", "lead"}
	for _, name := range templates {
		t.Run(name, func(t *testing.T) {
			result := renderPrompt(name, data)
			if result == "" {
				t.Errorf("template %q rendered empty", name)
			}
			if len(result) < 100 {
				t.Errorf("template %q rendered suspiciously short (%d chars)", name, len(result))
			}
		})
	}
}

// TestReadOnlyPreamble verifies the function returns preamble when env is set
// and empty string when not set.
func TestReadOnlyPreamble(t *testing.T) {
	t.Run("returns preamble when LOOM_READ_ONLY=1", func(t *testing.T) {
		t.Setenv("LOOM_READ_ONLY", "1")
		result := ReadOnlyPreamble()
		if result == "" {
			t.Error("ReadOnlyPreamble() = empty, want non-empty")
		}
		if !strings.Contains(result, "READ-ONLY") {
			t.Errorf("ReadOnlyPreamble() = %q, want contains 'READ-ONLY'", result)
		}
		if !strings.Contains(result, "save requested designs") {
			t.Errorf("ReadOnlyPreamble() = %q, want task-design write authorization", result)
		}
	})

	t.Run("returns empty when LOOM_READ_ONLY not set", func(t *testing.T) {
		t.Setenv("LOOM_READ_ONLY", "")
		result := ReadOnlyPreamble()
		if result != "" {
			t.Errorf("ReadOnlyPreamble() = %q, want empty", result)
		}
	})

	t.Run("returns empty when LOOM_READ_ONLY=0", func(t *testing.T) {
		t.Setenv("LOOM_READ_ONLY", "0")
		result := ReadOnlyPreamble()
		if result != "" {
			t.Errorf("ReadOnlyPreamble() = %q, want empty (only '1' triggers)", result)
		}
	})
}

func TestResolveActiveWorkspace_NoConfig(t *testing.T) {
	testutil.ClearLoomEnv(t)

	// Create a temp empty directory and point LOOM_CONFIG_DIR to it
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "loomcfg")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create temp config dir: %v", err)
	}

	// Save and restore original env var
	origVal, origSet := os.LookupEnv("LOOM_CONFIG_DIR")
	t.Cleanup(func() {
		if origSet {
			os.Setenv("LOOM_CONFIG_DIR", origVal)
		} else {
			os.Unsetenv("LOOM_CONFIG_DIR")
		}
	})

	os.Setenv("LOOM_CONFIG_DIR", configDir)

	ws, err := ResolveActiveWorkspace(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if ws != nil {
		t.Errorf("expected nil workspace when config dir is empty, got: %+v", ws)
	}
}

func TestResolveDesignFormat(t *testing.T) {
	tests := []struct {
		name      string
		workspace *WorkspaceConfig
		want      string
	}{
		{name: "nil workspace", workspace: nil, want: "markdown"},
		{name: "empty design format", workspace: &WorkspaceConfig{}, want: "markdown"},
		{name: "explicit markdown", workspace: &WorkspaceConfig{DesignFormat: "markdown"}, want: "markdown"},
		{name: "html", workspace: &WorkspaceConfig{DesignFormat: "html"}, want: "html"},
		{name: "garbage value falls back to markdown", workspace: &WorkspaceConfig{DesignFormat: "yaml"}, want: "markdown"},
		{name: "case-sensitive: HTML is not html", workspace: &WorkspaceConfig{DesignFormat: "HTML"}, want: "markdown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDesignFormat(tc.workspace); got != tc.want {
				t.Errorf("resolveDesignFormat() = %q, want %q", got, tc.want)
			}
		})
	}
}

const designFormatHTMLGuidance = "Design format: HTML"

func TestGeneratePlanningPrompt_DesignFormat(t *testing.T) {
	tests := []struct {
		name      string
		workspace *WorkspaceConfig
		wantHTML  bool
	}{
		{name: "nil workspace", workspace: nil, wantHTML: false},
		{name: "empty design format", workspace: &WorkspaceConfig{}, wantHTML: false},
		{name: "markdown", workspace: &WorkspaceConfig{DesignFormat: "markdown"}, wantHTML: false},
		{name: "garbage value", workspace: &WorkspaceConfig{DesignFormat: "garbage"}, wantHTML: false},
		{name: "html", workspace: &WorkspaceConfig{DesignFormat: "html"}, wantHTML: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GeneratePlanningPrompt("falcon", tc.workspace, "")
			gotHTML := strings.Contains(prompt, designFormatHTMLGuidance)
			if gotHTML != tc.wantHTML {
				t.Errorf("planning prompt contains %q = %v, want %v", designFormatHTMLGuidance, gotHTML, tc.wantHTML)
			}
			// The standard plan-content instruction must remain regardless of format.
			if !strings.Contains(prompt, "Write a comprehensive plan that includes:") {
				t.Error("planning prompt missing comprehensive plan instruction")
			}
		})
	}
}

func TestGenerateFleetPlanningPrompt_DesignFormat(t *testing.T) {
	tests := []struct {
		name      string
		workspace *WorkspaceConfig
		wantHTML  bool
	}{
		{name: "nil workspace", workspace: nil, wantHTML: false},
		{name: "empty design format", workspace: &WorkspaceConfig{}, wantHTML: false},
		{name: "markdown", workspace: &WorkspaceConfig{DesignFormat: "markdown"}, wantHTML: false},
		{name: "garbage value", workspace: &WorkspaceConfig{DesignFormat: "garbage"}, wantHTML: false},
		{name: "html", workspace: &WorkspaceConfig{DesignFormat: "html"}, wantHTML: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateFleetPlanningPrompt("falcon", "loom-test.1", tc.workspace)
			gotHTML := strings.Contains(prompt, designFormatHTMLGuidance)
			if gotHTML != tc.wantHTML {
				t.Errorf("fleet planning prompt contains %q = %v, want %v", designFormatHTMLGuidance, gotHTML, tc.wantHTML)
			}
			if !strings.Contains(prompt, "Write a comprehensive plan that includes:") {
				t.Error("fleet planning prompt missing comprehensive plan instruction")
			}
		})
	}
}

func TestCapabilitiesFor(t *testing.T) {
	tests := []struct {
		name        string
		backendName string
		want        backendCapabilities
	}{
		{
			name:        "claude has all capabilities",
			backendName: "claude",
			want: backendCapabilities{
				supportsSubagentSpawn: true,
				supportsInspectReview: true,
			},
		},
		{
			name:        "codex has no special capabilities",
			backendName: "codex",
			want:        backendCapabilities{},
		},
		{
			name:        "opencode has no special capabilities",
			backendName: "opencode",
			want:        backendCapabilities{},
		},
		{
			name:        "unknown backend has no special capabilities",
			backendName: "some-future-backend",
			want:        backendCapabilities{},
		},
		{
			name:        "empty backend name has no special capabilities",
			backendName: "",
			want:        backendCapabilities{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := capabilitiesFor(tc.backendName); got != tc.want {
				t.Errorf("capabilitiesFor(%q) = %+v, want %+v", tc.backendName, got, tc.want)
			}
		})
	}
}

func TestStepBuilders_CapabilityDriven(t *testing.T) {
	spawn := backendCapabilities{supportsSubagentSpawn: true, supportsInspectReview: true}
	none := backendCapabilities{}

	if got := buildTestStep(spawn); !strings.Contains(got, "spawn agent") {
		t.Errorf("buildTestStep with subagent spawn should mention spawning an agent, got: %q", got)
	}
	if got := buildTestStep(none); strings.Contains(got, "spawn") || strings.Contains(got, "Task tool") {
		t.Errorf("buildTestStep without subagent spawn should not mention spawn/Task tool, got: %q", got)
	}

	if got := buildReviewStep(spawn); !strings.Contains(got, "Task tool") {
		t.Errorf("buildReviewStep with subagent spawn should mention the Task tool, got: %q", got)
	}
	if got := buildReviewStep(none); strings.Contains(got, "spawn") || strings.Contains(got, "Task tool") {
		t.Errorf("buildReviewStep without subagent spawn should not mention spawn/Task tool, got: %q", got)
	}

	if got := buildInspectReviewStep(spawn); !strings.Contains(got, "inspect-reviewer") {
		t.Errorf("buildInspectReviewStep with inspect review should mention inspect-reviewer, got: %q", got)
	}
	if got := buildInspectReviewStep(none); got != "" {
		t.Errorf("buildInspectReviewStep without inspect review should be empty, got: %q", got)
	}
}

func TestGenerateTerminalPromptBuiltinPRReviewCheckout(t *testing.T) {
	got, err := GenerateTerminalPrompt(t.Context(), "builtin:pr-review-checkout")
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt builtin pr-review-checkout: %v", err)
	}
	if !strings.Contains(got, "READ-ONLY") {
		t.Fatalf("checkout review prompt missing read-only persona:\n%s", got)
	}
	// The whole point: no lead/backlog bootstrap that triggers startup commands.
	for _, forbidden := range []string{"INTERACTIVE MODE: Project Lead", "loom data list", "On Startup"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("checkout review prompt leaks lead bootstrap %q:\n%s", forbidden, got)
		}
	}
	// Self-describing checkout: the prompt diffs via the recorded base config, so
	// no PR-specific data is injected into the prompt itself.
	if !strings.Contains(got, "loom.reviewBase") {
		t.Fatalf("checkout review prompt must reference the recorded base (loom.reviewBase):\n%s", got)
	}
	// It must preserve the reviewed repo's AGENTS.md injection boundary.
	if !strings.Contains(got, "AGENTS.md") {
		t.Fatalf("checkout review prompt must mention AGENTS.md/onboarding:\n%s", got)
	}
	if lead := GenerateLeadPrompt(); got == lead {
		t.Fatal("checkout review prompt must differ from the lead prompt")
	}
}
