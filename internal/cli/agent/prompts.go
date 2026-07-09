package agent

import (
	"bytes"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

//go:embed prompts/*.md
var promptFS embed.FS

// promptTemplateData holds all template context fields for prompt rendering.
type promptTemplateData struct {
	AgentName         string
	Role              string
	WorkspaceBlock    string
	EpicScope         string
	SafetyBlock       string
	ReadyJSON         string
	ReadyFallback     string
	TaskID            string
	TestStep          string
	ReviewStep        string
	InspectReviewStep string
	SourceBranch      string
	TargetBranch      string
	ConflictList      string
	PushRef           string
}

// BuiltinInteractivePrompt is a selectable built-in terminal-agent prompt.
type BuiltinInteractivePrompt = domain.BuiltinInteractivePrompt

// BuiltinInteractivePrompts returns the built-in interactive terminal prompts.
func BuiltinInteractivePrompts() []BuiltinInteractivePrompt {
	return domain.BuiltinInteractivePrompts()
}

// renderPrompt loads a template by name, checks for per-project override,
// and renders it with the given data.
func renderPrompt(name string, data promptTemplateData) string {
	tmplContent, isOverride, err := loadTemplate(name)
	if err != nil {
		panic(fmt.Sprintf("prompt: failed to load template %q: %v", name, err))
	}

	tmpl, err := template.New(name).Parse(tmplContent)
	if err != nil {
		panic(fmt.Sprintf("prompt: failed to parse template %q: %v", name, err))
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		if !isOverride {
			panic(fmt.Sprintf("prompt: failed to execute template %q: %v", name, err))
		}
		log.Printf("warning: override template %q execution failed: %v; falling back to embedded default", name, err)
		embContent, embErr := promptFS.ReadFile("prompts/" + name + ".md")
		if embErr != nil {
			panic(fmt.Sprintf("prompt: embedded fallback template %q not found: %v", name, embErr))
		}
		embTmpl := template.Must(template.New(name).Parse(string(embContent)))
		buf.Reset()
		if execErr := embTmpl.Execute(&buf, data); execErr != nil {
			panic(fmt.Sprintf("prompt: embedded template %q execute failed (bug): %v", name, execErr))
		}
	}

	return buf.String()
}

// promptOverrideDir overrides the base directory for per-project prompt overrides.
// Empty string means use the current working directory. Set in tests to avoid os.Chdir.
var promptOverrideDir string

// loadTemplate checks for a per-project override at ./loom-prompts/<name>.md,
// then falls back to the embedded default. The isOverride return indicates
// whether the returned content came from a user override file.
func loadTemplate(name string) (content string, isOverride bool, err error) {
	base := promptOverrideDir
	if base == "" {
		base = "."
	}
	overridePath := filepath.Join(base, "loom-prompts", name+".md")
	if data, readErr := os.ReadFile(overridePath); readErr == nil { //nolint:gosec // G304: intentional per-project override loading
		// Validate the override parses as a template
		if _, parseErr := template.New(name).Parse(string(data)); parseErr != nil {
			log.Printf("warning: invalid template override %s: %v, using embedded default", overridePath, parseErr)
		} else {
			return string(data), true, nil
		}
	}

	data, err := promptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		return "", false, fmt.Errorf("embedded template %q not found: %w", name, err)
	}
	return string(data), false, nil
}

// buildWorkspaceContextBlock generates the workspace context section for prompts.
// Returns empty string if workspace is nil.
func buildWorkspaceContextBlock(workspace *config.WorkspaceConfig) string {
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
	sb.WriteString("- Run `loom data` commands from the workspace root (current directory)\n")
	sb.WriteString("- Run git commands (git status, git add, git commit, git push) from the specific repo subdirectory\n")
	sb.WriteString("- Run build/test commands from the specific repo subdirectory\n")
	sb.WriteString("- Changes may span multiple repos — coordinate commits across them\n\n")

	return sb.String()
}

// buildSafetyGuardrailsBlock returns the multi-agent safety rules section.
// These rules prevent agents from interfering with each other when running
// in parallel across worktrees.
func buildSafetyGuardrailsBlock() string {
	return `
### Multi-Agent Safety Rules

You are running in a parallel multi-agent environment. Follow these rules strictly:

- **Only modify files directly related to your assigned task** — do not touch files outside your task scope
- **Never run** ` + "`git stash`" + `, ` + "`git checkout main`" + `, or ` + "`git clean`" + ` outside your assigned worktree
- **Never force-push or reset --hard** without explicit instruction from the user
- **If you encounter files/changes from another agent**, leave them alone — do not modify, revert, or clean them up
- **Commit only your changes** — do not stage unrelated modifications with ` + "`git add -A`" + ` or ` + "`git add .`" + `; use specific file paths
- **If your worktree has unexpected state**, report it via task notes or ` + "`loom complete`" + ` rather than cleaning it up
- **Do not switch branches** — you are confined to your assigned worktree branch
`
}

// backendCapabilities describes which prompt features a backend supports.
// Builders branch on these capability fields rather than comparing backend
// names, so adding a backend or capability only requires updating
// capabilitiesFor.
type backendCapabilities struct {
	// supportsSubagentSpawn indicates the backend can spawn subagents via the
	// Task tool (used for test-writing and code-review steps).
	supportsSubagentSpawn bool
	// supportsInspectReview indicates the backend supports the dedicated
	// inspect-reviewer subagent step.
	supportsInspectReview bool
}

// capabilitiesFor resolves a backend name to its prompt capabilities.
// This is the single place backend names are mapped to capabilities.
func capabilitiesFor(backendName string) backendCapabilities {
	if backendName == "claude" {
		return backendCapabilities{
			supportsSubagentSpawn: true,
			supportsInspectReview: true,
		}
	}
	return backendCapabilities{}
}

// buildTestStep returns the capability-aware test step content.
func buildTestStep(caps backendCapabilities) string {
	if caps.supportsSubagentSpawn {
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

// buildReviewStep returns the capability-aware review step content.
func buildReviewStep(caps backendCapabilities) string {
	if caps.supportsSubagentSpawn {
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

// buildInspectReviewStep returns the inspect-reviewer step for backends that
// support it.
func buildInspectReviewStep(caps backendCapabilities) string {
	if caps.supportsInspectReview {
		return `### Step 6b: Inspect Review (spawn agent)
- Use the Agent tool with subagent_type='inspect-reviewer'
- Prompt: 'Review the latest commit on the current branch. Check for bugs, logic errors, security vulnerabilities, and code quality issues.'
- Document all issues found`
	}
	return ""
}

// GeneratePlanningPrompt creates the prompt for the planning agent.
// If workspace is non-nil, workspace context is injected into the prompt.
// If parentID is non-empty, the prompt scopes task discovery to that epic.
// SYNC: The jq filters below must match taskfilter.go NeedsPlan() criteria:
//
//	planning: design empty OR has "needs-revision" label
func GeneratePlanningPrompt(agentName string, workspace *config.WorkspaceConfig, parentID string) string {
	readyJSON := "loom data ready --limit 200 --output json"
	readyFallback := "loom data ready --limit 200"
	epicScope := ""
	if parentID != "" {
		readyJSON = fmt.Sprintf("loom data ready --parent %s --limit 200 --output json", parentID)
		readyFallback = fmt.Sprintf("loom data ready --parent %s --limit 200", parentID)
		epicScope = fmt.Sprintf("\n**Epic scope: %s** — You MUST only select tasks from this epic. Do not work on tasks from other epics.\n", parentID)
	}

	prompt := renderPrompt("planning", promptTemplateData{
		AgentName:      agentName,
		WorkspaceBlock: buildWorkspaceContextBlock(workspace),
		EpicScope:      epicScope,
		SafetyBlock:    buildSafetyGuardrailsBlock(),
		ReadyJSON:      readyJSON,
		ReadyFallback:  readyFallback,
	})

	// Inject the prior-attempt checkpoint as a FALLBACK — skipped when a session
	// resume is armed (the resumed session already carries the context).
	prompt = injectCheckpointIfNotResuming(prompt)
	return prompt
}

// GenerateTaskPrompt creates the prompt for the implementation agent.
// If workspace is non-nil, workspace context is injected into the prompt.
// If parentID is non-empty, the prompt scopes task discovery to that epic.
// SYNC: The jq filters below must match taskfilter.go ReadyToImplement() criteria:
//
//	implementation: design non-empty AND no "needs-revision" label
func GenerateTaskPrompt(agentName string, workspace *config.WorkspaceConfig, parentID string, backendName string) string {
	readyJSON := "loom data ready --limit 200 --output json"
	readyFallback := "loom data ready --limit 200"
	epicScope := ""
	if parentID != "" {
		readyJSON = fmt.Sprintf("loom data ready --parent %s --limit 200 --output json", parentID)
		readyFallback = fmt.Sprintf("loom data ready --parent %s --limit 200", parentID)
		epicScope = fmt.Sprintf("\n**Epic scope: %s** — You MUST only select tasks from this epic. Do not work on tasks from other epics.\n", parentID)
	}

	caps := capabilitiesFor(backendName)
	prompt := renderPrompt("task", promptTemplateData{
		AgentName:         agentName,
		WorkspaceBlock:    buildWorkspaceContextBlock(workspace),
		EpicScope:         epicScope,
		SafetyBlock:       buildSafetyGuardrailsBlock(),
		ReadyJSON:         readyJSON,
		ReadyFallback:     readyFallback,
		TestStep:          buildTestStep(caps),
		ReviewStep:        buildReviewStep(caps),
		InspectReviewStep: buildInspectReviewStep(caps),
	})

	// Inject the prior-attempt checkpoint as a FALLBACK — skipped when a session
	// resume is armed (the resumed session already carries the context).
	prompt = injectCheckpointIfNotResuming(prompt)
	return prompt
}

// GenerateFleetPlanningPrompt creates the prompt for a fleet planning agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetPlanningPrompt(agentName, taskID string, workspace *config.WorkspaceConfig) string {
	prompt := renderPrompt("fleet_planning", promptTemplateData{
		AgentName:      agentName,
		WorkspaceBlock: buildWorkspaceContextBlock(workspace),
		SafetyBlock:    buildSafetyGuardrailsBlock(),
		TaskID:         taskID,
	})
	return injectCheckpointIfNotResuming(prompt)
}

// GenerateFleetTaskPrompt creates the prompt for a fleet implementation agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetTaskPrompt(agentName, taskID string, workspace *config.WorkspaceConfig, backendName string) string {
	caps := capabilitiesFor(backendName)
	prompt := renderPrompt("fleet_task", promptTemplateData{
		AgentName:         agentName,
		WorkspaceBlock:    buildWorkspaceContextBlock(workspace),
		SafetyBlock:       buildSafetyGuardrailsBlock(),
		TaskID:            taskID,
		TestStep:          buildTestStep(caps),
		ReviewStep:        buildReviewStep(caps),
		InspectReviewStep: buildInspectReviewStep(caps),
	})
	return injectCheckpointIfNotResuming(prompt)
}

// GenerateConflictResolutionPrompt creates the prompt for merge conflict resolution
func GenerateConflictResolutionPrompt(sourceBranch, targetBranch string, conflicts []string) string {
	return GenerateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, targetBranch)
}

// generateConflictResolutionPromptWithPush creates a conflict resolution prompt with a custom push ref.
// pushRef is used in the "git push origin <pushRef>" command (e.g., "main" or "HEAD:main").
func GenerateConflictResolutionPromptWithPush(sourceBranch, targetBranch string, conflicts []string, pushRef string) string {
	return renderPrompt("conflict_resolution", promptTemplateData{
		SafetyBlock:  buildSafetyGuardrailsBlock(),
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		ConflictList: strings.Join(conflicts, "\n"),
		PushRef:      pushRef,
	})
}

// GenerateLeadPrompt creates the prompt for the interactive lead/manager mode
func GenerateLeadPrompt() string {
	return renderPrompt("lead", promptTemplateData{
		SafetyBlock: buildSafetyGuardrailsBlock(),
	})
}

// GenerateTerminalPrompt creates the base prompt for the interactive terminal
// agent runtime. Empty promptFile preserves the built-in lead prompt; a custom
// prompt file replaces that base and still receives the terminal safety rules.
func GenerateTerminalPrompt(promptFile string) (string, error) {
	promptFile = strings.TrimSpace(promptFile)
	if promptFile == "" {
		return GenerateLeadPrompt(), nil
	}
	if strings.HasPrefix(promptFile, "builtin:") {
		id := strings.TrimSpace(strings.TrimPrefix(promptFile, "builtin:"))
		if !isBuiltinInteractivePrompt(id) {
			return "", fmt.Errorf("unknown built-in interactive prompt %q", id)
		}
		return renderPrompt(id, terminalPromptTemplateData()), nil
	}
	path, err := resolveTerminalPromptPath(promptFile)
	if err != nil {
		return "", err
	}
	agentName, role := terminalPromptIdentity()
	prompt, err := LoadPromptTemplate(path, PromptData{
		AgentName:    agentName,
		WorktreeName: agentName,
		Role:         role,
	})
	if err != nil {
		return "", err
	}
	return prompt + "\n\n" + buildSafetyGuardrailsBlock(), nil
}

func isBuiltinInteractivePrompt(id string) bool {
	return domain.IsBuiltinInteractivePrompt(id)
}

func terminalPromptTemplateData() promptTemplateData {
	agentName, role := terminalPromptIdentity()
	return promptTemplateData{
		AgentName:   agentName,
		Role:        role,
		SafetyBlock: buildSafetyGuardrailsBlock(),
	}
}

func terminalPromptIdentity() (agentName, role string) {
	agentName = strings.TrimSpace(os.Getenv("LOOM_AGENT_NAME"))
	if agentName == "" {
		agentName = "lead"
	}
	role = strings.TrimSpace(os.Getenv("LOOM_AGENT_ROLE"))
	if role == "" {
		role = "lead"
	}
	return agentName, role
}

func resolveTerminalPromptPath(promptFile string) (string, error) {
	promptFile = strings.TrimSpace(promptFile)
	if filepath.IsAbs(promptFile) {
		return promptFile, nil
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, promptFile)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("resolve prompt file %q relative to cwd: %w", promptFile, statErr)
		}
	}
	if ws, err := config.ResolveActiveWorkspace(); err == nil && ws != nil && strings.TrimSpace(ws.Path) != "" {
		candidate := filepath.Join(ws.Path, promptFile)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("resolve prompt file %q relative to workspace %q: %w", promptFile, ws.Path, statErr)
		}
	}
	return "", fmt.Errorf("prompt file %q not found relative to current directory or active workspace root", promptFile)
}

// injectCheckpointIfNotResuming adds the prior-attempt checkpoint to the prompt
// as a FALLBACK, but SKIPS it when a session resume is armed
// (backends.GetResumeSessionID() != ""). Resume-first / checkpoint-fallback (P4):
// a resumed session already carries the full prior conversation, so re-injecting
// the git-diff "## PREVIOUS ATTEMPT CONTEXT" block would re-pay for context the
// model already has. Callers must arm the resume (SetResumeSessionID) BEFORE
// building the prompt for this to take effect.
func injectCheckpointIfNotResuming(prompt string) string {
	if backends.GetResumeSessionID() != "" {
		return prompt // resuming → the session carries the context; no checkpoint
	}
	wtPath := os.Getenv("LOOM_WORKTREE_PATH")
	if wtPath == "" {
		return prompt
	}
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(wtPath))
	if err != nil || cp == nil {
		return prompt
	}
	return injectCheckpointContext(prompt, cp)
}

// injectCheckpointContext inserts a "PREVIOUS ATTEMPT CONTEXT" section into the prompt.
// It places the block before "### Step 1:" if found, otherwise appends to the end.
// Yield checkpoints (YieldReason non-empty) get trusting "continue" instructions,
// while crash checkpoints get cautious "review and decide" instructions.
func injectCheckpointContext(prompt string, cp *config.Checkpoint) string {
	var sb strings.Builder
	sb.WriteString("\n\n## PREVIOUS ATTEMPT CONTEXT\n\n")

	if cp.YieldReason != "" {
		sb.WriteString(fmt.Sprintf("A previous attempt on task **%s** was preempted (yield reason: %s)", cp.TaskID, cp.YieldReason))
		sb.WriteString(fmt.Sprintf(" at %s.\n\n", cp.Timestamp.Format(time.RFC3339)))
	} else {
		sb.WriteString(fmt.Sprintf("A previous attempt on task **%s** exited with code %d", cp.TaskID, cp.ExitCode))
		if cp.ErrorClass != "" {
			sb.WriteString(fmt.Sprintf(" (error: %s)", cp.ErrorClass))
		}
		sb.WriteString(fmt.Sprintf(" at %s.\n\n", cp.Timestamp.Format(time.RFC3339)))
	}

	if cp.GitDiff != "" {
		sb.WriteString("The previous attempt made these uncommitted changes:\n```diff\n")
		sb.WriteString(cp.GitDiff)
		sb.WriteString("\n```\n\n")
	} else {
		sb.WriteString("The previous attempt made no uncommitted changes.\n\n")
	}

	if cp.YieldReason != "" {
		sb.WriteString("**Instructions**: The previous agent was interrupted, not crashed. Its changes are likely correct and in-progress. Continue from where it left off. Review the diff to understand what was done, then pick up the next step.\n")
	} else {
		sb.WriteString("**Instructions**: Review the previous changes. If they look correct and complete, continue from where they left off. If they look wrong or incomplete, start fresh. Do NOT blindly re-apply the diff — use it as context to understand what was attempted.\n")
	}

	// Inject before "### Step 1:" if found
	idx := strings.Index(prompt, "### Step 1:")
	if idx > 0 {
		return prompt[:idx] + sb.String() + "\n" + prompt[idx:]
	}
	return prompt + sb.String()
}

const readOnlyPreamble = `IMPORTANT: You are running in READ-ONLY mode. You MUST NOT modify any files, create new files, or run destructive commands. You may only read files, search code, and provide analysis/comments. Use loom data commands to comment on tasks but do not make code changes.`

// ReadOnlyPreamble returns the read-only instruction preamble if LOOM_READ_ONLY is set.
// Returns empty string if not in read-only mode.
func ReadOnlyPreamble() string {
	if os.Getenv("LOOM_READ_ONLY") == "1" {
		return readOnlyPreamble
	}
	return ""
}

// truncateUTF8Safe truncates s to at most max bytes without splitting a
// multi-byte UTF-8 character, appending a truncation marker if shortened.
func truncateUTF8Safe(s string, max int) string { //nolint:unparam // max is parameterized for readability at call sites
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "\n... [truncated]"
}

func init() {
	git.ConflictPromptGen = GenerateConflictResolutionPrompt
	git.ConflictPromptGenWithPush = GenerateConflictResolutionPromptWithPush
}
