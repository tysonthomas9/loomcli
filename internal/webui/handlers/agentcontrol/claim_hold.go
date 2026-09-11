package agentcontrol

import (
	"encoding/json"
	"net/http"
	"os"
	"os/user"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// The web half of the claim hold: a workspace-level, explicitly-owned refusal
// to START new work, which leaves every run already in flight untouched. These
// are thin proxies over the daemon control socket — the daemon owns the hold,
// its persistence and its ownership rules, so a stale browser tab gets the
// daemon's precise refusal rather than a silently divergent second opinion.
//
// A hold is OWNED, which is why every route resolves an actor: releasing
// someone else's quiesce (a colleague's, or the deploy script's) must be a
// deliberate act, and without force it is refused with 409.

// handleClaimHoldGet handles GET /api/workspaces/{ws}/claims/hold.
// Returns hold: null when claims are free.
func handleClaimHoldGet(holdFn ClaimHoldFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := holdFn("claims_hold_get", nil)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		writeClaimHoldResult(w, result)
	}
}

// handleClaimHoldSet handles POST /api/workspaces/{ws}/claims/hold.
// A hold by the same actor is an idempotent refresh; replacing a foreign
// holder needs force, and is 409 without it.
func handleClaimHoldSet(holdFn ClaimHoldFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req claimHoldSetRequest
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("invalid request body", "bad_request"))
			return
		}
		if strings.TrimSpace(req.Reason) == "" {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("reason is required to hold claims", "bad_request"))
			return
		}
		if req.TTLSeconds < 0 {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("ttl_seconds must not be negative", "bad_request"))
			return
		}

		sendClaimHoldSet(w, holdFn, claimHoldSetArgs{
			Held:       true,
			Actor:      resolveActor(r, req.Actor),
			Reason:     req.Reason,
			TTLSeconds: req.TTLSeconds,
			Force:      req.Force,
		})
	}
}

// handleClaimHoldRelease handles DELETE /api/workspaces/{ws}/claims/hold.
// {"force": true} releases a hold owned by someone else. It may be given as a
// JSON body or as ?force=true&actor=... — the browser client's shared fetch
// helper cannot attach a body to a DELETE, and a release must not be the one
// operation the web UI cannot perform.
func handleClaimHoldRelease(holdFn ClaimHoldFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req claimHoldReleaseRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := handler.ReadJSON(w, r, &req); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest,
					dto.NewErrorResponse("invalid request body", "bad_request"))
				return
			}
		}
		q := r.URL.Query()
		if req.Actor == "" {
			req.Actor = q.Get("actor")
		}
		if !req.Force {
			req.Force = q.Get("force") == "true" || q.Get("force") == "1"
		}

		sendClaimHoldSet(w, holdFn, claimHoldSetArgs{
			Held:  false,
			Actor: resolveActor(r, req.Actor),
			Force: req.Force,
		})
	}
}

// sendClaimHoldSet marshals the args, sends claims_hold_set and writes the
// resulting status (both operations answer with the same payload).
func sendClaimHoldSet(w http.ResponseWriter, holdFn ClaimHoldFn, args claimHoldSetArgs) {
	raw, err := json.Marshal(args)
	if err != nil {
		handler.WriteJSON(w, http.StatusBadRequest,
			dto.NewErrorResponse("invalid request body", "bad_request"))
		return
	}
	result, err := holdFn("claims_hold_set", raw)
	if err != nil {
		writeDaemonError(w, err)
		return
	}
	writeClaimHoldResult(w, result)
}

// writeClaimHoldResult renders a daemon claim-hold response: the status on
// success, a classified error otherwise.
func writeClaimHoldResult(w http.ResponseWriter, result *AgentControlResult) {
	if !result.Success {
		status, code := classifyClaimHoldError(result)
		handler.WriteJSON(w, status, dto.NewErrorResponse(result.Error, code))
		return
	}
	status := ClaimHoldStatusView{Running: []ClaimHoldRunningView{}}
	if len(result.Data) > 0 {
		if err := json.Unmarshal(result.Data, &status); err != nil {
			handler.WriteJSON(w, http.StatusBadGateway,
				dto.NewErrorResponse("malformed claim hold payload from daemon", "daemon_error"))
			return
		}
	}
	if status.Running == nil {
		status.Running = []ClaimHoldRunningView{}
	}
	handler.WriteJSON(w, http.StatusOK, status)
}

// classifyClaimHoldError maps the daemon's refusals to HTTP status codes.
// Both ownership refusals ("use --force to release" / "to replace") are 409:
// the request was well-formed and the daemon is healthy — the caller simply
// does not own the hold.
func classifyClaimHoldError(result *AgentControlResult) (int, string) {
	e := result.Error
	switch {
	case strings.Contains(e, "use --force"):
		return http.StatusConflict, "claim_hold_conflict"
	case strings.Contains(e, "is required"):
		return http.StatusBadRequest, "bad_request"
	default:
		return http.StatusBadGateway, "daemon_error"
	}
}

// resolveActor names who is acting, most explicit source first:
// request body > X-Actor header > LOOM_ACTOR env > OS user. The final
// fallback is deliberately a non-empty placeholder — the daemon rejects an
// empty actor, and an unattributable hold is worse than a vaguely attributed
// one.
func resolveActor(r *http.Request, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Actor")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
		return u.Username
	}
	return "webui"
}
