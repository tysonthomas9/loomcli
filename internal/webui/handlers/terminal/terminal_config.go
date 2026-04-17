package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TerminalLifecycleConfig is the JSON shape returned by GET /api/config/terminal.
// Values are milliseconds. Zero means "disabled" — no auto-kill of detached
// sessions and/or no idle reap.
type TerminalLifecycleConfig struct {
	GracePeriodMS int64 `json:"grace_period_ms"`
	IdleTimeoutMS int64 `json:"idle_timeout_ms"`
	MaxSessions   int   `json:"max_sessions"`
}

// HandleGetTerminalConfig returns a handler that exposes the local PTY
// manager's lifecycle configuration. The frontend uses this to decide how
// long to keep retrying an auto-reconnect before giving up — the ceiling
// must stay ≤ the server's grace period, or the client gives up while the
// server still holds the shell open.
//
// When ptyMgr is nil (e.g. tests with no terminal backend), zero values are
// returned, which the client interprets as "no timeout".
func HandleGetTerminalConfig(ptyMgr *webuterminal.PTYManager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg := TerminalLifecycleConfig{}
		if ptyMgr != nil {
			cfg.GracePeriodMS = ptyMgr.GracePeriod().Milliseconds()
			cfg.IdleTimeoutMS = ptyMgr.IdleTimeout().Milliseconds()
			cfg.MaxSessions = ptyMgr.MaxSessions()
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    cfg,
		})
	}
}
