package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/runhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	if !m.requireAgentRunsStore(w) {
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "id", "name")
	limit, ok := runhistory.ParseRunLimit(w, r)
	if !ok {
		return
	}
	if m.writeSupervisedAgentRuns(w, r, ws, id, limit) {
		return
	}
	m.writeRecordOrLegacyAgentRuns(w, r, ws, id, limit)
}

func (m *Module) writeSupervisedAgentRuns(
	w http.ResponseWriter,
	r *http.Request,
	ws, id string,
	limit int,
) bool {
	agent, supervised, err := m.supervisedByName(r.Context(), ws, id)
	if err != nil {
		writeUnifiedAgentLookupError(w, err)
		return true
	}
	if !supervised {
		return false
	}
	sessions, err := m.supervisedExecutionHistory(r.Context(), ws, agent.Name, limit)
	if err != nil {
		writeBindingError(w, err, "list supervised agent execution history failed")
		return true
	}
	if sessions == nil {
		sessions = []*agentHistorySessionDTO{}
	}
	handler.WriteJSON(w, http.StatusOK, agentRunsResponse{
		AgentID:  agent.Name,
		Runs:     []*domain.DriverRun{},
		Sessions: sessions,
	})
	return true
}

func (m *Module) writeRecordOrLegacyAgentRuns(
	w http.ResponseWriter,
	r *http.Request,
	ws, id string,
	limit int,
) {
	record, err := m.agentServiceForHistory(r.Context(), ws, id)
	if err != nil {
		if !errors.Is(err, agentsmodule.ErrNotFound) {
			writeAgentRecordError(w, err, "get agent record failed")
			return
		}
		m.writeLegacyBindingAgentRuns(w, r, ws, id, limit)
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

func (m *Module) writeLegacyBindingAgentRuns(
	w http.ResponseWriter,
	r *http.Request,
	ws, id string,
	limit int,
) {
	binding, found, err := m.unattachedBindingByID(r.Context(), ws, id)
	if err != nil {
		writeBindingError(w, err, "get legacy binding agent failed")
		return
	}
	if !found {
		handler.WriteDomainError(w, domain.ErrNotFound, "get agent record failed")
		return
	}
	runs, err := m.listBindingRunsForHistory(r.Context(), ws, binding.BindingID, limit)
	if err != nil {
		writeBindingError(w, err, "list legacy binding agent runs failed")
		return
	}
	if runs == nil {
		runs = []*domain.DriverRun{}
	}
	handler.WriteJSON(w, http.StatusOK, agentRunsResponse{
		AgentID:  binding.BindingID,
		Runs:     runhistory.SortAndTrim(runs, limit),
		Sessions: []*agentHistorySessionDTO{},
	})
}

func (m *Module) runsForAgent(ctx context.Context, ws, agentID string, limit int) ([]*domain.DriverRun, error) {
	runs, err := m.listAgentServiceRunsForHistory(ctx, ws, agentID, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		if run != nil {
			seen[run.RunID] = struct{}{}
		}
	}

	// Compatibility for runs created before fleet-db stamped agent_service_id:
	// while their binding remains attached, retain the historical binding join.
	if m.bindings == nil {
		return nil, automation.ErrUnavailable
	}
	bindings, err := m.bindings.ListBindings(ctx, ws, automation.BindingFilter{TargetAgentServiceID: agentID})
	if err != nil {
		return nil, err
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		bindingRuns, err := m.listBindingRunsForHistory(ctx, ws, b.BindingID, limit)
		if err != nil {
			return nil, err
		}
		for _, run := range bindingRuns {
			if run == nil {
				continue
			}
			if _, ok := seen[run.RunID]; ok {
				continue
			}
			seen[run.RunID] = struct{}{}
			runs = append(runs, run)
		}
	}
	return runhistory.SortAndTrim(runs, limit), nil
}
