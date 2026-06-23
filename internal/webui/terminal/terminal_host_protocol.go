package terminal

import (
	"errors"
	"fmt"
)

const TerminalHostProtocolVersion = 1

const (
	terminalHostOpAttach          = "attach"
	terminalHostOpAttached        = "attached"
	terminalHostOpOutput          = "output"
	terminalHostOpInput           = "input"
	terminalHostOpResize          = "resize"
	terminalHostOpDetach          = "detach"
	terminalHostOpKill            = "kill"
	terminalHostOpHasSession      = "has_session"
	terminalHostOpSessionClosed   = "session_closed"
	terminalHostOpAttachmentCount = "attachment_count"
	terminalHostOpSessionCount    = "session_count"
	terminalHostOpSessionCountFor = "session_count_for"
	terminalHostOpMaxSessions     = "max_sessions"
	terminalHostOpRegister        = "register"
	terminalHostOpEnsureSession   = "ensure_session"
	terminalHostOpWriteToSession  = "write_to_session"
	terminalHostOpPing            = "ping"
)

const (
	terminalHostErrPTYMaxSessions       = "pty_max_sessions"
	terminalHostErrPTYSessionNotFound   = "pty_session_not_found"
	terminalHostErrPTYManagerClosed     = "pty_manager_closed"
	terminalHostErrWorkspaceNotFound    = "workspace_not_registered"
	terminalHostErrInvalidWorkspacePath = "invalid_workspace_path"
	terminalHostErrUnknown              = "unknown"
)

type terminalHostRequest struct {
	ProtocolVersion int         `json:"protocol_version"`
	Op              string      `json:"op"`
	Key             SessionKey  `json:"key,omitempty"`
	ConnID          string      `json:"conn_id,omitempty"`
	Cols            uint16      `json:"cols,omitempty"`
	Rows            uint16      `json:"rows,omitempty"`
	Launch          *LaunchSpec `json:"launch,omitempty"`
	WorkspaceID     string      `json:"workspace_id,omitempty"`
	Path            string      `json:"path,omitempty"`
	Data            []byte      `json:"data,omitempty"`
	Argv            []string    `json:"argv,omitempty"`
}

type terminalHostResponse struct {
	ProtocolVersion int    `json:"protocol_version"`
	OK              bool   `json:"ok"`
	Error           string `json:"error,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ConnID          string `json:"conn_id,omitempty"`
	Reattached      bool   `json:"reattached,omitempty"`
	Scrollback      []byte `json:"scrollback,omitempty"`
	Bool            bool   `json:"bool,omitempty"`
	Count           int    `json:"count,omitempty"`
	MaxSessions     int    `json:"max_sessions,omitempty"`
	Created         bool   `json:"created,omitempty"`
}

type terminalHostStreamMessage struct {
	Op         string `json:"op"`
	Data       []byte `json:"data,omitempty"`
	ExitReason string `json:"exit_reason,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

func terminalHostErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrPTYMaxSessionsReached):
		return terminalHostErrPTYMaxSessions
	case errors.Is(err, ErrPTYSessionNotFound):
		return terminalHostErrPTYSessionNotFound
	case errors.Is(err, ErrPTYManagerClosed):
		return terminalHostErrPTYManagerClosed
	case errors.Is(err, ErrWorkspaceNotRegistered):
		return terminalHostErrWorkspaceNotFound
	case errors.Is(err, ErrInvalidWorkspacePath):
		return terminalHostErrInvalidWorkspacePath
	default:
		return terminalHostErrUnknown
	}
}

func terminalHostErrorFromCode(code, msg string) error {
	if msg == "" {
		msg = code
	}
	var base error
	switch code {
	case terminalHostErrPTYMaxSessions:
		base = ErrPTYMaxSessionsReached
	case terminalHostErrPTYSessionNotFound:
		base = ErrPTYSessionNotFound
	case terminalHostErrPTYManagerClosed:
		base = ErrPTYManagerClosed
	case terminalHostErrWorkspaceNotFound:
		base = ErrWorkspaceNotRegistered
	case terminalHostErrInvalidWorkspacePath:
		base = ErrInvalidWorkspacePath
	default:
		return errors.New(msg)
	}
	return fmt.Errorf("%w: %s", base, msg)
}
