package main

import (
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
				"--assignee falcon",
				"Planning Task",
				"Do NOT write any implementation code",
				"[Need Review]",
			},
		},
		{
			name:      "nova agent",
			agentName: "nova",
			wantParts: []string{
				"Your agent name is: nova",
				"--assignee nova",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GeneratePlanningPrompt(tc.agentName)

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
				"--assignee ember",
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
				"--assignee zephyr",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := GenerateTaskPrompt(tc.agentName)

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

func TestPromptStructure(t *testing.T) {
	t.Run("planning prompt has required sections", func(t *testing.T) {
		prompt := GeneratePlanningPrompt("test")
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
		prompt := GenerateTaskPrompt("test")
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
