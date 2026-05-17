package epics

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

var codexDeliveryRetryLocks sync.Map

// runEpicRequest is the JSON payload for POST /epics/{id}/run.
type runEpicRequest struct {
	Lead string `json:"lead"`
}

type runEpicResponse struct {
	EpicID                string                      `json:"epic_id"`
	LeadName              string                      `json:"lead_name,omitempty"`
	OrchestratorSessionID string                      `json:"orchestrator_session_id,omitempty"`
	State                 string                      `json:"state"`
	DeliveryState         string                      `json:"delivery_state,omitempty"`
	RunState              string                      `json:"run_state"`
	Reconcile             epicrunner.ReconcileResult  `json:"reconcile"`
	Dispatched            []epicrunner.DispatchedTask `json:"dispatched,omitempty"`
}

// handleRunEpic starts or resumes a lead-owned epic run and performs the same
// first reconciliation pass as `loom epic run`.
func handleRunEpic(m *Module) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsKey := r.PathValue("ws")
		epicID := r.PathValue("id")
		if epicID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("epic id required", "bad_request"))
			return
		}

		req, ok := readRunEpicRequest(w, r)
		if !ok {
			return
		}
		runner, result, err := newHTTPRunner(r.Context(), m, wsKey, epicID, req.Lead)
		if err != nil {
			writeEpicRunnerError(w, err)
			return
		}
		if result == nil {
			handler.WriteJSON(w, http.StatusInternalServerError, dto.NewErrorResponse("run epic returned no result", "internal"))
			return
		}

		deliveryState := deliverHTTPLeadAssignment(r.Context(), m, wsKey, result, r.Header.Get("X-Actor"))
		reconcile, runState, err := reconcileHTTPRun(r.Context(), m, wsKey, epicID, runner)
		if err != nil {
			writeEpicRunnerError(w, err)
			return
		}
		broadcastRunRefresh(m.hub, wsKey, result.LeadName, reconcile, r.Header.Get("X-Actor"))
		writeRunEpicResponse(w, result, reconcile, runState, deliveryState)
	}
}

func readRunEpicRequest(w http.ResponseWriter, r *http.Request) (runEpicRequest, bool) {
	var req runEpicRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("invalid request body", "bad_request"))
		return req, false
	}
	req.Lead = strings.TrimSpace(req.Lead)
	if req.Lead == "" {
		handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("lead required", "bad_request"))
		return req, false
	}
	return req, true
}

func newHTTPRunner(ctx context.Context, m *Module, wsKey, epicID, lead string) (*epicrunner.Runner, *epicrunner.StartResult, error) {
	if m.issueBackendFn == nil {
		return nil, nil, &epicrunner.Error{Kind: epicrunner.ErrorKindUnavailable, Msg: "issue backend not configured"}
	}
	ib := m.issueBackendFn(ctx)
	if ib == nil {
		return nil, nil, &epicrunner.Error{Kind: epicrunner.ErrorKindUnavailable, Msg: "issue backend not available"}
	}
	return epicrunner.NewRunner(ctx, epicrunner.RunnerConfig{
		Store:               m.store,
		IssueBackend:        ib,
		WorkspaceKey:        wsKey,
		EpicID:              epicID,
		LeadName:            lead,
		MutateLead:          true,
		RequireCommandStore: true,
		RequireRepos:        true,
		ValidateEpic:        true,
		FailOnDispatchError: true,
		PrepareWorktrees:    true,
		Interval:            m.runInterval,
	})
}

func reconcileHTTPRun(ctx context.Context, m *Module, wsKey, epicID string, runner *epicrunner.Runner) (epicrunner.ReconcileResult, string, error) {
	finishRun, started := m.runs.tryStart(runKey(wsKey, epicID))
	if !started {
		return epicrunner.ReconcileResult{}, "already_running", nil
	}
	reconcile, err := runner.ReconcileOnce(ctx)
	if err != nil {
		finishRun()
		return reconcile, "", err
	}
	if reconcile.Done {
		finishRun()
		return reconcile, "drained", nil
	}
	if reconcile.Blocked {
		finishRun()
		return reconcile, "blocked", nil
	}
	if m.backgroundRuns {
		startBackgroundRun(runner, m.runInterval, finishRun)
		return reconcile, "running", nil
	}
	finishRun()
	return reconcile, "reconciled", nil
}

func deliverHTTPLeadAssignment(ctx context.Context, m *Module, wsKey string, result *epicrunner.StartResult, actor string) string {
	if result == nil {
		return "pending"
	}
	deliveryState := "pending"
	if strings.TrimSpace(result.LeadName) == "" {
		return deliveryState
	}
	if result.Lead != nil {
		backend := strings.ToLower(strings.TrimSpace(result.Lead.Backend))
		if backend != "" && backend != backendnames.Codex {
			return deliveryState
		}
	}
	delivery, err := leadcontrol.DeliverCurrentAssignmentToCodex(ctx, m.store, wsKey, result.LeadName)
	if err != nil {
		slog.Warn("codex lead assignment delivery failed", "workspace", wsKey, "lead", result.LeadName, "err", err)
		return deliveryState
	}
	if delivery == nil {
		return deliveryState
	}
	switch delivery.State {
	case leadcontrol.DeliveryStateDelivered:
		return "delivered"
	case leadcontrol.DeliveryStatePending:
		startCodexDeliveryRetry(m, wsKey, result.LeadName, actor)
		return "pending"
	default:
		return deliveryState
	}
}

func startCodexDeliveryRetry(m *Module, wsKey, leadName, actor string) {
	if m == nil || m.store == nil || strings.TrimSpace(wsKey) == "" || strings.TrimSpace(leadName) == "" {
		return
	}
	key := wsKey + "\x00" + leadName
	if _, loaded := codexDeliveryRetryLocks.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer codexDeliveryRetryLocks.Delete(key)
		deadline := time.NewTimer(2 * time.Minute)
		defer deadline.Stop()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-deadline.C:
				return
			case <-ticker.C:
				delivery, err := leadcontrol.DeliverCurrentAssignmentToCodex(context.Background(), m.store, wsKey, leadName)
				if err != nil {
					slog.Warn("codex lead assignment retry failed", "workspace", wsKey, "lead", leadName, "err", err)
					continue
				}
				if delivery == nil {
					return
				}
				switch delivery.State {
				case leadcontrol.DeliveryStateDelivered:
					broadcastAgentRefresh(m.hub, wsKey, leadName, actor)
					return
				case leadcontrol.DeliveryStateNone, leadcontrol.DeliveryStateUnsupported:
					return
				}
			}
		}
	}()
}

func broadcastRunRefresh(hub *realtime.Hub, wsKey, leadName string, reconcile epicrunner.ReconcileResult, actor string) {
	broadcastAgentRefresh(hub, wsKey, leadName, actor)
	for _, dispatched := range reconcile.Dispatched {
		broadcastAgentRefresh(hub, wsKey, dispatched.AgentName, actor)
	}
}

func writeRunEpicResponse(w http.ResponseWriter, result *epicrunner.StartResult, reconcile epicrunner.ReconcileResult, runState, deliveryState string) {
	if strings.TrimSpace(deliveryState) == "" {
		deliveryState = "pending"
	}
	handler.WriteJSON(w, http.StatusOK, runEpicResponse{
		EpicID:                result.EpicID,
		LeadName:              result.LeadName,
		OrchestratorSessionID: result.OrchestratorSessionID,
		State:                 string(result.State),
		DeliveryState:         deliveryState,
		RunState:              runState,
		Reconcile:             reconcile,
		Dispatched:            reconcile.Dispatched,
	})
}

func runKey(workspace, epicID string) string {
	return workspace + ":" + epicID
}

func startBackgroundRun(runner *epicrunner.Runner, interval time.Duration, finish func()) {
	go func() {
		defer finish()
		timer := time.NewTimer(interval)
		defer timer.Stop()
		<-timer.C
		if err := runner.RunLoop(context.Background()); err != nil {
			slog.Warn("background epic runner stopped with error", "err", err)
		}
	}()
}

func writeEpicRunnerError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal"
	switch epicrunner.ErrorKindOf(err) {
	case epicrunner.ErrorKindValidation:
		status = http.StatusBadRequest
		code = "bad_request"
	case epicrunner.ErrorKindNotFound:
		status = http.StatusNotFound
		code = "not_found"
	case epicrunner.ErrorKindConflict:
		status = http.StatusConflict
		code = "conflict"
	case epicrunner.ErrorKindUnavailable:
		status = http.StatusServiceUnavailable
		code = "unavailable"
	}
	handler.WriteJSON(w, status, dto.NewErrorResponse(err.Error(), code))
}

func broadcastAgentRefresh(hub *realtime.Hub, workspace, agentName, actor string) {
	if hub == nil || workspace == "" || agentName == "" {
		return
	}
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "refresh",
		EntityType:  "agent",
		EntityID:    agentName,
		Action:      "agent.refresh",
		Title:       agentName,
		Actor:       actor,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceID: workspace,
	})
}
