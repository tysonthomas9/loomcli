package browserauth

import (
	"context"
	"path/filepath"
	"time"
)

// EnvOperatorSocket tells `loom serve` where to listen for local operator
// session requests. The local desktop service sets it for its serve child;
// without it the local operator bridge (and the operator browser routes in
// local mode) stay disabled.
const EnvOperatorSocket = "LOOM_BROWSER_SESSION_SOCKET"

// Socket operations.
const (
	OpIssue   = "issue"
	OpRefresh = "refresh"
	OpRevoke  = "revoke"
)

const (
	socketDirName  = "run"
	socketFileName = "browser-session.sock"
	// maxUnixSocketPath is the sun_path limit on darwin (104 incl. NUL).
	maxUnixSocketPath = 103
	socketIOTimeout   = 5 * time.Second
	maxSocketRequest  = 8 << 10
)

// OperatorSocketPath returns the per-user socket path under dataDir.
func OperatorSocketPath(dataDir string) string {
	return filepath.Join(dataDir, socketDirName, socketFileName)
}

// SocketRequest is one newline-delimited JSON request.
type SocketRequest struct {
	Op        string `json:"op"`
	Workspace string `json:"workspace,omitempty"`
	Token     string `json:"token,omitempty"`
}

// SocketResponse is one newline-delimited JSON response. Token is present
// only in a successful issue response.
type SocketResponse struct {
	OK                bool      `json:"ok"`
	Error             string    `json:"error,omitempty"`
	Code              string    `json:"code,omitempty"`
	Token             string    `json:"token,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	Workspace         string    `json:"workspace,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitzero"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at,omitzero"`
	IdleTimeoutSecs   int       `json:"idle_timeout_seconds,omitempty"`
}

// WorkspaceValidator confirms the requested workspace exists before a session
// is bound to it.
type WorkspaceValidator func(ctx context.Context, workspace string) error
