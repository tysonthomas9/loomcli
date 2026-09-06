package subscription

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const recoveryHandleHeader = "X-Loom-Recovery-Handle"

// handleIssueRecovery reads only the source captured by the authenticated SSE
// connection. A successful response does not acknowledge or reset a checkpoint.
func handleIssueRecovery(registry *realtime.RecoveryRegistry, workspaceFromCtx func(context.Context) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			handler.RespondError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		identity, ok := middleware.UserIdentityFromContext(r.Context())
		if !ok || strings.TrimSpace(identity.UserID) == "" {
			handler.RespondError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		values := recoveryHeaderValues(r.Header, recoveryHandleHeader)
		if len(values) != 1 || !realtime.ValidRecoveryHandle(values[0]) || invalidRecoveryRequest(r) {
			handler.RespondError(w, http.StatusBadRequest, "invalid recovery request")
			return
		}
		if registry == nil {
			handler.RespondError(w, http.StatusGone, "recovery unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := registry.Read(ctx, identity.UserID, sseWorkspaceFromContext(ctx, workspaceFromCtx), values[0])
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			writeRecoveryReadError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(recoveryHandleHeader, values[0])
		w.Header().Set("X-Loom-Recovery-Source", result.SourceIdentity)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.Document)
	}
}

func recoveryHeaderValues(headers http.Header, name string) []string {
	var values []string
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

func invalidRecoveryRequest(r *http.Request) bool {
	return r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(recoveryHeaderValues(r.Header, "Transfer-Encoding")) != 0 ||
		(r.Body != nil && r.Body != http.NoBody) || len(recoveryHeaderValues(r.Header, "X-Fleet-Repo")) != 0
}

func writeRecoveryReadError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	message := "recovery source unavailable"
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "recovery did not complete"
	case errors.Is(err, realtime.ErrRecoveryUnavailable), errors.Is(err, realtime.ErrRecoveryDenied):
		status = http.StatusGone
		message = "recovery unavailable"
	case errors.Is(err, realtime.ErrRecoveryBusy):
		status = http.StatusConflict
		message = "recovery already in progress"
	}
	handler.RespondError(w, status, message)
}
