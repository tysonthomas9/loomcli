package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
// Rejects names starting with '-' or containing '..'.
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// AgentResolver is satisfied by any interface that can resolve agent worktrees.
// Both GitOps and FileOps implement this, allowing resolveAgent to be shared.
type AgentResolver interface {
	ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error)
}

// resolveAgent validates the agent name from the path and resolves it via the given resolver.
// It extracts the workspace ID from the request context (set by WorkspaceMiddleware).
func resolveAgent(w http.ResponseWriter, r *http.Request, ops AgentResolver) (*AgentWorktree, bool) {
	agentName := r.PathValue("name")
	if agentName == "" {
		respondError(w, http.StatusBadRequest, "missing agent name")
		return nil, false
	}
	if !validAgentName.MatchString(agentName) {
		respondError(w, http.StatusBadRequest, "invalid agent name: must match [a-zA-Z0-9_-]+")
		return nil, false
	}

	wsID := WorkspaceFromContext(r.Context())
	wt, err := ops.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		respondError(w, http.StatusNotFound, fmt.Sprintf("agent worktree %q not found", agentName))
		return nil, false
	}
	return wt, true
}

// --- Push ---

type gitPushRequest struct {
	Target string `json:"target"`
}

// handleGitPush handles POST /api/agents/{name}/git/push
// Merges the agent's worktree branch INTO the target branch (loom push semantics).
func handleGitPush(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		var req gitPushRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		target := req.Target
		if target == "" {
			target = wt.DefaultBranch
		}
		if !validGitRef.MatchString(target) || strings.Contains(target, "..") {
			respondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := ops.Push(wt.Path, wt.Branch, target, wt.Remote)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			respondJSON(w, http.StatusConflict, result)
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// --- Push All ---

type pushAllWorktreeResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type pushAllResponse struct {
	Results []pushAllWorktreeResult `json:"results"`
	Pushed  int                     `json:"pushed"`
	Failed  int                     `json:"failed"`
}

// handleGitPushAll handles POST /api/git/push-all
// Pushes all agent worktree branches to their target branches.
func handleGitPushAll(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := WorkspaceFromContext(r.Context())
		worktrees, err := ops.ListAgentWorktrees(wsID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("listing worktrees: %v", err))
			return
		}

		var results []pushAllWorktreeResult
		pushed, failed := 0, 0

		for _, wt := range worktrees {
			remote := wt.Remote
			if remote == "" {
				remote = "origin"
			}
			result, pushErr := ops.Push(wt.Path, wt.Branch, wt.DefaultBranch, remote)
			if pushErr != nil {
				results = append(results, pushAllWorktreeResult{
					Name:  wt.Name,
					Error: pushErr.Error(),
				})
				failed++
				continue
			}
			if result.AlreadyUpToDate {
				results = append(results, pushAllWorktreeResult{
					Name:    wt.Name,
					Success: true,
					Message: "already up to date",
				})
				continue
			}
			if !result.Success {
				results = append(results, pushAllWorktreeResult{
					Name:  wt.Name,
					Error: result.Message,
				})
				failed++
				continue
			}
			results = append(results, pushAllWorktreeResult{
				Name:    wt.Name,
				Success: true,
				Message: result.Message,
			})
			pushed++
		}

		respondJSON(w, http.StatusOK, pushAllResponse{
			Results: results,
			Pushed:  pushed,
			Failed:  failed,
		})
	}
}

// --- Pull ---

type gitPullRequest struct {
	Source string `json:"source"`
}

// handleGitPull handles POST /api/agents/{name}/git/pull
// Merges the source branch INTO the agent's worktree branch.
func handleGitPull(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		var req gitPullRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		source := req.Source
		if source == "" {
			source = wt.DefaultBranch
		}
		if !validGitRef.MatchString(source) || strings.Contains(source, "..") {
			respondError(w, http.StatusBadRequest, "invalid source branch name")
			return
		}

		currentBranch, err := ops.GetCurrentBranch(wt.Path)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("getting current branch: %v", err))
			return
		}

		result, err := ops.Pull(wt.Path, currentBranch, source, wt.Remote)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			respondJSON(w, http.StatusConflict, result)
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// --- Sync ---

type gitSyncResponse struct {
	PushResult *GitPushResult `json:"push_result"`
	PullResult *GitPullResult `json:"pull_result"`
}

// handleGitSync handles POST /api/agents/{name}/git/sync
// Full push+pull cycle: first push to target, then pull from target.
func handleGitSync(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		target := wt.DefaultBranch

		pushResult, err := ops.Push(wt.Path, wt.Branch, target, wt.Remote)
		if err != nil {
			respondError(w, http.StatusBadGateway, fmt.Sprintf("push failed: %v", err))
			return
		}

		if !pushResult.Success && len(pushResult.ConflictedFiles) > 0 {
			respondJSON(w, http.StatusConflict, gitSyncResponse{PushResult: pushResult})
			return
		}

		currentBranch, err := ops.GetCurrentBranch(wt.Path)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("getting current branch: %v", err))
			return
		}

		pullResult, err := ops.Pull(wt.Path, currentBranch, target, wt.Remote)
		if err != nil {
			respondError(w, http.StatusBadGateway, fmt.Sprintf("pull failed: %v", err))
			return
		}

		status := http.StatusOK
		if !pullResult.Success && len(pullResult.ConflictedFiles) > 0 {
			status = http.StatusConflict
		}

		respondJSON(w, status, gitSyncResponse{
			PushResult: pushResult,
			PullResult: pullResult,
		})
	}
}

// --- PR ---

type gitPRRequest struct {
	Target string `json:"target"`
}

// handleGitPR handles POST /api/agents/{name}/git/pr
// Creates a GitHub PR from the agent's worktree branch.
func handleGitPR(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		if err := ops.CheckGhInstalled(); err != nil {
			respondError(w, http.StatusServiceUnavailable, "gh CLI not installed: install from https://cli.github.com/ and run 'gh auth login'")
			return
		}

		var req gitPRRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		target := req.Target
		if target == "" {
			target = wt.DefaultBranch
		}
		if !validGitRef.MatchString(target) || strings.Contains(target, "..") {
			respondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := ops.CreatePR(wt.Path, wt.Branch, target, wt.Remote)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		if result.Created {
			respondJSON(w, http.StatusCreated, result)
		} else {
			respondJSON(w, http.StatusOK, result)
		}
	}
}

// --- Reset ---

type gitResetRequest struct {
	Branch string `json:"branch"`
	Force  bool   `json:"force"`
	Push   bool   `json:"push"`
}

type lockedResponse struct {
	Error    string       `json:"error"`
	LockInfo lockInfoResp `json:"lock_info"`
}

type lockInfoResp struct {
	Agent    string `json:"agent"`
	PID      int    `json:"pid"`
	Duration string `json:"duration"`
	TaskID   string `json:"task_id,omitempty"`
}

// handleGitReset handles POST /api/agents/{name}/git/reset
// Hard resets the worktree to a branch.
func handleGitReset(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		var req gitResetRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		target := req.Branch
		if target == "" {
			target = wt.DefaultBranch
		}
		if !validGitRef.MatchString(target) || strings.Contains(target, "..") {
			respondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		result, err := ops.Reset(wt.Path, wt.Name, target, req.Force, req.Push)
		if err != nil {
			var lockedErr *GitResetLockedError
			if errors.As(err, &lockedErr) {
				respondJSON(w, http.StatusLocked, lockedResponse{
					Error: "agent locked",
					LockInfo: lockInfoResp{
						Agent:    lockedErr.AgentName,
						PID:      lockedErr.PID,
						Duration: lockedErr.Duration,
						TaskID:   lockedErr.TaskID,
					},
				})
				return
			}
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// --- Status ---

// handleGitStatus handles GET /api/agents/{name}/git/status
// Returns detailed git status for the agent's worktree.
func handleGitStatus(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		summary, err := ops.Status(wt.Path, wt.DefaultBranch)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("getting git status: %v", err))
			return
		}

		respondJSON(w, http.StatusOK, summary)
	}
}

// --- Target Update ---

type gitTargetRequest struct {
	Branch string `json:"branch"`
}

type gitTargetResponse struct {
	Success bool   `json:"success"`
	Branch  string `json:"branch"`
}

// handleGitTargetUpdate handles PATCH /api/agents/{name}/git/target
// Changes the target/integration branch for a worktree.
func handleGitTargetUpdate(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		var req gitTargetRequest
		if r.Body != nil {
			defer r.Body.Close()
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
				respondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		if req.Branch == "" {
			respondError(w, http.StatusBadRequest, "branch is required")
			return
		}

		if !validGitRef.MatchString(req.Branch) || strings.Contains(req.Branch, "..") {
			respondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		if !wt.IsWorkspace {
			respondError(w, http.StatusBadRequest, "target branch update only supported in workspace mode")
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		if err := ops.SetRepoDefaultBranch(wsID, wt.RepoName, req.Branch); err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("updating target branch: %v", err))
			return
		}

		respondJSON(w, http.StatusOK, gitTargetResponse{
			Success: true,
			Branch:  req.Branch,
		})
	}
}
