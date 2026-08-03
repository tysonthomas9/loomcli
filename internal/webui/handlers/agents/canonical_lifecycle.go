package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func (m *Module) handleCanonicalLifecycle(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, ok := m.resolveCanonicalLifecycleTarget(w, r, operation)
		if !ok {
			return
		}
		m.applyCanonicalLifecycle(w, r, target)
	}
}

type canonicalLifecycleTarget struct {
	workspace    string
	agentID      string
	kind         string
	action       agentsmodule.LifecycleAction
	operation    string
	pastTense    string
	updatedAt    time.Time
	generationID string
}

func (m *Module) resolveCanonicalLifecycleTarget(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
) (canonicalLifecycleTarget, bool) {
	var target canonicalLifecycleTarget
	if !validCanonicalLifecycleRequest(r) {
		writeAgentValidationError(w, "invalid request body")
		return target, false
	}
	workspace, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return target, false
	}
	agentID := agentRouteValue(r, "name", "idOrName")
	record, err := m.getAgentRecord(r.Context(), workspace, agentID)
	if err != nil {
		writeAgentRecordError(w, err, "get lifecycle Agent failed")
		return target, false
	}
	kind, err := m.canonicalAgentRecordKind(r.Context(), workspace, record)
	if err != nil {
		writeAgentRecordError(w, err, "resolve lifecycle Agent kind failed")
		return target, false
	}
	if m.agentLifecycle == nil {
		writeAgentRecordError(w, agentsmodule.ErrUnavailable, "agent lifecycle is unavailable")
		return target, false
	}

	action, pastTense, valid := canonicalLifecycleOperation(operation)
	if !valid {
		writeAgentValidationError(w, "unsupported lifecycle operation")
		return target, false
	}
	return canonicalLifecycleTarget{
		workspace: workspace, agentID: agentID, kind: kind, action: action,
		operation: operation, pastTense: pastTense,
		updatedAt: record.UpdatedAt, generationID: record.GenerationID,
	}, true
}

func (m *Module) applyCanonicalLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	target canonicalLifecycleTarget,
) {
	stopsInteractive := canonicalLifecycleStopsInteractive(target.kind, target.operation)
	unlock := func() {}
	if stopsInteractive {
		unlock = terminal.LockAgentLifecycle(target.workspace, target.agentID)
	}
	defer unlock()
	auth, ok := m.resolveAgentRecordAuthority(w, r, target.workspace, agentsmodule.ActionApplyLifecycle)
	if !ok {
		return
	}
	result, err := m.agentLifecycle.ApplyLifecycle(r.Context(), auth, agentsmodule.ApplyLifecycleCommand{
		WorkspaceKey: target.workspace, AgentID: target.agentID, Action: target.action,
		ExpectedUpdatedAt: target.updatedAt, ExpectedGenerationID: target.generationID,
		IdempotencyKey: agentLifecycleIdempotencyKey(target.workspace, target.agentID, target.action, target.updatedAt),
	})
	if err != nil {
		writeAgentRecordError(w, err, "apply Agent lifecycle failed")
		return
	}
	if result == nil || result.Agent == nil {
		writeAgentRecordError(w, agentsmodule.ErrInvalidPersistedState, "Agent lifecycle returned no identity")
		return
	}
	if stopsInteractive {
		if m.interactiveRuntime == nil {
			writeAgentRecordError(w, agentsmodule.ErrUnavailable, "interactive runtime lifecycle is unavailable")
			return
		}
		if err := m.interactiveRuntime.StopAgent(r.Context(), target.workspace, target.agentID); err != nil {
			writeAgentInternalError(w, "stop interactive runtime", err)
			return
		}
	}
	broadcastAgentRefresh(m.hub, target.workspace, target.agentID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, dto.AgentLifecycleResponse{
		Message: fmt.Sprintf("agent %q %s", target.agentID, target.pastTense),
		Status:  "succeeded",
	})
}

func validCanonicalLifecycleRequest(r *http.Request) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request lifecycleRequest
	return decoder.Decode(&request) == nil && !decoder.More()
}

func canonicalLifecycleOperation(operation string) (agentsmodule.LifecycleAction, string, bool) {
	switch strings.TrimSpace(operation) {
	case "start":
		return agentsmodule.LifecycleEnable, "started", true
	case "stop":
		return agentsmodule.LifecycleDisable, "stopped", true
	case "restart":
		return agentsmodule.LifecycleEnable, "restarted", true
	default:
		return "", "", false
	}
}

func canonicalLifecycleStopsInteractive(kind, operation string) bool {
	operation = strings.TrimSpace(operation)
	return kind == agentRecordKindInteractive && (operation == "stop" || operation == "restart")
}
