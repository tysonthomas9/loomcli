// Package browserauth mints the short-lived signed browser delegations that
// FleetDB requires for every durable-browser operation.
//
// A delegation names exactly one owner (an interactive agent), one workspace
// and a fixed set of operations. Loom mints one only after it has verified a
// principal of its own: an active agent-session binding, a validated remote
// user with browser permission, or a live local desktop operator session.
// FleetDB verifies the signature and claims independently and never trusts
// X-Actor, request bodies or URLs for owner identity.
//
// The wire contract (header and claim names) is shared with FleetDB
// internal/auth/browser_delegation.go and must stay byte-compatible.
package browserauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// MaxDelegationLifetime is FleetDB's hard ceiling; DefaultDelegationTTL stays
// well below it so clock skew never pushes a fresh token over the limit.
const (
	MaxDelegationLifetime = 5 * time.Minute
	DefaultDelegationTTL  = 2 * time.Minute
)

// Default issuer/audience for the local desktop runtime, where Loom launches
// its own embedded FleetDB and configures both ends.
const (
	DefaultIssuer   = "loom"
	DefaultAudience = "fleet-db"
)

// ErrSignerUnavailable is returned when no signing key is configured. It wraps
// domain.ErrBrowserUnavailable so HTTP layers render a visible 503.
var ErrSignerUnavailable = fmt.Errorf("browser delegation signer not configured: %w", domain.ErrBrowserUnavailable)

// Principal is the verified caller Loom delegates for. Exactly one of the
// agent-session or operator shapes is valid; Validate enforces that.
type Principal struct {
	Kind string // domain.BrowserPrincipalAgentSession | domain.BrowserPrincipalWorkspaceOperator

	// Subject becomes the delegation `sub` and the browser's created_by.
	Subject string

	// SessionID is the agent-session binding ID (agent principal only).
	SessionID string

	// AuthMode is remote_user or local_desktop (operator only).
	AuthMode string

	// LocalOperatorSessionID identifies the local desktop session (operator
	// with auth_mode=local_desktop only). It is an ID, never the bearer.
	LocalOperatorSessionID string
}

// Validate rejects principal shapes FleetDB would reject, so Loom fails
// before signing rather than minting a token that can never verify.
func (p Principal) Validate() error {
	if strings.TrimSpace(p.Subject) == "" {
		return errors.New("browser principal subject is required")
	}
	switch p.Kind {
	case domain.BrowserPrincipalAgentSession:
		if p.SessionID == "" || p.AuthMode != "" || p.LocalOperatorSessionID != "" {
			return errors.New("agent_session principal requires only a session id")
		}
	case domain.BrowserPrincipalWorkspaceOperator:
		switch p.AuthMode {
		case domain.BrowserAuthModeLocalDesktop:
			if p.LocalOperatorSessionID == "" {
				return errors.New("local_desktop operator requires a local operator session id")
			}
		case domain.BrowserAuthModeRemoteUser:
			if p.LocalOperatorSessionID != "" {
				return errors.New("remote_user operator must not carry a local session id")
			}
		default:
			return fmt.Errorf("unknown operator auth mode %q", p.AuthMode)
		}
		if p.SessionID != "" {
			return errors.New("operator principal must not carry an agent session id")
		}
	default:
		return fmt.Errorf("unknown browser principal kind %q", p.Kind)
	}
	return nil
}

// Signer mints Ed25519 (EdDSA) browser delegations.
type Signer struct {
	keyID    string
	key      ed25519.PrivateKey
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// NewSigner returns a signer. keyID, issuer and audience are required.
func NewSigner(keyID string, key ed25519.PrivateKey, issuer, audience string) (*Signer, error) {
	if strings.TrimSpace(keyID) == "" || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("browser delegation signer: key id and ed25519 private key are required")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, errors.New("browser delegation signer: issuer and audience are required")
	}
	return &Signer{keyID: keyID, key: key, issuer: issuer, audience: audience, ttl: DefaultDelegationTTL, now: time.Now}, nil
}

// Mint signs a delegation for principal acting on owner in workspace.
// A nil signer returns ErrSignerUnavailable.
func (s *Signer) Mint(workspace, owner string, principal Principal, operations ...string) (string, error) {
	if s == nil {
		return "", ErrSignerUnavailable
	}
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(owner) == "" {
		return "", fmt.Errorf("browser delegation: workspace and owner are required: %w", domain.ErrInvalid)
	}
	if err := principal.Validate(); err != nil {
		return "", fmt.Errorf("browser delegation: %w: %w", err, domain.ErrInvalid)
	}
	if len(operations) == 0 {
		return "", fmt.Errorf("browser delegation: at least one operation is required: %w", domain.ErrInvalid)
	}
	for _, op := range operations {
		if !validOperation(op) {
			return "", fmt.Errorf("browser delegation: unknown operation %q: %w", op, domain.ErrInvalid)
		}
		if op == domain.BrowserOpCreate && principal.Kind != domain.BrowserPrincipalAgentSession {
			return "", fmt.Errorf("browser delegation: only an agent session may create browsers: %w", domain.ErrBrowserForbidden)
		}
	}
	claims, err := s.claims(workspace, owner, principal, operations)
	if err != nil {
		return "", err
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("browser delegation: sign: %w", err)
	}
	return signed, nil
}

// claims builds the delegation claim set for an already-validated request.
func (s *Signer) claims(workspace, owner string, principal Principal, operations []string) (jwt.MapClaims, error) {
	jti, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("browser delegation: jti: %w", err)
	}
	now := s.now().UTC()
	claims := jwt.MapClaims{
		"iss":            s.issuer,
		"aud":            s.audience,
		"sub":            principal.Subject,
		"workspace_key":  workspace,
		"principal_kind": principal.Kind,
		"owner_agent_id": owner,
		"operations":     append([]string(nil), operations...),
		"iat":            now.Unix(),
		"exp":            now.Add(s.ttl).Unix(),
		"jti":            jti,
	}
	switch principal.Kind {
	case domain.BrowserPrincipalAgentSession:
		claims["session_id"] = principal.SessionID
	case domain.BrowserPrincipalWorkspaceOperator:
		claims["auth_mode"] = principal.AuthMode
		if principal.LocalOperatorSessionID != "" {
			claims["local_operator_session_id"] = principal.LocalOperatorSessionID
		}
	}
	return claims, nil
}

func validOperation(op string) bool {
	switch op {
	case domain.BrowserOpCreate, domain.BrowserOpList, domain.BrowserOpGet, domain.BrowserOpSelect:
		return true
	}
	return false
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
