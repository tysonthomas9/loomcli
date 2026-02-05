package cli

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
				"--claim",
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
				"--claim",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GeneratePlanningPrompt(tc.agentName, nil)

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
				"--claim",                 // Main task claiming uses atomic --claim
				"--assignee ember",        // Reclaiming stale tasks still uses --assignee
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
				"--claim",
				"--assignee zephyr",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateTaskPrompt(tc.agentName, nil)

			for _, part := range tc.wantParts {
				if !strings.Contains(prompt, part) {
					t.Errorf("prompt missing expected part: %q", part)
				}
			}
		})
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
		prompt := GeneratePlanningPrompt("test", nil)
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
		prompt := GenerateTaskPrompt("test", nil)
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

	prompt := GeneratePlanningPrompt("falcon", ws)

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

	prompt := GenerateTaskPrompt("nova", ws)

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
			prompt := generateConflictResolutionPromptWithPush(tc.sourceBranch, tc.targetBranch, tc.conflicts, tc.pushRef)

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
	// generateConflictResolutionPromptWithPush with pushRef = targetBranch
	conflicts := []string{"file.go"}

	publicPrompt := GenerateConflictResolutionPrompt("feature", "main", conflicts)
	internalPrompt := generateConflictResolutionPromptWithPush("feature", "main", conflicts, "main")

	if publicPrompt != internalPrompt {
		t.Error("GenerateConflictResolutionPrompt should produce identical output to generateConflictResolutionPromptWithPush with pushRef=targetBranch")
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
				"bd show loomcli-kv6.4",
				"status in_progress",
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
				"bd show proj-abc.1",
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
				"bd show loomcli-kv6.4",
				"status in_progress",
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
				"bd show proj-xyz.2",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateFleetTaskPrompt(tc.agentName, tc.taskID, nil)

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

	prompt := GenerateFleetTaskPrompt("nova", "bd-xyz.3", ws)

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
		prompt := GenerateFleetTaskPrompt("test", "bd-test.1", nil)
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
	taskPrompt := GenerateFleetTaskPrompt("test", "bd-test.1", nil)

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
