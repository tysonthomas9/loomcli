package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
				"git push origin HEAD",
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

	// Check for beads commands
	beadsCommands := []string{
		"bd stats",
		"bd list",
		"bd show",
		"bd create",
		"bd update",
		"bd close",
		"bd sync",
		"bd blocked",
	}

	for _, cmd := range beadsCommands {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt missing beads command: %q", cmd)
		}
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
		"Run `bd` commands from the workspace root",
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
		"Run `bd` commands from the workspace root",
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
		"fleet_planning":      GenerateFleetPlanningPrompt("test", "bd-test.1", nil),
		"fleet_task":          GenerateFleetTaskPrompt("test", "bd-test.1", nil, "claude"),
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
				"loom data show loomcli-kv6.4",
				"already claimed",
				"--design",
				"git push origin HEAD",
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

func TestGenerateFleetPlanningPrompt_Workspace(t *testing.T) {
	ws := &WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "frontend", Path: "frontend", DefaultBranch: "develop", Remote: "origin"},
			{Name: "backend", Path: "services/backend", DefaultBranch: "main", Remote: "origin"},
		},
	}

	prompt := GenerateFleetPlanningPrompt("falcon", "bd-abc.1", ws)

	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"frontend",
		"./frontend",
		"develop",
		"backend",
		"./services/backend",
		"Run `bd` commands from the workspace root",
		// Standard planning steps must still be present
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"Step 4:",
		"Step 5:",
		"Step 6:",
		// Fleet-specific
		"pre-assigned",
		"bd-abc.1",
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

	prompt := GenerateFleetTaskPrompt("nova", "bd-xyz.3", ws, "claude")

	wantParts := []string{
		"Workspace Mode: Multi-Repo Environment",
		"api",
		"./api",
		"web",
		"./web",
		"Run `bd` commands from the workspace root",
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
		"bd-xyz.3",
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
			prompt := GenerateFleetTaskPrompt("test", "bd-test.1", nil, tc.backendName)

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
		prompt := GenerateFleetPlanningPrompt("test", "bd-test.1", nil)
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
		prompt := GenerateFleetTaskPrompt("test", "bd-test.1", nil, "claude")
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
	planPrompt := GenerateFleetPlanningPrompt("test", "bd-test.1", nil)
	taskPrompt := GenerateFleetTaskPrompt("test", "bd-test.1", nil, "claude")

	notWantParts := []string{
		"bd update <id> --claim",
		"bd ready --json",
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
		{"planning", promptTemplateData{AgentName: "test", BdReadyJSON: "bd ready --limit 200 --json", BdReadyFallback: "bd ready --limit 200"}},
		{"task", promptTemplateData{AgentName: "test", BdReadyJSON: "bd ready --limit 200 --json", BdReadyFallback: "bd ready --limit 200", TestStep: "test step", ReviewStep: "review step"}},
		{"fleet_planning", promptTemplateData{AgentName: "test", TaskID: "bd-test.1"}},
		{"fleet_task", promptTemplateData{AgentName: "test", TaskID: "bd-test.1", TestStep: "test step", ReviewStep: "review step"}},
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
		AgentName:       "testAgent",
		WorkspaceBlock:  "workspace block content",
		EpicScope:       "epic scope content",
		SafetyBlock:     "safety block content",
		BdReadyJSON:     "bd ready --parent epic-123 --limit 200 --json",
		BdReadyFallback: "bd ready --parent epic-123 --limit 200",
		TaskID:          "task-456",
		TestStep:        "### Step 5: Write Tests\n- test content",
		ReviewStep:      "### Step 6: Code Review\n- review content",
		SourceBranch:    "feature/test",
		TargetBranch:    "main",
		ConflictList:    "file1.go\nfile2.go",
		PushRef:         "HEAD:main",
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

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if ws != nil {
		t.Errorf("expected nil workspace when config dir is empty, got: %+v", ws)
	}
}
