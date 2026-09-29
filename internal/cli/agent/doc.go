// Package agent builds the prompts loom hands to an agent, and the checks that
// surround a run.
//
// Prompt construction is the bulk of it. An agent's prompt depends on what it
// is being asked to do — plan or implement — and on where its state lives:
// GeneratePlanningPrompt and GenerateTaskPrompt serve local workspaces, their
// GenerateFleet* counterparts serve fleet-backed ones, GenerateLeadPrompt
// builds the lead's standing prompt, and GenerateTerminalPrompt renders an
// operator-supplied prompt file for an interactive session.
// GenerateConflictResolutionPrompt produces the prompt for the narrower job of
// resolving a merge conflict.
//
// FormatIssueText renders an issue into the text an agent actually reads, so
// the same issue always looks the same to every backend.
//
// Around the run, ClaimStillHeld verifies loom still owns the task before
// acting on its result — a claim can lapse while an agent works — and
// EnsureSkillMaterializeHook installs the hook that materializes workspace
// skills into the worktree before the agent starts.
package agent
