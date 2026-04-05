package webui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// WorkspaceAgentStatus represents an agent's status within a workspace.
type WorkspaceAgentStatus struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Status string `json:"status"` // "ready", "N changes"
	Repo   string `json:"repo,omitempty"`
}

// handleListWorkspaceAgents returns agent status for a specific workspace
// by scanning its worktrees directory for git worktrees.
func handleListWorkspaceAgents(
	workspaceConfigByIDFn func(string) (*WorkspaceData, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := r.PathValue("ws")
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace ID required"})
			return
		}

		if workspaceConfigByIDFn == nil {
			respondJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
			return
		}

		wsData, err := workspaceConfigByIDFn(wsID)
		if err != nil || wsData == nil {
			respondJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
			return
		}

		agents := discoverWorkspaceAgents(wsData.Path)
		respondJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// discoverWorkspaceAgents scans <wsPath>/worktrees/<repo>/<agent> for git worktrees.
// Worktrees are processed in parallel to reduce latency.
func discoverWorkspaceAgents(wsPath string) []WorkspaceAgentStatus {
	worktreesDir := filepath.Join(wsPath, "worktrees")
	if _, err := os.Stat(worktreesDir); err != nil {
		return nil
	}

	// First pass: collect all candidate agent paths (cheap OS reads).
	type candidate struct {
		agentName string
		agentPath string
		repoName  string
	}
	var candidates []candidate

	// Iterate repos under worktrees/
	repoEntries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return nil
	}
	for _, repoEntry := range repoEntries {
		if !repoEntry.IsDir() {
			continue
		}
		repoName := repoEntry.Name()
		repoWorktrees := filepath.Join(worktreesDir, repoName)

		// Iterate agents under worktrees/<repo>/
		agentEntries, err := os.ReadDir(repoWorktrees)
		if err != nil {
			continue
		}
		for _, agentEntry := range agentEntries {
			if !agentEntry.IsDir() {
				continue
			}
			agentPath := filepath.Join(repoWorktrees, agentEntry.Name())

			// Verify it's a git worktree (.git file or dir)
			if _, err := os.Stat(filepath.Join(agentPath, ".git")); err != nil {
				continue
			}

			candidates = append(candidates, candidate{
				agentName: agentEntry.Name(),
				agentPath: agentPath,
				repoName:  repoName,
			})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Second pass: fan out git calls in parallel.
	agents := make([]WorkspaceAgentStatus, len(candidates))
	var wg sync.WaitGroup
	wg.Add(len(candidates))

	for i, c := range candidates {
		go func(idx int, cand candidate) {
			defer wg.Done()
			branch, status := getGitBranchAndStatus(cand.agentPath)
			agents[idx] = WorkspaceAgentStatus{
				Name:   cand.agentName,
				Branch: branch,
				Status: status,
				Repo:   cand.repoName,
			}
		}(i, c)
	}

	wg.Wait()
	return agents
}

// getGitBranchAndStatus runs a single "git status --branch --porcelain" and
// parses both the branch name and the working-tree status from the output.
func getGitBranchAndStatus(path string) (branch string, status string) {
	cmd := exec.Command("git", "status", "--branch", "--porcelain") //nolint:gosec // path is workspace-internal
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "unknown", "error"
	}

	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) == 0 {
		return "unknown", "ready"
	}

	// Parse branch from first line: "## main...origin/main" or "## main"
	branchLine := lines[0]
	branch = "unknown"
	if strings.HasPrefix(branchLine, "## ") {
		branch = strings.TrimPrefix(branchLine, "## ")
		if idx := strings.Index(branch, "..."); idx >= 0 {
			branch = branch[:idx]
		}
	}

	// Count changes (remaining lines)
	changeCount := len(lines) - 1
	if changeCount == 0 {
		return branch, "ready"
	}
	if changeCount == 1 {
		return branch, "1 change"
	}
	return branch, fmt.Sprintf("%d changes", changeCount)
}

// getGitBranch returns the current branch for a git directory.
func getGitBranch(path string) string {
	cmd := exec.Command("git", "branch", "--show-current") //nolint:gosec // path is workspace-internal
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// getGitStatus returns a short status string for a git worktree.
func getGitStatus(path string) string {
	cmd := exec.Command("git", "status", "--porcelain") //nolint:gosec // path is workspace-internal
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	lines := strings.TrimSpace(string(out))
	if lines == "" {
		return "ready"
	}
	count := len(strings.Split(lines, "\n"))
	if count == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", count)
}
