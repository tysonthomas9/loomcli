package workflows

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

var validTaskWorkflowRunID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type taskWorkflowRunsResponse struct {
	TaskID     string              `json:"task_id"`
	SubjectRef string              `json:"subject_ref"`
	Runs       []*domain.DriverRun `json:"runs"`
}

// listTaskWorkflowRuns is the HTTP adapter for the task workflow-run read
// projection. Cross-capability joins and session-aware dedupe stay behind the
// injected read-only port.
func (m *Module) listTaskWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspace == "" {
		// Direct unit handlers do not install workspace resolution middleware.
		workspace = strings.TrimSpace(r.PathValue("ws"))
	}
	taskID := r.PathValue("taskId")
	if workspace == "" || taskID == "" || taskID != strings.TrimSpace(taskID) || !validTaskWorkflowRunID.MatchString(taskID) {
		writeError(w, http.StatusBadRequest, "workspace and valid task id are required")
		return
	}
	if m.taskWorkflowRuns == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow run history is unavailable")
		return
	}
	projection, err := m.taskWorkflowRuns.ListTaskWorkflowRuns(r.Context(), readprojection.TaskWorkflowRunQuery{
		WorkspaceKey: workspace,
		TaskID:       taskID,
		Limit:        defaultRunsLimit,
	})
	if err != nil {
		writeDomainError(w, err, "list task workflow runs failed")
		return
	}
	runs := projection.Runs
	if runs == nil {
		runs = []*domain.DriverRun{}
	}
	handler.WriteJSON(w, http.StatusOK, taskWorkflowRunsResponse{
		TaskID: taskID, SubjectRef: projection.SubjectRef, Runs: runs,
	})
}
