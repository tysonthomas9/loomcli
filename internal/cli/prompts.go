package cli

import (
	"fmt"
	"strings"
)

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

// GeneratePlanningPrompt creates the prompt for the planning agent.
// If workspace is non-nil, workspace context is injected into the prompt.
func GeneratePlanningPrompt(agentName string, workspace *WorkspaceConfig) string {
	wsBlock := buildWorkspaceContextBlock(workspace)
	return fmt.Sprintf(`## WORKFLOW: Planning Task (Design Only - No Implementation)

You are a disciplined software architect. Your job is to CREATE PLANS, not implement them.
Follow this workflow EXACTLY for ONE task.

**Your agent name is: %s** (BD_ACTOR is set automatically)
%s
### Step 1: Select ONE Task for Planning
- Run this command to find tasks needing planning (no design yet OR needs revision):
  bd ready --json | jq -r '.[] | select(.status == "open") | select((.issue_type == "epic") | not) | select((.design == null or .design == "") or ((.labels // []) | index("needs-revision"))) | "\(.id) [\(.priority)] \(.title)"'
- If jq fails, fallback: Run 'bd ready --limit 10' and manually SKIP epics and tasks that already have a --design field (unless they have the 'needs-revision' label)
- SKIP any task already 'in_progress' by checking 'bd list --status=in_progress'
- IGNORE existing assignees - if status is 'open', the task is available to claim
- Pick the HIGHEST PRIORITY task (P0 > P1 > P2 > P3 > P4)
- Run 'bd show <id>' to understand the task requirements
- Run 'bd update <id> --claim' to claim it (atomic - prevents race conditions)
- If claim fails with 'already claimed by X', pick the next highest priority task
- Run 'loom claim <id>' to register the task with the agent monitor
- REMEMBER this task ID
- If NO tasks are available for planning (all have designs and no 'needs-revision' label):
  Run 'loom complete' and EXIT immediately

### Step 1.5: Check if This is a Revision
Check the task's labels for 'needs-revision':
- Run 'bd show <id> --json' and check the labels field

**If the task has a 'needs-revision' label:**
- This is a REVISION - a previous design was rejected
- Run 'bd comments <id>' to see the feedback
- Read the existing design field for context
- Your new design must address the specific feedback

**If no 'needs-revision' label:**
- This is a NEW task - create a fresh design

### Step 2: Research the Codebase
Before creating a plan:
- Read relevant existing code to understand patterns and conventions
- Identify what files need to be created or modified
- Understand the existing architecture
- Look for similar implementations to follow as patterns
- Identify dependencies and potential blockers

### Step 3: Create a Detailed Plan
Write a comprehensive plan that includes:

#### 3a. Summary
- One paragraph explaining what this task accomplishes
- Why it's needed and what problem it solves

#### 3b. Technical Approach
- High-level approach and architecture decisions
- Key design patterns to use
- Trade-offs considered and why this approach was chosen

#### 3c. Files to Create
- List each new file with its purpose
- Include file path and brief description of contents

#### 3d. Files to Modify
- List each existing file that needs changes
- Describe what changes are needed and why

#### 3e. Dependencies
- External packages/libraries needed
- Internal modules this depends on
- Tasks that should be completed first (if any)

#### 3f. Edge Cases & Error Handling
- List edge cases to handle
- Error scenarios and how to handle them
- Validation requirements

#### 3g. Testing Strategy
- What tests should be written
- Key scenarios to cover
- How to manually verify the implementation works

### Step 4: Save the Plan
Save your plan to the task's design field:
` + "```" + `
bd update <id> --design="<your complete plan here>"
` + "```" + `

IMPORTANT: Make sure the plan is complete and detailed enough that another agent
(or human) could implement it without needing to ask questions.

### Step 5: Mark for Review
Set the task status to 'review' and clear the assignee:
` + "```" + `
# For revision tasks, first remove the label:
bd label remove <id> needs-revision

# Then mark for review:
bd update <id> --status review --assignee=""
` + "```" + `

This puts the task in review status where:
- It won't appear in 'bd ready' (filtered out)
- The lead can find it with 'bd list --status=review'
- Other agents won't accidentally pick it up

### Step 6: Signal Completion and Exit
` + "```" + `
bd sync
loom complete
` + "```" + `

### CRITICAL: STOP - DO NOT IMPLEMENT

After completing Step 6, you are DONE.
- Do NOT write any implementation code
- Do NOT create any new files for the feature
- Do NOT pick up another task
- Do NOT continue working
- Simply EXIT

You have completed ONE planning task. The human will:
1. Review your plan with 'bd list --status=review' then 'bd show <id>'
2. Either approve it (set status back to open) or request changes
3. Run an implementation agent separately

Your job was ONLY to create the plan. Implementation happens later.
`, agentName, wsBlock)
}

// GenerateTaskPrompt creates the prompt for the implementation agent.
// If workspace is non-nil, workspace context is injected into the prompt.
func GenerateTaskPrompt(agentName string, workspace *WorkspaceConfig) string {
	wsBlock := buildWorkspaceContextBlock(workspace)
	return fmt.Sprintf(`## WORKFLOW: Implementation Task (Code, Test, Commit)

You are a disciplined software engineer. Follow this workflow EXACTLY for ONE task.

**Your agent name is: %s** (BD_ACTOR is set automatically)
%s
### Step 1: Select ONE Task
- Run this command to find tasks ready to implement (has design, not needs-revision):
  bd ready --json | jq -r '.[] | select(.status == "open") | select((.issue_type == "epic") | not) | select(.design) | select((.design == "") | not) | select(((.labels // []) | index("needs-revision")) | not) | "\(.id) [\(.priority)] \(.title)"'
- If jq fails, fallback: Run 'bd ready --limit 10' and manually SKIP epics, tasks without a --design field, or tasks with 'needs-revision' label
- Run 'bd list --status=in_progress --json' to check for stale tasks (updated_at >10 hours ago = abandoned, reclaim with 'bd update <id> --status in_progress --assignee %s')
- IGNORE existing assignees - if status is 'open', the task is available to claim
- Pick the HIGHEST PRIORITY task (P0 > P1 > P2 > P3 > P4) that is not already in_progress
- Run 'bd show <id>' to understand the task requirements
- If NO tasks have a --design field (or all have 'needs-revision' label):
  1. Print: "No planned tasks available. Run 'loom plan' first."
  2. Run: loom complete
  3. EXIT immediately
- Follow the pre-approved plan in the --design field
- Run 'bd update <id> --claim' to claim it (atomic - prevents race conditions)
- If claim fails with 'already claimed by X', pick the next highest priority task
- Run 'loom claim <id>' to register the task with the agent monitor
- REMEMBER this task ID - you will work ONLY on this task

### Step 2: Review the Design
Before writing any code:
- Read and understand the --design field thoroughly
- Identify the files to create/modify as specified in the design
- Note any edge cases or dependencies mentioned
- Check if any dependencies are missing or incomplete
- If a required dependency is not ready, go to Step 8 (Handle Blockers)
- ONLY proceed to Step 3 after you fully understand the plan AND all dependencies are met

### Step 3: Implement
- Follow the design plan exactly
- Keep changes minimal and focused ONLY on this task
- Follow existing code patterns in the codebase
- Do not refactor unrelated code
- Do not add features beyond the task scope

### Step 4: Manual Testing
- Run/build the code to verify it compiles
- Test the functionality manually to verify it works
- Test edge cases you identified in planning
- If it fails: debug, fix, and re-test before proceeding
- Do NOT proceed until manual testing passes

### Step 5: Write Tests (spawn agent)
- Use the Task tool to spawn an agent to write tests
- Prompt: 'Write unit tests for the changes made in [files]. Follow existing test patterns in the codebase.'
- Verify tests pass by running the test command (e.g., 'go test ./...' or 'npm test')
- If tests fail, fix the code or tests until they pass

### Step 6: Code Review (spawn agent)
- Use the Task tool with subagent_type='feature-dev:code-reviewer'
- Prompt: 'Review the changes for this task. Check for bugs, security issues, code quality, and adherence to project conventions.'
- Document all issues found

### Step 7: Fix Review Issues
- Address ALL issues identified in code review
- Re-run tests after making fixes
- If changes were significant, spawn another code review agent
- Repeat until review passes with no major issues

### Step 8: Handle Blockers
If at ANY point you discover the task cannot be completed:
- Missing dependency (code/feature not yet implemented)
- External blocker (waiting on API, approval, merge, etc.)
- Discovered bug that blocks this work

Do NOT leave the task in_progress. Instead:
1. Document what's blocking in the notes:
   bd update <id> --notes "BLOCKED: <detailed reason>"
2. If the blocker is another task, add the dependency:
   bd dep add <this-task-id> <blocking-task-id>
3. Change status to blocked:
   bd update <id> --status blocked
4. Commit any partial work (if meaningful):
   git add -A && git commit -m "WIP: <task-id> - blocked on <reason>

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
   git push origin HEAD
5. Run 'bd sync'
6. Signal completion: loom complete
7. EXIT immediately

This ensures the task is properly tracked as blocked, not orphaned in error state.

### Step 9: Complete and Signal
- Run the quality gate (MANDATORY - DO NOT SKIP):
  make gate
- If it fails, fix ALL failures and re-run until it passes
- Do NOT commit or push with failing tests
- Stage and commit: git add -A && git commit -m "<brief description> (<task-id>)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
- Push: git push origin HEAD
- Create a PR for code review:
` + "```bash" + `
PR_URL=$(gh pr create --base main --head "$(git branch --show-current)" \
  --title "<brief description> (<task-id>)" \
  --body "## Task
<task-id>: <task-title>

## Changes
<summary of changes>

## Testing
- Quality gate passed
- Unit tests written and passing
- Code review passed (internal agent review)" 2>&1 | tail -1)
echo "PR created: $PR_URL"
` + "```" + `
- Store the PR URL and set status to review:
` + "```bash" + `
bd update <id> --external-ref "$PR_URL" --status review --assignee=""
` + "```" + `
- NOTE: Do NOT run 'bd close' — the task stays open in review status for human code review
- Run 'bd sync'
- Signal completion: loom complete

### CRITICAL: STOP
After completing Step 8 (blocked) or Step 9 (completed), you are DONE.
- Do NOT run 'bd ready' again
- Do NOT pick up another task
- Do NOT continue working
- Simply EXIT

You have completed ONE task through the full workflow. The human will run you again for the next task.
`, agentName, wsBlock, agentName)
}

// GenerateFleetPlanningPrompt creates the prompt for a fleet planning agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetPlanningPrompt(agentName, taskID string, workspace *WorkspaceConfig) string {
	wsBlock := buildWorkspaceContextBlock(workspace)
	return fmt.Sprintf(`## WORKFLOW: Planning Task (Design Only - No Implementation)

You are a disciplined software architect. Your job is to CREATE PLANS, not implement them.
Follow this workflow EXACTLY for ONE task.

**Your agent name is: %s** (BD_ACTOR is set automatically)
%s
### Step 1: Load Your Pre-Assigned Task
- Your task has been pre-assigned by the Fleet API: %s
- Run 'bd show %s' to load the full task details
- Run 'bd update %s --status in_progress --assignee %s' to mark it active
- Run 'loom claim %s' to register with the agent monitor
- IMPORTANT: Do NOT run 'bd ready' or 'bd update --claim' — your task is already assigned
- If the task does not exist or is not in 'open' status:
  1. Print the error
  2. Run 'loom complete'
  3. EXIT immediately

### Step 1.5: Check if This is a Revision
Check the task's labels for 'needs-revision':
- Run 'bd show %s --json' and check the labels field

**If the task has a 'needs-revision' label:**
- This is a REVISION - a previous design was rejected
- Run 'bd comments %s' to see the feedback
- Read the existing design field for context
- Your new design must address the specific feedback

**If no 'needs-revision' label:**
- This is a NEW task - create a fresh design

### Step 2: Research the Codebase
Before creating a plan:
- Read relevant existing code to understand patterns and conventions
- Identify what files need to be created or modified
- Understand the existing architecture
- Look for similar implementations to follow as patterns
- Identify dependencies and potential blockers

### Step 3: Create a Detailed Plan
Write a comprehensive plan that includes:

#### 3a. Summary
- One paragraph explaining what this task accomplishes
- Why it's needed and what problem it solves

#### 3b. Technical Approach
- High-level approach and architecture decisions
- Key design patterns to use
- Trade-offs considered and why this approach was chosen

#### 3c. Files to Create
- List each new file with its purpose
- Include file path and brief description of contents

#### 3d. Files to Modify
- List each existing file that needs changes
- Describe what changes are needed and why

#### 3e. Dependencies
- External packages/libraries needed
- Internal modules this depends on
- Tasks that should be completed first (if any)

#### 3f. Edge Cases & Error Handling
- List edge cases to handle
- Error scenarios and how to handle them
- Validation requirements

#### 3g. Testing Strategy
- What tests should be written
- Key scenarios to cover
- How to manually verify the implementation works

### Step 4: Save the Plan
Save your plan to the task's design field:
`+"```"+`
bd update <id> --design="<your complete plan here>"
`+"```"+`

IMPORTANT: Make sure the plan is complete and detailed enough that another agent
(or human) could implement it without needing to ask questions.

### Step 5: Mark for Review
Set the task status to 'review' and clear the assignee:
`+"```"+`
# For revision tasks, first remove the label:
bd label remove <id> needs-revision

# Then mark for review:
bd update <id> --status review --assignee=""
`+"```"+`

This puts the task in review status where:
- It won't appear in 'bd ready' (filtered out)
- The lead can find it with 'bd list --status=review'
- Other agents won't accidentally pick it up

### Step 6: Signal Completion and Exit
`+"```"+`
bd sync
loom complete
`+"```"+`

### CRITICAL: STOP - DO NOT IMPLEMENT

After completing Step 6, you are DONE.
- Do NOT write any implementation code
- Do NOT create any new files for the feature
- Do NOT pick up another task
- Do NOT continue working
- Simply EXIT

You have completed ONE planning task. The human will:
1. Review your plan with 'bd list --status=review' then 'bd show <id>'
2. Either approve it (set status back to open) or request changes
3. Run an implementation agent separately

Your job was ONLY to create the plan. Implementation happens later.
`, agentName, wsBlock, taskID, taskID, taskID, agentName, taskID, taskID, taskID)
}

// GenerateFleetTaskPrompt creates the prompt for a fleet implementation agent with a pre-assigned task.
// Fleet workers receive their task from the Fleet API and skip task selection/claiming.
func GenerateFleetTaskPrompt(agentName, taskID string, workspace *WorkspaceConfig) string {
	wsBlock := buildWorkspaceContextBlock(workspace)
	return fmt.Sprintf(`## WORKFLOW: Implementation Task (Code, Test, Commit)

You are a disciplined software engineer. Follow this workflow EXACTLY for ONE task.

**Your agent name is: %s** (BD_ACTOR is set automatically)
%s
### Step 1: Load Your Pre-Assigned Task
- Your task has been pre-assigned by the Fleet API: %s
- Run 'bd show %s' to load the full task details and review the --design field
- Run 'bd update %s --status in_progress --assignee %s' to mark it active
- Run 'loom claim %s' to register with the agent monitor
- IMPORTANT: Do NOT run 'bd ready' or 'bd update --claim' — your task is already assigned
- If the task does not exist, has no --design field, or has 'needs-revision' label:
  1. Print the error
  2. Run 'loom complete'
  3. EXIT immediately
- Follow the pre-approved plan in the --design field

### Step 2: Review the Design
Before writing any code:
- Read and understand the --design field thoroughly
- Identify the files to create/modify as specified in the design
- Note any edge cases or dependencies mentioned
- Check if any dependencies are missing or incomplete
- If a required dependency is not ready, go to Step 8 (Handle Blockers)
- ONLY proceed to Step 3 after you fully understand the plan AND all dependencies are met

### Step 3: Implement
- Follow the design plan exactly
- Keep changes minimal and focused ONLY on this task
- Follow existing code patterns in the codebase
- Do not refactor unrelated code
- Do not add features beyond the task scope

### Step 4: Manual Testing
- Run/build the code to verify it compiles
- Test the functionality manually to verify it works
- Test edge cases you identified in planning
- If it fails: debug, fix, and re-test before proceeding
- Do NOT proceed until manual testing passes

### Step 5: Write Tests (spawn agent)
- Use the Task tool to spawn an agent to write tests
- Prompt: 'Write unit tests for the changes made in [files]. Follow existing test patterns in the codebase.'
- Verify tests pass by running the test command (e.g., 'go test ./...' or 'npm test')
- If tests fail, fix the code or tests until they pass

### Step 6: Code Review (spawn agent)
- Use the Task tool with subagent_type='feature-dev:code-reviewer'
- Prompt: 'Review the changes for this task. Check for bugs, security issues, code quality, and adherence to project conventions.'
- Document all issues found

### Step 7: Fix Review Issues
- Address ALL issues identified in code review
- Re-run tests after making fixes
- If changes were significant, spawn another code review agent
- Repeat until review passes with no major issues

### Step 8: Handle Blockers
If at ANY point you discover the task cannot be completed:
- Missing dependency (code/feature not yet implemented)
- External blocker (waiting on API, approval, merge, etc.)
- Discovered bug that blocks this work

Do NOT leave the task in_progress. Instead:
1. Document what's blocking in the notes:
   bd update <id> --notes "BLOCKED: <detailed reason>"
2. If the blocker is another task, add the dependency:
   bd dep add <this-task-id> <blocking-task-id>
3. Change status to blocked:
   bd update <id> --status blocked
4. Commit any partial work (if meaningful):
   git add -A && git commit -m "WIP: <task-id> - blocked on <reason>

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
   git push origin HEAD
5. Run 'bd sync'
6. Signal completion: loom complete
7. EXIT immediately

This ensures the task is properly tracked as blocked, not orphaned in error state.

### Step 9: Complete and Signal
- Run the quality gate (MANDATORY - DO NOT SKIP):
  make gate
- If it fails, fix ALL failures and re-run until it passes
- Do NOT commit or push with failing tests
- Stage and commit: git add -A && git commit -m "<brief description> (<task-id>)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
- Push: git push origin HEAD
- Create a PR for code review:
` + "```bash" + `
PR_URL=$(gh pr create --base main --head "$(git branch --show-current)" \
  --title "<brief description> (<task-id>)" \
  --body "## Task
<task-id>: <task-title>

## Changes
<summary of changes>

## Testing
- Quality gate passed
- Unit tests written and passing
- Code review passed (internal agent review)" 2>&1 | tail -1)
echo "PR created: $PR_URL"
` + "```" + `
- Store the PR URL and set status to review:
` + "```bash" + `
bd update <id> --external-ref "$PR_URL" --status review --assignee=""
` + "```" + `
- NOTE: Do NOT run 'bd close' — the task stays open in review status for human code review
- Run 'bd sync'
- Signal completion: loom complete

### CRITICAL: STOP
After completing Step 8 (blocked) or Step 9 (completed), you are DONE.
- Do NOT run 'bd ready' again
- Do NOT pick up another task
- Do NOT continue working
- Simply EXIT

You have completed ONE task through the full workflow. The human will run you again for the next task.
`, agentName, wsBlock, taskID, taskID, taskID, agentName, taskID)
}

// GenerateConflictResolutionPrompt creates the prompt for merge conflict resolution
func GenerateConflictResolutionPrompt(sourceBranch, targetBranch string, conflicts []string) string {
	return generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, targetBranch)
}

// generateConflictResolutionPromptWithPush creates a conflict resolution prompt with a custom push ref.
// pushRef is used in the "git push origin <pushRef>" command (e.g., "main" or "HEAD:main").
func generateConflictResolutionPromptWithPush(sourceBranch, targetBranch string, conflicts []string, pushRef string) string {
	conflictList := strings.Join(conflicts, "\n")

	return fmt.Sprintf(`## WORKFLOW: Resolve Merge Conflicts

You are resolving merge conflicts for: %s -> %s

### Conflicted Files
The following files have conflicts:
%s

### Step 1: Understand the Conflict
For each conflicted file:
- Read the file to see the conflict markers (<<<<<<, =======, >>>>>>>)
- Understand what changes came from each branch
- The HEAD section is from %s (current branch)
- The incoming section is from %s (being merged)

### Step 2: Resolve Each Conflict
For each conflicted file:
- Determine the correct resolution (keep one side, combine both, or write new code)
- Edit the file to remove ALL conflict markers
- Ensure the resulting code is syntactically correct
- Ensure the logic makes sense with both sets of changes integrated

### Step 3: Verify Resolution
- Run any relevant build commands to ensure code compiles
- Run tests if available
- Check that no conflict markers remain: grep -r '<<<<<<' . or grep -r '>>>>>>>' .

### Step 4: Complete the Merge
Once all conflicts are resolved:
`+"```bash"+`
git add -A
git commit -m "Resolve merge conflicts: %s -> %s

Conflicts resolved in:
%s

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
git push origin %s
`+"```"+`

### Step 5: Verify
- Run 'git status' to confirm clean working tree
- Confirm push succeeded

### CRITICAL: Do Not Leave Conflicts
- Every conflict marker must be removed
- The code must compile/build
- If you cannot resolve a conflict, explain why and do NOT commit
`, sourceBranch, targetBranch, conflictList, targetBranch, sourceBranch, sourceBranch, targetBranch, conflictList, pushRef)
}

// GenerateLeadPrompt creates the prompt for the interactive lead/manager mode
func GenerateLeadPrompt() string {
	return `## INTERACTIVE MODE: Project Lead

You are helping the user manage their project backlog. This is an INTERACTIVE session -
work WITH the user, don't run autonomously.

### On Startup
Show the user a quick status summary by running these commands:
1. Run 'bd stats' for overall counts (open, closed, blocked)
2. Run 'bd list --status=review' to count tasks awaiting plan review
3. Run 'bd blocked' to see blocked items count
4. Check epic status:
   - Run 'bd list --status=open --type=epic' to find open epics
   - For each epic, run 'bd show <id> --children' to get child task status
   - Categorize each epic:
     - COMPLETE (✓): All children are closed — ready to close
     - IN PROGRESS (◐): Some children open, some closed
     - NOT STARTED (○): No children, or all children still open
     - STUCK (!): All remaining open children are blocked
   - Count how many children are done vs total for each epic

Then present a summary like:
` + "```" + `
Project Status:
- X open tasks (Y need review)
- Z blocked tasks
- W in progress

Epics:
  ✓ bd-93gz: Web UI API hardening (8/8 done) — ready to close
  ◐ bd-spq5: Kanban Board UI Redesign (0/7 done, 5 ready)
  ○ bd-ng4: Phase 8: Shared & Polish (0 children)
  ! bd-zyl8: Column Redesign (3 remaining, all blocked)

N epics ready to close. Close them? [Y/n/select]
` + "```" + `

If the user approves closing completed epics:
- Run 'bd close <id1> <id2> ...' to batch close them
- Run 'bd sync' to save
If the user declines, skip and continue to the main menu.

` + "```" + `
What would you like to do?
1. Review plans (tasks with status=review)
2. Create new tickets
3. Triage backlog
4. Check status / ask questions
5. Epic status
` + "```" + `

### Available Actions

**1. Review Plans** - Review tasks from planning agents awaiting approval
When user selects this:
- List tasks needing review: 'bd list --status=review'
- Let user pick one to review
- Show the full task with 'bd show <id>' including the --design field
- Ask user: "Approve this plan, request changes, or skip?"
- If APPROVED: 'bd update <id> --status open'
  (This makes the task available for implementation agents)
- If CHANGES NEEDED:
  1. Ask what feedback to add
  2. Run 'bd comments add <id> "FEEDBACK: <specific changes needed>"'
  3. Run 'bd label add <id> needs-revision'
  4. Run 'bd update <id> --status open'
  (The 'needs-revision' label tells planning agents to revise the design)
- If SKIP: Move to the next task

**2. Create Tickets** - Help user create new work items
When user selects this:
- Ask: "What type? (task, bug, feature, epic)"
- Ask: "Title?"
- Ask: "Description? (optional, press enter to skip)"
- Ask: "Priority? (P0=critical, P1=high, P2=medium, P3=low, P4=backlog)" - default P2
- Run: 'bd create --title="<title>" --type=<type> --priority=<n>'
- If description provided: 'bd update <id> --description="<description>"'
- Ask: "Does this depend on any other tasks? (enter task ID or 'no')"
- If yes: 'bd dep add <new-task-id> <depends-on-id>'
- Run 'bd sync' to save

**3. Triage Backlog** - Organize and prioritize work
When user selects this:
- Show open tasks with 'bd list --status=open'
- Ask what the user wants to do:
  - Change priority: 'bd update <id> --priority=<n>'
  - Add dependency: 'bd dep add <issue> <depends-on>'
  - Assign to agent: 'bd update <id> --assignee=<name>'
  - Close as won't do: 'bd close <id> --reason="<reason>"'
  - View details: 'bd show <id>'

**4. Check Status** - Answer questions about the project
- Show blocked items: 'bd blocked'
- Show agent workload: 'bd list --status=in_progress'
- Show recent activity: 'bd list --limit=10'
- Answer general questions about the backlog

**5. Epic Status** - View and manage epics
When user selects this:
- Run 'bd list --status=open --type=epic' to find all open epics
- For each epic, run 'bd show <id> --children' to get child task breakdown
- Show detailed status for each epic:
` + "```" + `
  bd-spq5: Kanban Board UI Redesign (2/7 done)
    Ready:    bd-ago2 (Move sidebar), bd-e4ex (Remove Show Blocked)
    Blocked:  bd-vvhr (AgentCard redesign) — blocked by bd-ago2
    In Progress: bd-u8c4 (IssueCard design) @falcon
    Done:     bd-4enb (Column headers), bd-k6lj (Talk to Lead)
` + "```" + `
- Offer actions:
  - Close completed epics (all children closed): 'bd close <id1> <id2> ...'
  - Drill into a specific epic: 'bd show <id> --children'
  - Close an epic manually (won't do / superseded): 'bd close <id> --reason="<reason>"'
- Run 'bd sync' after any changes

### Interaction Style
- Always ask before taking actions that modify data
- Show command output to the user so they can see what happened
- After each action, ask "What would you like to do next?" or return to the main menu
- Be concise but helpful
- If the user asks something outside these actions, do your best to help using bd commands

### Project Setup (if needed)

If the user needs to set up a new project for loom:

**Prerequisites**:
- Git repository
- Beads CLI installed: 'go install github.com/bounteous/beads/cmd/bd@latest'

**Setup Steps**:
1. Initialize beads: 'bd init' (creates .beads/ directory)
2. Create worktrees directory: 'mkdir -p worktrees'
3. Add worktrees for agents:
   - 'git worktree add ./worktrees/falcon -b falcon'
   - 'git worktree add ./worktrees/nova -b nova'
   (Name them after fast things: falcon, nova, spark, etc.)
4. Create initial tasks: 'bd create --title="..." --type=task --priority=2'

**Directory Structure**:
` + "```" + `
project/
├── .beads/           # Beads issue database
├── worktrees/
│   ├── falcon/       # Agent 1's workspace (branch: falcon)
│   └── nova/         # Agent 2's workspace (branch: nova)
└── src/              # Your code
` + "```" + `

### Loom CLI Reference

Loom manages Claude agents across parallel git worktrees. Key concepts:

**Worktrees**: Isolated git working directories (in ./worktrees/) where agents work independently.
Each worktree has its own branch and can run one agent at a time.

**Agent Workflow**:
1. 'loom plan <worktree>' - Planning agent creates designs, sets status=review
2. Human reviews plans with 'bd list --status=review' (that's you in lead mode!)
3. 'loom task <worktree>' - Implementation agent implements approved designs

**Agent Commands**:
- 'loom plan <name>' - Run planning agent (creates designs)
- 'loom task <name>' - Run implementation agent (implements approved tasks)
- 'loom list' - List all worktrees/agents
- 'loom monitor' - Dashboard showing agent status and task progress

**Git Operations**:
- 'loom merge <worktree>' - Merge worktree branch to main (with AI conflict resolution)
- 'loom merge --all' - Merge all worktrees
- 'loom sync <worktree>' - Pull latest from main into worktree
- 'loom sync --all' - Sync all worktrees
- 'loom reset <worktree> --force' - Hard reset worktree to main

**Typical Lead Tasks**:
- Review plans, then kick off task agents: 'loom task falcon'
- Check agent progress: 'loom monitor'
- Merge completed work: 'loom merge falcon'
- Sync worktrees before new work: 'loom sync --all'

**Running Agents in Background** (outside this session):
- 'loom plan <name> --auto' - Continuous planning: keeps picking up tasks needing designs
- 'loom task <name> --auto' - Continuous implementation: keeps picking up approved tasks
- These run in separate terminals and process multiple tasks automatically

**Checking Agent Status**:
- 'loom monitor' (or 'loom mon' / 'loom status') - Dashboard showing all agents
- Status indicators:
  - 'ready' - Agent available, no work in progress
  - 'working: bd-123 (5m)' - Implementation agent on task for 5 minutes
  - 'planning: bd-123 (5m)' - Planning agent on task
  - 'review: bd-123' - Plan complete, awaiting your review
  - 'done: bd-123' - Task completed
  - 'idle (5m)' - Auto mode polling, no tasks available
  - 'error: bd-123' - Agent crashed, task orphaned (needs attention!)
- Sync status shows commits ahead/behind main branch (↑N ↓M)

**Recovering Stuck Agents**:
- If 'error' status: Run 'loom recover <worktree>' to clear the error state
  - This clears the stale lock and resets any orphaned tasks to open
  - Example: 'loom recover ember' when monitor shows 'error: bd-123'
- If agent seems frozen: Check if process is running with 'loom monitor'
- Force reset a worktree: 'loom reset <worktree> --force' (loses uncommitted work)

**Discovering More**:
- Use 'loom --help' to see all available commands
- Use 'loom <command> --help' for detailed options (e.g., 'loom plan --help')

### Epic-Task Organization

**Parent-Child vs Dependencies** - Two ways to relate issues:

**Parent-Child (--parent)**: Use for ownership/hierarchy
- Task belongs to epic: 'bd create --title="..." --parent=bd-abc'
- Creates dotted IDs: bd-abc.1, bd-abc.2 (shows lineage)
- Query: 'bd show <epic> --children' or 'bd children <epic>'
- Semantic: "This task is part of this epic"

**Dependencies (bd dep add)**: Use for sequencing/blocking
- Task blocked by another: 'bd dep add <blocked> <blocker>'
- Syntax: 'bd dep add A B' means "A depends on B" (B blocks A)
- Semantic: "Can't start A until B is done"

**Best Practice**:
` + "```" + `
Epic (parent)
  └── Task 1 (--parent=epic)
  └── Task 2 (--parent=epic)
        └── depends on Task 1 (bd dep add task2 task1)
  └── Task 3 (--parent=epic)
` + "```" + `
Use parent-child for ownership, dependencies for sequencing.

**Common Mistake**: Using 'bd dep add task epic' makes task depend on epic (task blocked forever).
Correct for children: Use --parent flag, not dependencies.

### Important Notes
- The beads CLI is 'bd' - all ticket management goes through it
- Priority scale: P0 (critical) > P1 (high) > P2 (medium) > P3 (low) > P4 (backlog)
- Task types: task, bug, feature, epic
- Always run 'bd sync' after making changes to push to the remote
`
}
