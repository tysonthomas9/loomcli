package authority

import (
	"fmt"
	"strings"
	"time"
)

// Issuer is the server-side factory for verified principals, typed authority,
// and issuer-bound admission registries. Construct it in server composition;
// request DTOs and capability modules should never receive an Issuer.
type Issuer struct {
	seal *issuerSeal
	now  func() time.Time
}

// NewIssuer constructs an Issuer backed by the process wall clock.
func NewIssuer() *Issuer {
	issuer, err := NewIssuerWithClock(time.Now)
	if err != nil {
		panic(err)
	}
	return issuer
}

// NewIssuerWithClock constructs an Issuer with an injected clock. It is useful
// for deterministic expiry tests and server compositions with a platform
// clock. A nil clock is rejected.
func NewIssuerWithClock(now func() time.Time) (*Issuer, error) {
	if now == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidIssuer)
	}
	return &Issuer{seal: &issuerSeal{nonce: 1}, now: now}, nil
}

// DeriveVerifiedPrincipal validates already-verified claims and binds them to
// this Issuer. It does not verify a credential itself; the transport's
// credential verifier supplies PrincipalClaims only after verification.
func (i *Issuer) DeriveVerifiedPrincipal(claims PrincipalClaims) (VerifiedPrincipal, error) {
	if err := i.validate(); err != nil {
		return VerifiedPrincipal{}, err
	}
	subject := strings.TrimSpace(claims.Subject)
	workspace := strings.TrimSpace(claims.Workspace)
	if subject == "" {
		return VerifiedPrincipal{}, fmt.Errorf("%w: subject is required", ErrInvalidPrincipal)
	}
	if !validClass(claims.Class) {
		return VerifiedPrincipal{}, fmt.Errorf("%w: unsupported class %q", ErrInvalidPrincipal, claims.Class)
	}
	if workspace == "" {
		return VerifiedPrincipal{}, fmt.Errorf("%w: workspace is required", ErrInvalidScope)
	}
	if claims.ExpiresAt.IsZero() || !i.now().Before(claims.ExpiresAt) {
		return VerifiedPrincipal{}, fmt.Errorf("%w: expiry must be in the future", ErrInvalidPrincipal)
	}
	actions := make(map[Action]struct{}, len(claims.Actions))
	for _, value := range claims.Actions {
		action, err := normalizeAction(value)
		if err != nil {
			return VerifiedPrincipal{}, fmt.Errorf("%w: principal action: %v", ErrInvalidPrincipal, err)
		}
		actions[action] = struct{}{}
	}
	if len(actions) == 0 {
		return VerifiedPrincipal{}, fmt.Errorf("%w: at least one exact action is required", ErrInvalidPrincipal)
	}
	return VerifiedPrincipal{
		seal:      i.seal,
		subject:   subject,
		class:     claims.Class,
		workspace: workspace,
		actions:   actions,
		expiresAt: claims.ExpiresAt,
	}, nil
}

// IssueOperator derives an operator authority for one exact workspace/action.
func (i *Issuer) IssueOperator(principal VerifiedPrincipal, workspace string, action Action) (OperatorAuthority, error) {
	value, err := i.issue(principal, ClassOperator, workspace, action, "")
	if err != nil {
		return OperatorAuthority{}, err
	}
	return OperatorAuthority{grant: value}, nil
}

// IssueExecution derives an execution authority for one exact workspace/action.
func (i *Issuer) IssueExecution(principal VerifiedPrincipal, workspace string, action Action) (ExecutionAuthority, error) {
	value, err := i.issue(principal, ClassExecution, workspace, action, "")
	if err != nil {
		return ExecutionAuthority{}, err
	}
	return ExecutionAuthority{grant: value}, nil
}

// IssueExecutionForOwner derives an execution authority and binds the exact
// server-verified node/lease/fence tuple that owned the running execution when
// the authority was issued.
func (i *Issuer) IssueExecutionForOwner(principal VerifiedPrincipal, workspace string, action Action, owner ExecutionOwner) (ExecutionAuthority, error) {
	if strings.TrimSpace(owner.NodeID) == "" || owner.NodeID != strings.TrimSpace(owner.NodeID) ||
		strings.TrimSpace(owner.LeaseID) == "" || owner.LeaseID != strings.TrimSpace(owner.LeaseID) || owner.FencingToken <= 0 {
		return ExecutionAuthority{}, fmt.Errorf("%w: execution owner node, lease, and positive fence are required", ErrInvalidScope)
	}
	value, err := i.issue(principal, ClassExecution, workspace, action, "")
	if err != nil {
		return ExecutionAuthority{}, err
	}
	return ExecutionAuthority{grant: value, owner: owner}, nil
}

// IssueSession derives a session authority for one exact workspace/action.
func (i *Issuer) IssueSession(principal VerifiedPrincipal, workspace string, action Action) (SessionAuthority, error) {
	value, err := i.issue(principal, ClassSession, workspace, action, "")
	if err != nil {
		return SessionAuthority{}, err
	}
	return SessionAuthority{grant: value}, nil
}

// IssueWebhook derives a webhook authority for one exact workspace/action.
func (i *Issuer) IssueWebhook(principal VerifiedPrincipal, workspace string, action Action) (WebhookAuthority, error) {
	value, err := i.issue(principal, ClassWebhook, workspace, action, "")
	if err != nil {
		return WebhookAuthority{}, err
	}
	return WebhookAuthority{grant: value}, nil
}

// IssueSystem derives an action-scoped SystemAuthority. A nonempty reason is
// required so use by a registered runtime component can be audited.
func (i *Issuer) IssueSystem(principal VerifiedPrincipal, workspace string, action Action, reason string) (SystemAuthority, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return SystemAuthority{}, fmt.Errorf("%w: system audit reason is required", ErrInvalidScope)
	}
	value, err := i.issue(principal, ClassSystem, workspace, action, reason)
	if err != nil {
		return SystemAuthority{}, err
	}
	return SystemAuthority{grant: value}, nil
}

func (i *Issuer) issue(principal VerifiedPrincipal, class Class, workspace string, action Action, reason string) (grant, error) {
	if err := i.validate(); err != nil {
		return grant{}, err
	}
	if principal.seal == nil || principal.seal != i.seal || principal.actions == nil {
		return grant{}, fmt.Errorf("%w: principal belongs to another or no issuer", ErrInvalidPrincipal)
	}
	if principal.class != class {
		return grant{}, fmt.Errorf("%w: got %q, need %q", ErrPrincipalClass, principal.class, class)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return grant{}, fmt.Errorf("%w: workspace is required", ErrInvalidScope)
	}
	if workspace != principal.workspace {
		return grant{}, fmt.Errorf("%w: requested %q, verified %q", ErrWorkspaceMismatch, workspace, principal.workspace)
	}
	action, err := normalizeAction(action)
	if err != nil {
		return grant{}, err
	}
	if _, ok := principal.actions[action]; !ok {
		return grant{}, fmt.Errorf("%w: %q", ErrActionNotAllowed, action)
	}
	if principal.expiresAt.IsZero() || !i.now().Before(principal.expiresAt) {
		return grant{}, ErrPrincipalExpired
	}
	return grant{
		seal:      i.seal,
		subject:   principal.subject,
		class:     class,
		workspace: workspace,
		action:    action,
		expiresAt: principal.expiresAt,
		reason:    reason,
	}, nil
}

func (i *Issuer) validate() error {
	if i == nil || i.seal == nil || i.now == nil {
		return ErrInvalidIssuer
	}
	return nil
}

func normalizeAction(value Action) (Action, error) {
	action := Action(strings.TrimSpace(string(value)))
	if action == "" {
		return "", fmt.Errorf("%w: action is required", ErrInvalidScope)
	}
	if action == "*" {
		return "", fmt.Errorf("%w: wildcard actions are forbidden", ErrInvalidScope)
	}
	return action, nil
}

func validClass(class Class) bool {
	switch class {
	case ClassOperator, ClassExecution, ClassSession, ClassWebhook, ClassSystem:
		return true
	default:
		return false
	}
}
