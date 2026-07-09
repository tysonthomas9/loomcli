package onboarding

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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

type createdIssueRef struct {
	ID string `json:"id"`
}

type normalizedRunFirstTaskRequest struct {
	AgentName   string
	Title       string
	Description string
	IssueType   string
	Priority    int
	SourceRepo  string
}

// HandleRunFirstTask creates the first onboarding task, assigns it to the
// selected planner, and requests the agent start in one backend operation.
func HandleRunFirstTask(issueSvc service.IssueService, agentSvc service.AgentService) http.HandlerFunc {
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

		created, err := createAndQueueFirstTask(r.Context(), issueSvc, agentSvc, ws, normalized)
		if err != nil {
			handler.HandleServiceError(w, err)
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
		return normalizedRunFirstTaskRequest{}, service.ErrValidation("agent_name is required")
	}
	if out.Title == "" {
		return normalizedRunFirstTaskRequest{}, service.ErrValidation("title is required")
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
	issueSvc service.IssueService,
	agentSvc service.AgentService,
	ws string,
	req normalizedRunFirstTaskRequest,
) (json.RawMessage, error) {
	if err := ensureAgentExists(ctx, agentSvc, ws, req.AgentName); err != nil {
		return nil, err
	}
	created, err := issueSvc.CreateIssue(ctx, service.CreateIssueParams{
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
	if created == nil {
		return nil, service.ErrInternal("create issue returned no result", nil)
	}
	issueID, err := decodeIssueID(created.Data)
	if err != nil {
		return nil, err
	}
	if err := queueFirstTask(ctx, agentSvc, ws, req.AgentName, issueID); err != nil {
		deleteCreatedFirstTask(issueSvc, issueID)
		return nil, err
	}
	return created.Data, nil
}

func queueFirstTask(ctx context.Context, agentSvc service.AgentService, ws, agentName, issueID string) error {
	_, err := agentSvc.RequestAgentLifecycle(ctx, ws, agentName, service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
		Payload:      map[string]string{"task_id": issueID},
	})
	if err != nil {
		return err
	}
	return nil
}

func ensureAgentExists(ctx context.Context, agentSvc service.AgentService, ws, agentName string) error {
	agents, err := agentSvc.ListAgents(ctx, ws)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent != nil && agent.Name == agentName {
			return nil
		}
	}
	return service.ErrNotFound("agent not found")
}

func decodeIssueID(raw json.RawMessage) (string, error) {
	var created createdIssueRef
	if err := json.Unmarshal(raw, &created); err != nil {
		return "", service.ErrInternal("failed to decode created issue", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", service.ErrInternal("created issue response did not include an id", nil)
	}
	return created.ID, nil
}

func deleteCreatedFirstTask(issueSvc service.IssueService, issueID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if _, err := issueSvc.DeleteIssue(ctx, issueID); err != nil {
		slog.Warn("onboarding first task cleanup failed", "issue_id", issueID, "err", err)
	}
}
