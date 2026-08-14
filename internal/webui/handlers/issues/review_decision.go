package issues

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type reviewDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

type reviewDecisionResponse struct {
	Success bool                          `json:"success"`
	Data    *service.ReviewDecisionResult `json:"data,omitempty"`
	Error   string                        `json:"error,omitempty"`
}

func HandleReviewDecision(svc *service.ReviewDecisionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reviewDecisionRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, reviewDecisionResponse{Success: false, Error: "invalid request body"})
			return
		}
		decisionID := strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
		actor := "local-user"
		if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && identity.UserID != "" {
			actor = identity.UserID
		}
		result, err := svc.Apply(r.Context(), service.ReviewDecisionParams{
			IssueID: r.PathValue("id"), Decision: req.Decision, Reason: req.Reason,
			Actor: actor, DecisionID: decisionID,
		})
		if err != nil {
			status, message := http.StatusInternalServerError, "internal server error"
			var svcErr *service.ServiceError
			if errors.As(err, &svcErr) {
				status, message = handler.StatusForKind(svcErr.Kind), svcErr.Message
			}
			handler.WriteJSON(w, status, reviewDecisionResponse{Success: false, Error: message})
			return
		}
		handler.WriteJSON(w, http.StatusOK, reviewDecisionResponse{Success: true, Data: result})
	}
}
