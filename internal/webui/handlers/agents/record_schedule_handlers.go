package agents

import (
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) buildAttachedSchedulePatch(
	w http.ResponseWriter,
	r *http.Request,
	ws, agentID string,
	req loomapi.PatchUnifiedAgentRequest,
) (string, automation.BindingPatch, bool) {
	hasScheduleField := req.Schedule != nil || req.ScheduleTimezone != nil
	if !hasScheduleField {
		if req.BindingId != nil {
			handler.RespondError(w, http.StatusBadRequest, "binding_id requires schedule or schedule_timezone")
			return "", automation.BindingPatch{}, false
		}
		return "", automation.BindingPatch{}, true
	}
	bindingID := ""
	if req.BindingId != nil {
		bindingID = strings.TrimSpace(*req.BindingId)
	}
	if bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding_id is required for schedule updates")
		return "", automation.BindingPatch{}, false
	}
	if !m.validateAttachedScheduleBinding(w, r, ws, agentID, bindingID) {
		return "", automation.BindingPatch{}, false
	}
	patch, ok := buildSchedulePatch(w, req)
	if !ok {
		return "", automation.BindingPatch{}, false
	}
	return bindingID, patch, true
}

func (m *Module) validateAttachedScheduleBinding(
	w http.ResponseWriter,
	r *http.Request,
	ws, agentID, bindingID string,
) bool {
	if m.bindings == nil {
		writeBindingError(w, automation.ErrUnavailable, "get attached binding failed")
		return false
	}
	binding, err := m.bindings.GetBinding(r.Context(), ws, bindingID)
	if err != nil {
		writeBindingError(w, err, "get attached binding failed")
		return false
	}
	if strings.TrimSpace(binding.TargetAgentServiceID) != agentID {
		handler.RespondError(w, http.StatusConflict, "binding is not managed by this agent")
		return false
	}
	if binding.SourceKind != automation.SourceKindCron {
		handler.RespondError(w, http.StatusBadRequest, "schedule can only be changed on a cron agent")
		return false
	}
	return true
}

func buildSchedulePatch(
	w http.ResponseWriter,
	req loomapi.PatchUnifiedAgentRequest,
) (automation.BindingPatch, bool) {
	patch := automation.BindingPatch{}
	if req.Schedule != nil {
		schedule := strings.TrimSpace(*req.Schedule)
		if err := automation.ValidateSchedule(schedule); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.Schedule = &schedule
	}
	if req.ScheduleTimezone != nil {
		timezone := strings.TrimSpace(*req.ScheduleTimezone)
		if err := automation.ValidateScheduleTimezone(timezone); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.ScheduleTimezone = &timezone
	}
	return patch, true
}
