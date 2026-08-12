package agents

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	if !m.requireAgentRuns(w) {
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "id", "name")
	limit, ok := handler.ParseRunLimit(w, r)
	if !ok {
		return
	}
	m.writeAgentRecordRuns(w, r, ws, id, limit)
}

func (m *Module) writeAgentRecordRuns(
	w http.ResponseWriter,
	r *http.Request,
	ws, id string,
	limit int,
) {
	record, err := m.agentServiceForHistory(r.Context(), ws, id)
	if err != nil {
		writeAgentRecordError(w, err, "get agent record failed")
		return
	}
	runs, err := m.runsForAgent(r.Context(), ws, record.ServiceID, limit)
	if err != nil {
		writeBindingError(w, err, "list agent runs failed")
		return
	}
	if runs == nil {
		runs = []*domain.DriverRun{}
	}
	handler.WriteJSON(w, http.StatusOK, agentRunsResponse{
		AgentID:  record.ServiceID,
		Runs:     runs,
		Sessions: []*agentHistorySessionDTO{},
	})
}

func (m *Module) runsForAgent(ctx context.Context, ws, agentID string, limit int) ([]*domain.DriverRun, error) {
	runs, err := m.listAgentServiceRunsForHistory(ctx, ws, agentID, limit)
	if err != nil {
		return nil, err
	}
	return handler.SortAndTrim(runs, limit), nil
}
