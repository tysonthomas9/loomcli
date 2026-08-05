package agents

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// createdPromptAgentResponse refreshes the response projection after all
// prompt-agent writes have committed. A read-side outage at this point must
// not turn a successful create into a 5xx: UI clients may retry that response
// and create a second enabled agent. The committed write results are a complete
// and accurate fallback projection, while the warning keeps the degraded read
// visible to operators.
func (m *Module) createdPromptAgentResponse(
	ctx context.Context,
	ws string,
	committedRecord *domain.AgentService,
	committedBinding *automation.Binding,
	now time.Time,
) agentRecordDTO {
	record, err := m.store.AgentServices().Get(ctx, ws, committedRecord.ServiceID)
	if err != nil {
		m.logPromptAgentResponseFallback(ws, committedRecord.ServiceID, committedBinding.BindingID, "get_record", err)
		return m.agentRecordDTOWithBindings(ctx, ws, committedRecord, []*automation.Binding{committedBinding}, now)
	}
	if record == nil {
		err = errors.New("agent service read returned no record")
		m.logPromptAgentResponseFallback(ws, committedRecord.ServiceID, committedBinding.BindingID, "get_record", err)
		return m.agentRecordDTOWithBindings(ctx, ws, committedRecord, []*automation.Binding{committedBinding}, now)
	}
	if m.bindings == nil {
		err = automation.ErrUnavailable
		m.logPromptAgentResponseFallback(ws, committedRecord.ServiceID, committedBinding.BindingID, "list_bindings", err)
		return m.agentRecordDTOWithBindings(ctx, ws, record, []*automation.Binding{committedBinding}, now)
	}
	bindings, err := m.bindings.ListBindings(ctx, ws, automation.BindingFilter{
		TargetAgentServiceID: record.ServiceID,
	})
	if err != nil {
		m.logPromptAgentResponseFallback(ws, committedRecord.ServiceID, committedBinding.BindingID, "list_bindings", err)
		return m.agentRecordDTOWithBindings(ctx, ws, record, []*automation.Binding{committedBinding}, now)
	}
	return m.agentRecordDTOWithBindings(ctx, ws, record, bindings, now)
}

func (m *Module) logPromptAgentResponseFallback(ws, agentID, bindingID, stage string, err error) {
	slog.Warn("prompt agent create: post-commit response refresh failed; returning committed snapshot",
		"workspace", ws,
		"agent_id", agentID,
		"binding_id", bindingID,
		"stage", stage,
		"err", err,
	)
}
