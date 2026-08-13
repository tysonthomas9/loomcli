package git

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
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

// writeSourceControlError maps a Source Control error to its canonical HTTP
// status and public error envelope.
func writeSourceControlError(w http.ResponseWriter, err error) {
	handler.HandleSourceControlError(w, err)
}

// --- Push ---

// HandleGitPush handles POST /api/agents/{name}/git/push
// Merges the agent's worktree branch INTO the target branch (loom push semantics).
func HandleGitPush(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.GitPushRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		target := gitValueOrZero(req.Target)
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.Push(r.Context(), sourcecontrol.PushCommand{WorkspaceKey: wsID, AgentID: agentName, TargetBranch: target})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			handler.WriteJSON(w, http.StatusConflict, pushResponse(result))
			return
		}

		handler.WriteJSON(w, http.StatusOK, pushResponse(result))
	}
}

// --- Push All ---

// HandleGitPushAll handles POST /api/git/push-all
// Pushes all agent worktree branches to their target branches.
func HandleGitPushAll(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.PushAll(r.Context(), sourcecontrol.PushAllCommand{WorkspaceKey: wsID})
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		handler.WriteJSON(w, http.StatusOK, pushAllResponse(result))
	}
}

// --- Pull ---

// HandleGitPull handles POST /api/agents/{name}/git/pull
// Merges the source branch INTO the agent's worktree branch.
func HandleGitPull(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.GitPullRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		source := gitValueOrZero(req.Source)
		if source != "" && (!validGitRef.MatchString(source) || strings.Contains(source, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid source branch name")
			return
		}

		result, err := svc.Pull(r.Context(), sourcecontrol.PullCommand{WorkspaceKey: wsID, AgentID: agentName, SourceBranch: source})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}

		if !result.Success && len(result.ConflictedFiles) > 0 {
			handler.WriteJSON(w, http.StatusConflict, pullResponse(result))
			return
		}

		handler.WriteJSON(w, http.StatusOK, pullResponse(result))
	}
}

// --- Sync ---

// HandleGitSync handles POST /api/agents/{name}/git/sync
// Full push+pull cycle: first push to target, then pull from target.
func HandleGitSync(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.Sync(r.Context(), sourcecontrol.SyncCommand{WorkspaceKey: wsID, AgentID: agentName})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}

		// Push conflict: return partial result
		if result.Push != nil && !result.Push.Success && len(result.Push.ConflictedFiles) > 0 {
			handler.WriteJSON(w, http.StatusConflict, syncResponse(result))
			return
		}

		status := http.StatusOK
		if result.Pull != nil && !result.Pull.Success && len(result.Pull.ConflictedFiles) > 0 {
			status = http.StatusConflict
		}

		handler.WriteJSON(w, status, syncResponse(result))
	}
}

// --- PR ---

// HandleGitPR handles POST /api/agents/{name}/git/pr
// Creates a GitHub PR from the agent's worktree branch.
func HandleGitPR(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.GitCreatePullRequestRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		target := gitValueOrZero(req.Target)
		if target != "" && (!validGitRef.MatchString(target) || strings.Contains(target, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid target branch name")
			return
		}

		result, err := svc.CreatePullRequest(r.Context(), sourcecontrol.CreatePullRequestCommand{WorkspaceKey: wsID, AgentID: agentName, TargetBranch: target})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}

		if result.Created {
			handler.WriteJSON(w, http.StatusCreated, pullRequestCreationResponse(result))
		} else {
			handler.WriteJSON(w, http.StatusOK, pullRequestCreationResponse(result))
		}
	}
}

// --- Reset ---

// HandleGitReset handles POST /api/agents/{name}/git/reset
// Hard resets the worktree to a branch.
func HandleGitReset(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.GitResetRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}

		branch := gitValueOrZero(req.Branch)
		if branch != "" && (!validGitRef.MatchString(branch) || strings.Contains(branch, "..")) {
			handler.RespondError(w, http.StatusBadRequest, "invalid branch name")
			return
		}

		result, err := svc.Reset(r.Context(), sourcecontrol.ResetCommand{
			WorkspaceKey: wsID, AgentID: agentName, TargetBranch: branch,
			Force: gitValueOrZero(req.Force), Push: gitValueOrZero(req.Push),
		})
		if err != nil {
			var lockedErr *sourcecontrol.ResetLockedError
			if errors.As(err, &lockedErr) {
				lockInfo := loomapi.GitResetLockInfo{
					Agent: lockedErr.AgentID, Pid: lockedErr.PID, Duration: lockedErr.Age,
				}
				if lockedErr.TaskID != "" {
					lockInfo.TaskId = gitPointer(lockedErr.TaskID)
				}
				handler.WriteJSON(w, http.StatusLocked, loomapi.GitResetLockedResponse{
					Error:    "agent locked",
					LockInfo: lockInfo,
				})
				return
			}
			writeSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, resetResponse(result))
	}
}

// --- Status ---

// HandleGitStatus handles GET /api/agents/{name}/git/status
// Returns detailed git status for the agent's worktree.
func HandleGitStatus(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.AgentStatus(r.Context(), sourcecontrol.AgentStatusQuery{WorkspaceKey: wsID, AgentID: agentName})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, statusResponse(result))
	}
}

// --- Target Update ---

// HandleGitTargetUpdate handles PATCH /api/agents/{name}/git/target
// Changes the target/integration branch for a worktree.
func HandleGitTargetUpdate(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.GitTargetRequest
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

		if err := svc.SetTargetBranch(r.Context(), sourcecontrol.SetTargetBranchCommand{WorkspaceKey: wsID, AgentID: agentName, Branch: req.Branch}); err != nil {
			writeSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.GitTargetResponse{
			Success: true,
			Branch:  req.Branch,
		})
	}
}

func gitValueOrZero[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
