// Package authority defines capability-independent, server-derived authority
// values and the default-deny admission mechanism used at module boundaries.
//
// Authority values are deliberately opaque. Only an Issuer can populate their
// unexported state, their zero values are invalid, and their wire encoders and
// decoders fail closed. Transports verify credentials and ask the server-owned
// Issuer to derive a typed authority; request payloads never carry authority.
package authority

import (
	"errors"
	"time"
)

// Action is the stable, capability-qualified name admitted by an operation
// registry, for example "workflowcatalog.approve-version".
type Action string

// Class identifies the verified principal and authority class. Class is used
// by the generic operation registry; capability commands should still accept
// the concrete authority type they require.
type Class string

const (
	ClassOperator  Class = "operator"
	ClassExecution Class = "execution"
	ClassSession   Class = "session"
	ClassWebhook   Class = "webhook"
	ClassSystem    Class = "system"
)

var (
	// ErrInvalidIssuer means an uninitialized issuer or admission registry was
	// used. The zero value of Issuer is intentionally unusable.
	ErrInvalidIssuer = errors.New("authority: invalid issuer")
	// ErrInvalidPrincipal means verified-principal claims were incomplete or
	// the principal was not derived by the issuer being used.
	ErrInvalidPrincipal = errors.New("authority: invalid verified principal")
	// ErrInvalidScope means a workspace, action, expiry, or system audit reason
	// was empty or otherwise invalid.
	ErrInvalidScope = errors.New("authority: invalid scope")
	// ErrPrincipalExpired means authority derivation was attempted at or after
	// the verified principal's expiry.
	ErrPrincipalExpired = errors.New("authority: verified principal expired")
	// ErrPrincipalClass means a principal of one class was used to derive a
	// different authority class.
	ErrPrincipalClass = errors.New("authority: verified principal class mismatch")
	// ErrWorkspaceMismatch means the requested workspace does not exactly match
	// the workspace established by credential verification.
	ErrWorkspaceMismatch = errors.New("authority: workspace mismatch")
	// ErrActionNotAllowed means credential verification did not grant the exact
	// requested action. Wildcard actions are intentionally unsupported.
	ErrActionNotAllowed = errors.New("authority: action not allowed")
	// ErrOpaqueAuthority is returned by authority wire encoders and decoders.
	// Authority must be derived server-side and must never cross a request wire.
	ErrOpaqueAuthority = errors.New("authority: opaque values cannot be serialized or deserialized")
)

// PrincipalClaims are the claims produced by successful credential
// verification. DeriveVerifiedPrincipal validates and seals them for one
// server-side Issuer. Callers must not populate this value from request JSON;
// it is the output of credential verification, not a transport DTO.
type PrincipalClaims struct {
	Subject   string
	Class     Class
	Workspace string
	Actions   []Action
	ExpiresAt time.Time
}

type issuerSeal struct {
	nonce byte
}

// VerifiedPrincipal is an opaque, issuer-bound representation of already
// verified claims. Its zero value is invalid.
type VerifiedPrincipal struct {
	seal            *issuerSeal
	subject         string
	class           Class
	workspace       string
	actions         map[Action]struct{}
	expiresAt       time.Time
	principalMarker verifiedPrincipalMarker
}

type verifiedPrincipalMarker struct{ value byte }

// Subject returns the server-verified audit subject.
func (p VerifiedPrincipal) Subject() string { return p.subject }

// Class returns the server-verified principal class.
func (p VerifiedPrincipal) Class() Class { return p.class }

// Workspace returns the server-verified workspace scope.
func (p VerifiedPrincipal) Workspace() string { return p.workspace }

// ExpiresAt returns the credential expiry inherited by derived authorities.
func (p VerifiedPrincipal) ExpiresAt() time.Time { return p.expiresAt }

type grant struct {
	seal      *issuerSeal
	subject   string
	class     Class
	workspace string
	action    Action
	expiresAt time.Time
	reason    string
}

// Authority is a sealed union of the five supported authority value types.
// It exists for coarse operation-registry admission. Capability command APIs
// should accept a concrete type such as OperatorAuthority instead.
type Authority interface {
	isAuthority()
}

type operatorAuthorityMarker struct{ value byte }
type executionAuthorityMarker struct{ value [2]byte }
type sessionAuthorityMarker struct{ value [3]byte }
type webhookAuthorityMarker struct{ value [4]byte }
type systemAuthorityMarker struct{ value [5]byte }

// OperatorAuthority authorizes one operator action in one workspace until the
// verified credential expires. Its zero value is invalid.
type OperatorAuthority struct {
	grant          grant
	operatorMarker operatorAuthorityMarker
}

// ExecutionAuthority represents one execution-scoped caller. It is a distinct
// type and cannot be substituted for OperatorAuthority.
type ExecutionAuthority struct {
	grant           grant
	executionMarker executionAuthorityMarker
}

// SessionAuthority represents one session-scoped caller. It is a distinct
// type and cannot be substituted for OperatorAuthority.
type SessionAuthority struct {
	grant         grant
	sessionMarker sessionAuthorityMarker
}

// WebhookAuthority represents one verified webhook caller. It is a distinct
// type and cannot be substituted for OperatorAuthority.
type WebhookAuthority struct {
	grant         grant
	webhookMarker webhookAuthorityMarker
}

// SystemAuthority represents a registered system component authorized for one
// exact action and carrying a nonempty audit reason. It is not a superuser.
type SystemAuthority struct {
	grant        grant
	systemMarker systemAuthorityMarker
}

func (OperatorAuthority) isAuthority()  {}
func (ExecutionAuthority) isAuthority() {}
func (SessionAuthority) isAuthority()   {}
func (WebhookAuthority) isAuthority()   {}
func (SystemAuthority) isAuthority()    {}

func (a OperatorAuthority) Subject() string      { return a.grant.subject }
func (a OperatorAuthority) Workspace() string    { return a.grant.workspace }
func (a OperatorAuthority) Action() Action       { return a.grant.action }
func (a OperatorAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

func (a ExecutionAuthority) Subject() string      { return a.grant.subject }
func (a ExecutionAuthority) Workspace() string    { return a.grant.workspace }
func (a ExecutionAuthority) Action() Action       { return a.grant.action }
func (a ExecutionAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

func (a SessionAuthority) Subject() string      { return a.grant.subject }
func (a SessionAuthority) Workspace() string    { return a.grant.workspace }
func (a SessionAuthority) Action() Action       { return a.grant.action }
func (a SessionAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

func (a WebhookAuthority) Subject() string      { return a.grant.subject }
func (a WebhookAuthority) Workspace() string    { return a.grant.workspace }
func (a WebhookAuthority) Action() Action       { return a.grant.action }
func (a WebhookAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

func (a SystemAuthority) Subject() string      { return a.grant.subject }
func (a SystemAuthority) Workspace() string    { return a.grant.workspace }
func (a SystemAuthority) Action() Action       { return a.grant.action }
func (a SystemAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

// Reason returns the mandatory server-side audit reason attached to a system
// authority.
func (a SystemAuthority) Reason() string { return a.grant.reason }
