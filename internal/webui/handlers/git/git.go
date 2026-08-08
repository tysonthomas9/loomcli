package git

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
// Rejects names starting with '-' or containing '..'.
// Keep in sync with internal/cli/git.go:gitRefPattern
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

func decodeOptionalRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	defer r.Body.Close()
	err := handler.DecodeOneJSON(w, r, dst, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody})
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	handler.RespondError(w, http.StatusBadRequest, "invalid request body")
	return false
}

// writeAgentGitError maps a service error to an HTTP response for agent git handlers.
// ServiceErrors use HandleServiceError; other errors use the given fallback status.
func writeAgentGitError(w http.ResponseWriter, err error, fallbackStatus int) {
	var svcErr *apperrors.ServiceError
	if errors.As(err, &svcErr) {
		handler.HandleServiceError(w, err)
		return
	}
	handler.RespondError(w, fallbackStatus, err.Error())
}

// --- Push ---

type gitPushRequest struct {
	Target string `json:"target"`
}

// HandleGitPush handles POST /api/agents/{name}/git/push
// Merges the agent's worktree branch INTO the target branch (loom push semantics).
func HandleGitPush(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPushRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		target := req.Target
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.GitPush(r.Context(), wsID, agentName, target)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			handler.WriteJSON(w, http.StatusConflict, result)
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// --- Push All ---

// HandleGitPushAll handles POST /api/git/push-all
// Pushes all agent worktree branches to their target branches.
func HandleGitPushAll(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GitPushAll(r.Context(), wsID)
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// --- Pull ---

type gitPullRequest struct {
	Source string `json:"source"`
}

// HandleGitPull handles POST /api/agents/{name}/git/pull
// Merges the source branch INTO the agent's worktree branch.
func HandleGitPull(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPullRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		source := req.Source
		if source != "" && (!validGitRef.MatchString(source) || strings.Contains(source, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid source branch name")
			return
		}

		result, err := svc.GitPull(r.Context(), wsID, agentName, source)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			handler.WriteJSON(w, http.StatusConflict, result)
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// --- Sync ---

// HandleGitSync handles POST /api/agents/{name}/git/sync
// Full push+pull cycle: first push to target, then pull from target.
func HandleGitSync(svc agentcoord.AgentService) http.HandlerFunc {
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
			handler.WriteJSON(w, http.StatusConflict, result)
			return
		}

		status := http.StatusOK
		if result.PullResult != nil && !result.PullResult.Success && len(result.PullResult.ConflictedFiles) > 0 {
			status = http.StatusConflict
		}

		handler.WriteJSON(w, status, result)
	}
}

// --- PR ---

type gitPRRequest struct {
	Target string `json:"target"`
}

// HandleGitPR handles POST /api/agents/{name}/git/pr
// Creates a GitHub PR from the agent's worktree branch.
func HandleGitPR(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitPRRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		target := req.Target
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.CreatePR(r.Context(), wsID, agentName, target)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}

		if result.Created {
			handler.WriteJSON(w, http.StatusCreated, result)
		} else {
			handler.WriteJSON(w, http.StatusOK, result)
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

// HandleGitReset handles POST /api/agents/{name}/git/reset
// Hard resets the worktree to a branch.
func HandleGitReset(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitResetRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		branch := req.Branch
		if branch != "" && (!validGitRef.MatchString(branch) || strings.Contains(branch, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		result, err := svc.GitReset(r.Context(), wsID, agentName, branch, req.Force, req.Push)
		if err != nil {
			var lockedErr *ops.GitResetLockedError
			if errors.As(err, &lockedErr) {
				handler.WriteJSON(w, http.StatusLocked, lockedResponse{
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

		handler.WriteJSON(w, http.StatusOK, result)
	}
}

// --- Status ---

// HandleGitStatus handles GET /api/agents/{name}/git/status
// Returns detailed git status for the agent's worktree.
func HandleGitStatus(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GitStatus(r.Context(), wsID, agentName)
		if err != nil {
			writeAgentGitError(w, err, http.StatusInternalServerError)
			return
		}

		handler.WriteJSON(w, http.StatusOK, result)
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

// HandleGitTargetUpdate handles PATCH /api/agents/{name}/git/target
// Changes the target/integration branch for a worktree.
func HandleGitTargetUpdate(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req gitTargetRequest
		if r.Body != nil {
			defer r.Body.Close()
			if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
				handler.RespondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		if req.Branch == "" {
			handler.RespondError(w, http.StatusBadRequest, "branch is required")
			return
		}

		if !validGitRef.MatchString(req.Branch) || strings.Contains(req.Branch, "..") {
			handler.RespondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		if err := svc.SetTargetBranch(r.Context(), wsID, agentName, req.Branch); err != nil {
			writeAgentGitError(w, err, http.StatusInternalServerError)
			return
		}

		handler.WriteJSON(w, http.StatusOK, gitTargetResponse{
			Success: true,
			Branch:  req.Branch,
		})
	}
}
