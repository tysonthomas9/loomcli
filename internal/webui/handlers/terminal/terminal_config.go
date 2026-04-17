package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// TerminalLifecycleConfig is the JSON shape returned by GET /api/config/terminal.
// Values are milliseconds. Zero means "disabled" — no auto-kill of detached
// sessions and/or no idle reap.
type TerminalLifecycleConfig struct {
	GracePeriodMS int64 `json:"grace_period_ms"`
	IdleTimeoutMS int64 `json:"idle_timeout_ms"`
	MaxSessions   int   `json:"max_sessions"`
}

// HandleGetTerminalConfig returns a handler that serves the supplied
// lifecycle config snapshot. The frontend uses this to decide how long to
// keep retrying an auto-reconnect — the ceiling must stay ≤ the server's
// grace period, or the client gives up while the server still holds the
// shell open. The snapshot is passed by value (not via a PTYManager
// pointer) so this handler package does not need to import the PTY
// implementation, preserving the handlers → services → terminal DAG.
func HandleGetTerminalConfig(cfg TerminalLifecycleConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    cfg,
		})
	}
}
