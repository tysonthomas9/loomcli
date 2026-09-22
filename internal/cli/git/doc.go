// Package git is loom's git and GitHub surface: nearly every repository
// operation an agent or command performs goes through here rather than
// shelling out ad hoc. A few low-level recovery and runtime paths still invoke
// the binary directly by design, as does internal/gitbranch.
//
// The operations divide into three groups. Worktree manipulation — checkout,
// branch inspection, and the merge and rebase paths — is what moves an agent's
// work along. Diffing (DiffFiles, DiffCommits, DiffFilePatch, ComputeDiffPatch)
// produces the ops.Diff* results the web layer and the task bridge render.
// GitHub interaction covers pull requests, with CheckGhInstalled gating the
// paths that need the gh CLI and FilterPullRequestsForReview narrowing a PR
// list to what actually wants review.
//
// Conflicts are handed back to an agent rather than resolved here.
// GetConflictedFiles reports what is in conflict, and the ConflictPromptGen
// function variables are the injection point for the prompt that asks an agent
// to resolve them — set by internal/cli/agent, which owns prompt text, so that
// this package does not depend on the prompt layer.
package git
