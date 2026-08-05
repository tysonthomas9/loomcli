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
	"sync"
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
	owner           ExecutionOwner
	executionMarker executionAuthorityMarker
}

// ExecutionResourceKind identifies the exact fenced runtime resource owned by
// an execution authority. DriverRun and TaskRun deliberately remain distinct
// until TaskRun fencing has the same durable guarantees as DriverRun fencing.
type ExecutionResourceKind string

const (
	ExecutionResourceDriverRun ExecutionResourceKind = "driver_run"
	ExecutionResourceTaskRun   ExecutionResourceKind = "task_run"
)

// ExecutionOwner is the verified lease/fence tuple bound to an execution
// authority. It is server-derived and never accepted from a request wire.
type ExecutionOwner struct {
	ResourceKind ExecutionResourceKind
	ResourceID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
}

// SessionAuthority represents one session-scoped caller. It is a distinct
// type and cannot be substituted for OperatorAuthority.
type SessionAuthority struct {
	grant         grant
	owner         SessionOwner
	sessionMarker sessionAuthorityMarker
}

// SessionOwner is the server-verified lease generation bound to a
// SessionAuthority. SessionID and AgentID identify the Interaction aggregate;
// TerminalID is set for PTY-backed children and left empty for non-terminal
// chat/inbox sessions. The raw lease credential is deliberately absent.
type SessionOwner struct {
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
	credential   *sessionLeaseCredential
}

// sessionLeaseCredential retains one raw AgentLease credential only long
// enough to carry a server-validated SessionAuthority into the exact
// owner-fenced FleetDB command. It is shared by value-copies of SessionOwner,
// but ConsumeLeaseCredential succeeds at most once and clears the retained
// bytes before returning.
type sessionLeaseCredential struct {
	mu    sync.Mutex
	value []byte
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

// ResourceKind returns the server-verified execution resource discriminator.
func (a ExecutionAuthority) ResourceKind() ExecutionResourceKind { return a.owner.ResourceKind }

// ResourceID returns the server-verified DriverRun or TaskRun identifier.
func (a ExecutionAuthority) ResourceID() string { return a.owner.ResourceID }

// NodeID returns the server-verified execution owner node.
func (a ExecutionAuthority) NodeID() string { return a.owner.NodeID }

// LeaseID returns the server-verified execution owner lease.
func (a ExecutionAuthority) LeaseID() string { return a.owner.LeaseID }

// FencingToken returns the server-verified execution owner fence.
func (a ExecutionAuthority) FencingToken() int64 { return a.owner.FencingToken }

func (a SessionAuthority) Subject() string      { return a.grant.subject }
func (a SessionAuthority) Workspace() string    { return a.grant.workspace }
func (a SessionAuthority) Action() Action       { return a.grant.action }
func (a SessionAuthority) ExpiresAt() time.Time { return a.grant.expiresAt }

// SessionID returns the server-verified AgentSession identity.
func (a SessionAuthority) SessionID() string { return a.owner.SessionID }

// AgentID returns the durable Agent identity attached to the session.
func (a SessionAuthority) AgentID() string { return a.owner.AgentID }

// TerminalID returns the exact terminal identity for PTY-backed authorities.
// It is empty for session-scoped chat and inbox operations.
func (a SessionAuthority) TerminalID() string { return a.owner.TerminalID }

// NodeID returns the node that owns the live session generation.
func (a SessionAuthority) NodeID() string { return a.owner.NodeID }

// LeaseID returns the verified session lease identity.
func (a SessionAuthority) LeaseID() string { return a.owner.LeaseID }

// FencingToken returns the verified session lease generation.
func (a SessionAuthority) FencingToken() int64 { return a.owner.FencingToken }

// SessionOwner returns the exact server-verified owner generation attached to
// this authority. The returned value may carry a private one-use lease
// credential for the capability's FleetDB transport; ordinary callers cannot
// inspect or serialize that credential.
func (a SessionAuthority) SessionOwner() SessionOwner { return a.owner }

// ConsumeLeaseCredential transfers the private raw lease credential to the
// owner-fenced transport exactly once. The caller must clear the returned
// bytes after adding them to the outbound credential header.
func (owner SessionOwner) ConsumeLeaseCredential() []byte {
	if owner.credential == nil {
		return nil
	}
	owner.credential.mu.Lock()
	defer owner.credential.mu.Unlock()
	value := append([]byte(nil), owner.credential.value...)
	clear(owner.credential.value)
	owner.credential.value = nil
	return value
}

// CloseLeaseCredential clears an unused private credential. It is safe to call
// after ConsumeLeaseCredential and from multiple value-copies of the owner.
func (owner SessionOwner) CloseLeaseCredential() {
	if owner.credential == nil {
		return
	}
	owner.credential.mu.Lock()
	defer owner.credential.mu.Unlock()
	clear(owner.credential.value)
	owner.credential.value = nil
}

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
