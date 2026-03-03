package cli

import (
	"bytes"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed prompts/*.md
var promptFS embed.FS

// promptTemplateData holds all template context fields for prompt rendering.
type promptTemplateData struct {
	AgentName       string
	WorkspaceBlock  string
	EpicScope       string
	BdReadyJSON     string
	BdReadyFallback string
	TaskID          string
	TestStep        string
	ReviewStep      string
	SourceBranch    string
	TargetBranch    string
	ConflictList    string
	PushRef         string
}

// renderPrompt loads a template by name, checks for per-project override,
// and renders it with the given data.
func renderPrompt(name string, data promptTemplateData) string {
	tmplContent, err := loadTemplate(name)
	if err != nil {
		panic(fmt.Sprintf("prompt: failed to load template %q: %v", name, err))
	}

	tmpl, err := template.New(name).Parse(tmplContent)
	if err != nil {
		panic(fmt.Sprintf("prompt: failed to parse template %q: %v", name, err))
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("prompt: failed to execute template %q: %v", name, err))
	}

	return buf.String()
}

// promptOverrideDir overrides the base directory for per-project prompt overrides.
// Empty string means use the current working directory. Set in tests to avoid os.Chdir.
var promptOverrideDir string

// loadTemplate checks for a per-project override at ./loom-prompts/<name>.md,
// then falls back to the embedded default.
func loadTemplate(name string) (string, error) {
	base := promptOverrideDir
	if base == "" {
		base = "."
	}
	overridePath := filepath.Join(base, "loom-prompts", name+".md")
	if content, err := os.ReadFile(overridePath); err == nil { //nolint:gosec // G304: intentional per-project override loading
		// Validate the override parses as a template
		if _, parseErr := template.New(name).Parse(string(content)); parseErr != nil {
			log.Printf("warning: invalid template override %s: %v, using embedded default", overridePath, parseErr)
		} else {
			return string(content), nil
		}
	}

	content, err := promptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("embedded template %q not found: %w", name, err)
	}
	return string(content), nil
}

// buildWorkspaceContextBlock generates the workspace context section for prompts.
// Returns empty string if workspace is nil.
func buildWorkspaceContextBlock(workspace *WorkspaceConfig) string {
	if workspace == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n### Workspace Mode: Multi-Repo Environment\n")
	sb.WriteString("You are working in a multi-repo workspace. The workspace root is your current working directory.\n")
	sb.WriteString("Repositories are subdirectories within this workspace:\n\n")

	if len(workspace.Repos) == 0 {
		sb.WriteString("_No repositories configured in this workspace._\n")
	} else {
		sb.WriteString("| Repo | Path | Default Branch |\n")
		sb.WriteString("|------|------|----------------|\n")
		for _, repo := range workspace.Repos {
			branch := repo.DefaultBranch
			if branch == "" {
				branch = "main"
			}
			sb.WriteString(fmt.Sprintf("| %s | ./%s | %s |\n", repo.Name, repo.Path, branch))
		}
	}

	sb.WriteString("\n**Important workspace rules:**\n")
	sb.WriteString("- Run `bd` commands from the workspace root (current directory)\n")
	sb.WriteString("- Run git commands (git status, git add, git commit, git push) from the specific repo subdirectory\n")
	sb.WriteString("- Run build/test commands from the specific repo subdirectory\n")
	sb.WriteString("- Changes may span multiple repos — coordinate commits across them\n\n")

	return sb.String()
}

// buildTestStep returns the backend-aware test step content.
func buildTestStep(backendName string) string {
	if backendName == "claude" {
		return `### Step 5: Write Tests (spawn agent)
- Use the Task tool to spawn an agent to write tests
- Prompt: 'Write unit tests for the changes made in [files]. Follow existing test patterns in the codebase.'
- Verify tests pass by running the test command (e.g., 'go test ./...' or 'npm test')
- If tests fail, fix the code or tests until they pass`
	}
	return `### Step 5: Write Tests
- Write unit tests for your changes, following existing test patterns in the codebase
- Verify tests pass by running the test command (e.g., 'go test ./...' or 'npm test')
- If tests fail, fix the code or tests until they pass`
}

// buildReviewStep returns the backend-aware review step content.
func buildReviewStep(backendName string) string {
	if backendName == "claude" {
		return `### Step 6: Code Review (spawn agent)
- Use the Task tool with subagent_type='feature-dev:code-reviewer'
- Prompt: 'Review the changes for this task. Check for bugs, security issues, code quality, and adherence to project conventions.'
- Document all issues found`
	}
	return `### Step 6: Code Review
- Review your own changes for bugs, security issues, code quality, and adherence to project conventions
- Check for common issues: error handling, edge cases, naming consistency
- Document and fix all issues found`
}

// GeneratePlanningPrompt creates the prompt for the planning agent.
// If workspace is non-nil, workspace context is injected into the prompt.
// If parentID is non-empty, the prompt scopes task discovery to that epic.
// SYNC: The jq filters below must match taskfilter.go NeedsPlan() criteria:
//
//	planning: design empty OR has "needs-revision" label
func GeneratePlanningPrompt(agentName string, workspace *WorkspaceConfig, parentID string) string {
	bdReadyJSON := "bd ready --limit 50 --json"
	bdReadyFallback := "bd ready --limit 50"
	epicScope := ""
	if parentID != "" {
		bdReadyJSON = fmt.Sprintf("bd ready --parent %s --limit 50 --json", parentID)
		bdReadyFallback = fmt.Sprintf("bd ready --parent %s --limit 50", parentID)
		epicScope = fmt.Sprintf("\n**Epic scope: %s** — You MUST only select tasks from this epic. Do not work on tasks from other epics.\n", parentID)
	}

	return renderPrompt("planning", promptTemplateData{
		AgentName:       agentName,
		WorkspaceBlock:  buildWorkspaceContextBlock(workspace),
		EpicScope:       epicScope,
		BdReadyJSON:     bdReadyJSON,
		BdReadyFallback: bdReadyFallback,
	})
}

// GenerateTaskPrompt creates the prompt for the implementation agent.
// If workspace is non-nil, workspace context is injected into the prompt.
// If parentID is non-empty, the prompt scopes task discovery to that epic.
// SYNC: The jq filters below must match taskfilter.go ReadyToImplement() criteria:
//
//	implementation: design non-empty AND no "needs-revision" label
func GenerateTaskPrompt(agentName string, workspace *WorkspaceConfig, parentID string, backendName string) string {
	bdReadyJSON := "bd ready --limit 50 --json"
	bdReadyFallback := "bd ready --limit 50"
	epicScope := ""
	if parentID != "" {
		bdReadyJSON = fmt.Sprintf("bd ready --parent %s --limit 50 --json", parentID)
		bdReadyFallback = fmt.Sprintf("bd ready --parent %s --limit 50", parentID)
		epicScope = fmt.Sprintf("\n**Epic scope: %s** — You MUST only select tasks from this epic. Do not work on tasks from other epics.\n", parentID)
	}

	return renderPrompt("task", promptTemplateData{
		AgentName:       agentName,
		WorkspaceBlock:  buildWorkspaceContextBlock(workspace),
		EpicScope:       epicScope,
		BdReadyJSON:     bdReadyJSON,
		BdReadyFallback: bdReadyFallback,
		TestStep:        buildTestStep(backendName),
		ReviewStep:      buildReviewStep(backendName),
	})
}

// GenerateFleetPlanningPrompt creates the prompt for a fleet planning agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetPlanningPrompt(agentName, taskID string, workspace *WorkspaceConfig) string {
	return renderPrompt("fleet_planning", promptTemplateData{
		AgentName:      agentName,
		WorkspaceBlock: buildWorkspaceContextBlock(workspace),
		TaskID:         taskID,
	})
}

// GenerateFleetTaskPrompt creates the prompt for a fleet implementation agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetTaskPrompt(agentName, taskID string, workspace *WorkspaceConfig, backendName string) string {
	return renderPrompt("fleet_task", promptTemplateData{
		AgentName:      agentName,
		WorkspaceBlock: buildWorkspaceContextBlock(workspace),
		TaskID:         taskID,
		TestStep:       buildTestStep(backendName),
		ReviewStep:     buildReviewStep(backendName),
	})
}

// GenerateConflictResolutionPrompt creates the prompt for merge conflict resolution
func GenerateConflictResolutionPrompt(sourceBranch, targetBranch string, conflicts []string) string {
	return generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, targetBranch)
}

// generateConflictResolutionPromptWithPush creates a conflict resolution prompt with a custom push ref.
// pushRef is used in the "git push origin <pushRef>" command (e.g., "main" or "HEAD:main").
func generateConflictResolutionPromptWithPush(sourceBranch, targetBranch string, conflicts []string, pushRef string) string {
	return renderPrompt("conflict_resolution", promptTemplateData{
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		ConflictList: strings.Join(conflicts, "\n"),
		PushRef:      pushRef,
	})
}

// GenerateLeadPrompt creates the prompt for the interactive lead/manager mode
func GenerateLeadPrompt() string {
	return renderPrompt("lead", promptTemplateData{})
}
