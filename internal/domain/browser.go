package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Browser observed statuses as FleetDB stores them. Loom never invents a
// status: an unrecognized stored value is reported as-is and must never be
// treated as ready.
const (
	BrowserStatusStarting = "starting"
	BrowserStatusFailed   = "failed"
	BrowserStatusReady    = "ready"
)

// Browser delegation operations. They name exactly one FleetDB browser route
// each and are the only values a signed delegation may carry.
const (
	BrowserOpCreate = "browser:create"
	BrowserOpList   = "browser:list"
	BrowserOpGet    = "browser:get"
	BrowserOpSelect = "browser:select"
)

// Browser delegation principal kinds and operator auth modes (see
// docs/product/kernel-browser-auth.md).
const (
	BrowserPrincipalAgentSession      = "agent_session"
	BrowserPrincipalWorkspaceOperator = "workspace_operator"
	BrowserAuthModeRemoteUser         = "remote_user"
	BrowserAuthModeLocalDesktop       = "local_desktop"
)

// Browser length limits mirror FleetDB validation so the CLI can reject a bad
// request before any network call.
const (
	BrowserNameMaxRunes      = 200
	BrowserRequestIDMaxRunes = 256
)

var (
	// ErrBrowserRequestConflict means the request ID was already used for a
	// different create payload for the same owner.
	ErrBrowserRequestConflict = errors.New("domain: browser request id reused with a different payload")

	// ErrBrowserUnauthorized means the caller has no valid browser principal
	// (no agent-session binding, no operator session, or FleetDB rejected the
	// delegation).
	ErrBrowserUnauthorized = errors.New("domain: browser authorization required")

	// ErrBrowserForbidden means the caller is authenticated but not permitted
	// to act on the requested interactive agent.
	ErrBrowserForbidden = errors.New("domain: browser access forbidden")

	// ErrBrowserUnavailable means the browser authority (FleetDB, the
	// delegation signer, or the local session service) is not configured or
	// not reachable. Callers must surface it; they must never fall back to a
	// locally synthesized browser.
	ErrBrowserUnavailable = errors.New("domain: browser service unavailable")
)

// Browser is the durable identity of one interactive-agent browser as served
// by FleetDB. It carries no runtime, CDP, or page data.
type Browser struct {
	ID           string `json:"id"`
	WorkspaceKey string `json:"workspace_key"`
	OwnerAgentID string `json:"owner_agent_id"`
	CreatedBy    string `json:"created_by"`
	Name         string `json:"name"`
	DesiredState string `json:"desired_state"`
	Status       string `json:"status"`
	RequestID    string `json:"request_id"`
	Selected     bool   `json:"selected"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// BrowserCreate is the caller-controlled part of a create. The owner is never
// part of it: it comes only from a verified principal.
type BrowserCreate struct {
	Name      string `json:"name"`
	RequestID string `json:"request_id"`
}

// Normalize trims the create payload in place and validates it.
func (c *BrowserCreate) Normalize() error {
	c.Name = strings.TrimSpace(c.Name)
	c.RequestID = strings.TrimSpace(c.RequestID)
	if c.Name == "" || utf8.RuneCountInString(c.Name) > BrowserNameMaxRunes {
		return errors.Join(ErrInvalid, errors.New("browser name must contain 1 to 200 characters"))
	}
	if c.RequestID == "" || utf8.RuneCountInString(c.RequestID) > BrowserRequestIDMaxRunes {
		return errors.Join(ErrInvalid, errors.New("browser request_id must contain 1 to 256 characters"))
	}
	return nil
}
