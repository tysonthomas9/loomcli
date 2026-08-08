package agents

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) getAgentRecord(
	ctx context.Context,
	workspace,
	agentID string,
) (*domain.AgentService, error) {
	if m == nil || m.agentRecords == nil {
		return nil, agentsmodule.ErrUnavailable
	}
	record, err := m.agentRecords.GetAgent(ctx, workspace, agentID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, agentsmodule.ErrInvalidPersistedState
	}
	if _, err := agentsmodule.ParseRuntimeMetadata(record.Metadata); err != nil {
		return nil, err
	}
	return canonicalAgentServiceProjection(record), nil
}

func (m *Module) listAgentRecords(
	ctx context.Context,
	workspace string,
	includeArchived bool,
) ([]*domain.AgentService, error) {
	if m == nil || m.agentRecords == nil {
		return nil, agentsmodule.ErrUnavailable
	}
	records, err := m.agentRecords.ListAgents(ctx, workspace, agentsmodule.AgentFilter{
		IncludeDeleted: includeArchived,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*domain.AgentService, 0, len(records))
	for _, record := range records {
		if record == nil {
			return nil, agentsmodule.ErrInvalidPersistedState
		}
		if _, err := agentsmodule.ParseRuntimeMetadata(record.Metadata); err != nil {
			return nil, err
		}
		out = append(out, canonicalAgentServiceProjection(record))
	}
	return out, nil
}

func canonicalAgentServiceProjection(record *agentsmodule.Agent) *domain.AgentService {
	if record == nil {
		return nil
	}
	return &domain.AgentService{
		WorkspaceKey:    record.WorkspaceKey,
		ServiceID:       record.AgentID,
		GenerationID:    record.GenerationID,
		Name:            record.Name,
		Kind:            domain.AgentServiceKind(record.Kind),
		DesiredState:    domain.AgentServiceDesiredState(record.DesiredState),
		RoleName:        record.Behavior.RoleName,
		DriverID:        record.Behavior.DriverID,
		DriverVersionID: record.Behavior.DriverVersionID,
		PlacementPolicy: record.PlacementPolicy,
		MaxInstances:    record.MaxInstances,
		RestartPolicy:   record.RestartPolicy,
		BudgetPolicy:    record.BudgetPolicy,
		Metadata:        cloneStringMap(record.Metadata),
		CreatedBy:       record.CreatedBy,
		DeletedAt:       cloneAgentRecordTime(record.DeletedAt),
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
	}
}

func cloneAgentRecordTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (m *Module) resolveAgentRecordAuthority(
	w http.ResponseWriter,
	r *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, bool) {
	if m == nil || m.agentRecordAuthority == nil {
		writeAgentRecordError(w, agentsmodule.ErrUnavailable, "agent record authority is unavailable")
		return authority.OperatorAuthority{}, false
	}
	auth, err := m.agentRecordAuthority.ResolveOperatorAuthority(r, workspace, action)
	if err != nil {
		writeAgentRecordError(w, err, "agent record authority denied")
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func writeAgentRecordError(w http.ResponseWriter, err error, fallback string) {
	if classification, ok := handler.ClassifyAuthenticationAuthorityError(err); ok {
		message := "authentication required"
		if classification.Status == http.StatusForbidden {
			message = "forbidden"
		}
		handler.RespondError(w, classification.Status, message)
		return
	}
	switch {
	case errors.Is(err, agentsmodule.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, agentsmodule.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentsmodule.ErrAlreadyExists),
		errors.Is(err, agentsmodule.ErrConflict),
		errors.Is(err, agentsmodule.ErrInvalidTransition),
		errors.Is(err, agentsmodule.ErrNotOwner):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case handler.IsControlPlaneRateLimited(err):
		handler.RespondError(w, http.StatusTooManyRequests, fallback)
	case handler.IsControlPlaneUnavailable(err):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	case errors.Is(err, agentsmodule.ErrUnavailable):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}

func writeUnifiedAgentLookupError(w http.ResponseWriter, err error) {
	const fallback = "get agent failed"
	switch {
	case errors.Is(err, agentsmodule.ErrInvalid),
		errors.Is(err, agentsmodule.ErrNotFound),
		errors.Is(err, agentsmodule.ErrAlreadyExists),
		errors.Is(err, agentsmodule.ErrConflict),
		errors.Is(err, agentsmodule.ErrNotOwner),
		errors.Is(err, agentsmodule.ErrInvalidTransition),
		errors.Is(err, agentsmodule.ErrUnavailable),
		errors.Is(err, agentsmodule.ErrInvalidPersistedState):
		writeAgentRecordError(w, err, fallback)
	default:
		writeBindingError(w, err, fallback)
	}
}
