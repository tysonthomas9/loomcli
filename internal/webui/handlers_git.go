package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
// Rejects names starting with '-' or containing '..'.
// Keep in sync with internal/cli/git.go:gitRefPattern
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// writeAgentGitError maps a service error to an HTTP response for agent git handlers.
// ServiceErrors use WriteServiceError; other errors use the given fallback status.
func writeAgentGitError(w http.ResponseWriter, err error, fallbackStatus int) {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		WriteServiceError(w, err)
		return
	}
	respondError(w, fallbackStatus, err.Error())
}

// --- Push ---

type gitPushRequest struct {
	Target string `json:"target"`
}

// handleGitPush handles POST /api/agents/{name}/git/push
// Merges the agent's worktree branch INTO the target branch (loom push semantics).
func handleGitPush(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPushRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		target := req.Target
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			respondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.GitPush(r.Context(), wsID, agentName, target)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
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

// handleGitPushAll handles POST /api/git/push-all
// Pushes all agent worktree branches to their target branches.
func handleGitPushAll(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GitPushAll(r.Context(), wsID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// --- Pull ---

type gitPullRequest struct {
	Source string `json:"source"`
}

// handleGitPull handles POST /api/agents/{name}/git/pull
// Merges the source branch INTO the agent's worktree branch.
func handleGitPull(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPullRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		source := req.Source
		if source != "" && (!validGitRef.MatchString(source) || strings.Contains(source, "..")) {
			respondError(w, http.StatusBadRequest, "invalid source branch name")
			return
		}

		result, err := svc.GitPull(r.Context(), wsID, agentName, source)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
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

// handleGitSync handles POST /api/agents/{name}/git/sync
// Full push+pull cycle: first push to target, then pull from target.
func handleGitSync(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GitSync(r.Context(), wsID, agentName)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}

		// Push conflict: return partial result
		if result.PushResult != nil && !result.PushResult.Success && len(result.PushResult.ConflictedFiles) > 0 {
			respondJSON(w, http.StatusConflict, result)
			return
		}

		status := http.StatusOK
		if result.PullResult != nil && !result.PullResult.Success && len(result.PullResult.ConflictedFiles) > 0 {
			status = http.StatusConflict
		}

		respondJSON(w, status, result)
	}
}

// --- PR ---

type gitPRRequest struct {
	Target string `json:"target"`
}

// handleGitPR handles POST /api/agents/{name}/git/pr
// Creates a GitHub PR from the agent's worktree branch.
func handleGitPR(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPRRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		target := req.Target
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			respondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.CreatePR(r.Context(), wsID, agentName, target)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
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
func handleGitReset(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitResetRequest
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		}

		branch := req.Branch
		if branch != "" && (!validGitRef.MatchString(branch) || strings.Contains(branch, "..")) {
			respondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		result, err := svc.GitReset(r.Context(), wsID, agentName, branch, req.Force, req.Push)
		if err != nil {
			var lockedErr *ops.GitResetLockedError
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
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// --- Status ---

// handleGitStatus handles GET /api/agents/{name}/git/status
// Returns detailed git status for the agent's worktree.
func handleGitStatus(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GitStatus(r.Context(), wsID, agentName)
		if err != nil {
			writeAgentGitError(w, err, http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusOK, result)
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
func handleGitTargetUpdate(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

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

		if err := svc.SetTargetBranch(r.Context(), wsID, agentName, req.Branch); err != nil {
			writeAgentGitError(w, err, http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusOK, gitTargetResponse{
			Success: true,
			Branch:  req.Branch,
		})
	}
}
