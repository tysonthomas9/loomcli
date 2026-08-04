package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// cleanupTimeout bounds the compensating delete after a failed lifecycle
// start. Detached from the request context so a disconnected client does
// not cause the cleanup to no-op and orphan the just-created issue.
const cleanupTimeout = 5 * time.Second

type runFirstTaskRequest struct {
	AgentName   string `json:"agent_name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	IssueType   string `json:"issue_type,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
	SourceRepo  string `json:"source_repo,omitempty"`
}

type runFirstTaskResponse struct {
	Success   bool            `json:"success"`
	Issue     json.RawMessage `json:"issue"`
	AgentName string          `json:"agent_name"`
	Started   bool            `json:"started"`
	Queued    bool            `json:"queued"`
}

type normalizedRunFirstTaskRequest struct {
	AgentName   string
	Title       string
	Description string
	IssueType   string
	Priority    int
	SourceRepo  string
}

type AgentLifecycleAPI interface {
	GetAgent(context.Context, string, string) (*agentsmodule.Agent, error)
	ApplyLifecycle(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error)
}

// HandleRunFirstTask creates the first onboarding task and enables the
// selected canonical Agent. The task remains unassigned and claimable by the
// normal Execution path; no daemon command or task payload is emitted.
func HandleRunFirstTask(
	workItems workitems.API,
	agents AgentLifecycleAPI,
	resolver workflowcataloghttp.OperatorAuthorityResolver,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := middleware.WorkspaceFromContext(r.Context())
		if ws == "" {
			ws = r.PathValue("ws")
		}
		req, err := readRunFirstTaskRequest(w, r)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		normalized, err := normalizeRunFirstTaskRequest(req)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		if agents == nil || resolver == nil {
			handler.RespondError(w, http.StatusServiceUnavailable, "canonical Agent lifecycle is unavailable")
			return
		}
		auth, err := resolver.ResolveOperatorAuthority(r, ws, agentsmodule.ActionApplyLifecycle)
		if err != nil {
			writeAgentLifecycleError(w, err, "Agent lifecycle authority denied")
			return
		}
		created, err := createAndQueueFirstTask(r.Context(), workItems, agents, auth, ws, normalized)
		if err != nil {
			writeAgentLifecycleError(w, err, "queue first task failed")
			return
		}

		handler.WriteJSON(w, http.StatusCreated, runFirstTaskResponse{
			Success:   true,
			Issue:     created,
			AgentName: normalized.AgentName,
			Started:   false,
			Queued:    true,
		})
	}
}

func readRunFirstTaskRequest(w http.ResponseWriter, r *http.Request) (runFirstTaskRequest, error) {
	var req runFirstTaskRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		return runFirstTaskRequest{}, err
	}
	return req, nil
}

func normalizeRunFirstTaskRequest(req runFirstTaskRequest) (normalizedRunFirstTaskRequest, error) {
	out := normalizedRunFirstTaskRequest{
		AgentName:   strings.TrimSpace(req.AgentName),
		Title:       strings.TrimSpace(req.Title),
		Description: strings.TrimSpace(req.Description),
		IssueType:   strings.TrimSpace(req.IssueType),
		Priority:    2,
		SourceRepo:  strings.TrimSpace(req.SourceRepo),
	}
	if out.AgentName == "" {
		return normalizedRunFirstTaskRequest{}, fmt.Errorf("agent_name is required: %w", workitems.ErrInvalid)
	}
	if out.Title == "" {
		return normalizedRunFirstTaskRequest{}, fmt.Errorf("title is required: %w", workitems.ErrInvalid)
	}
	if out.IssueType == "" {
		out.IssueType = "task"
	}
	if req.Priority != nil {
		out.Priority = *req.Priority
	}
	return out, nil
}

func createAndQueueFirstTask(
	ctx context.Context,
	workItems workitems.API,
	agents AgentLifecycleAPI,
	auth authority.OperatorAuthority,
	ws string,
	req normalizedRunFirstTaskRequest,
) (json.RawMessage, error) {
	agent, err := agents.GetAgent(ctx, ws, req.AgentName)
	if err != nil {
		return nil, err
	}
	if agent == nil || agent.DeletedAt != nil {
		return nil, agentsmodule.ErrNotFound
	}
	created, err := workItems.Create(ctx, workitems.CreateCommand{
		Title:       req.Title,
		IssueType:   req.IssueType,
		Priority:    req.Priority,
		Description: req.Description,
		Status:      "open",
		SourceRepo:  req.SourceRepo,
	})
	if err != nil {
		return nil, err
	}
	issueID, err := createdIssueID(created)
	if err != nil {
		return nil, err
	}
	if err := queueFirstTask(ctx, agents, auth, agent, issueID); err != nil {
		deleteCreatedFirstTask(workItems, issueID)
		return nil, err
	}
	raw, err := json.Marshal(created)
	if err != nil {
		return nil, fmt.Errorf("encode created issue: %w", workitems.ErrInvalidPersistedState)
	}
	return raw, nil
}

func queueFirstTask(
	ctx context.Context,
	agents AgentLifecycleAPI,
	auth authority.OperatorAuthority,
	agent *agentsmodule.Agent,
	issueID string,
) error {
	_, err := agents.ApplyLifecycle(ctx, auth, agentsmodule.ApplyLifecycleCommand{
		WorkspaceKey:         agent.WorkspaceKey,
		AgentID:              agent.AgentID,
		Action:               agentsmodule.LifecycleEnable,
		ExpectedUpdatedAt:    agent.UpdatedAt,
		ExpectedGenerationID: agent.GenerationID,
		IdempotencyKey:       "onboarding-first-task:" + issueID,
	})
	return err
}

func createdIssueID(created *workitems.CreatedIssue) (string, error) {
	if created == nil {
		return "", fmt.Errorf("created issue response was empty: %w", workitems.ErrInvalidPersistedState)
	}
	if created.Detail != nil && strings.TrimSpace(created.Detail.ID) != "" {
		return created.Detail.ID, nil
	}
	if created.Summary != nil && strings.TrimSpace(created.Summary.ID) != "" {
		return created.Summary.ID, nil
	}
	return "", fmt.Errorf("created issue response did not include an id: %w", workitems.ErrInvalidPersistedState)
}

func deleteCreatedFirstTask(workItems workitems.API, issueID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if _, err := workItems.Delete(ctx, workitems.DeleteCommand{IssueID: issueID}); err != nil {
		slog.Warn("onboarding first task cleanup failed", "issue_id", issueID, "err", err)
	}
}

func writeAgentLifecycleError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, workitems.ErrInvalid),
		errors.Is(err, workitems.ErrNotFound),
		errors.Is(err, workitems.ErrConflict),
		errors.Is(err, workitems.ErrUnavailable),
		errors.Is(err, workitems.ErrTimeout),
		errors.Is(err, workitems.ErrNotImplemented),
		errors.Is(err, workitems.ErrInvalidPersistedState):
		handler.HandleWorkItemsError(w, err)
	case errors.Is(err, workflowcataloghttp.ErrUnauthenticated),
		errors.Is(err, authority.ErrInvalidPrincipal),
		errors.Is(err, authority.ErrPrincipalExpired):
		handler.RespondError(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, authority.ErrWorkspaceMismatch),
		errors.Is(err, authority.ErrActionNotAllowed),
		errors.Is(err, authority.ErrAdmissionDenied),
		errors.Is(err, authority.ErrPrincipalClass):
		handler.RespondError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, agentsmodule.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, "agent not found")
	case errors.Is(err, agentsmodule.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentsmodule.ErrAlreadyExists),
		errors.Is(err, agentsmodule.ErrConflict),
		errors.Is(err, agentsmodule.ErrInvalidTransition),
		errors.Is(err, agentsmodule.ErrNotOwner):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agentsmodule.ErrUnavailable):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}
