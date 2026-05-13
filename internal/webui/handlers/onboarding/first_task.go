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

// HandleRunFirstTask creates the first onboarding task, assigns it to the
// selected planner, and requests the agent start in one backend operation.
func HandleRunFirstTask(issueSvc service.IssueService, agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := r.PathValue("ws")
		var req runFirstTaskRequest
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		agentName := strings.TrimSpace(req.AgentName)
		title := strings.TrimSpace(req.Title)
		if agentName == "" {
			handler.HandleServiceError(w, service.ErrValidation("agent_name is required"))
			return
		}
		if title == "" {
			handler.HandleServiceError(w, service.ErrValidation("title is required"))
			return
		}
		if err := ensureAgentExists(r.Context(), agentSvc, ws, agentName); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		issueType := strings.TrimSpace(req.IssueType)
		if issueType == "" {
			issueType = "task"
		}
		priority := 2
		if req.Priority != nil {
			priority = *req.Priority
		}

		created, err := issueSvc.CreateIssue(r.Context(), service.CreateIssueParams{
			Title:       title,
			IssueType:   issueType,
			Priority:    priority,
			Description: strings.TrimSpace(req.Description),
			Status:      "open",
			SourceRepo:  strings.TrimSpace(req.SourceRepo),
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		issueID, err := decodeIssueID(created)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		_, err = agentSvc.RequestAgentLifecycle(r.Context(), ws, agentName, service.AgentLifecycleInput{
			State:        domain.AgentStateActive,
			DesiredState: domain.AgentDesiredRunning,
			CommandType:  "start",
			Payload:      map[string]string{"task_id": issueID},
		})
		if err != nil {
			deleteCreatedFirstTask(issueSvc, issueID)
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusCreated, runFirstTaskResponse{
			Success:   true,
			Issue:     created,
			AgentName: agentName,
			Started:   false,
			Queued:    true,
		})
	}
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
