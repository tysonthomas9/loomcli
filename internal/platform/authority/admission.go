package authority

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidOperationRule means an admission rule is empty, uses an unknown
	// authority class, or duplicates another registered action.
	ErrInvalidOperationRule = errors.New("authority: invalid operation rule")
	// ErrAdmissionDenied is the sentinel wrapped by every AdmissionError.
	ErrAdmissionDenied = errors.New("authority: admission denied")
)

// DenialReason is a stable admission-failure classification.
type DenialReason string

const (
	DenialUnknownOperation DenialReason = "unknown_operation"
	DenialInvalidAuthority DenialReason = "invalid_authority"
	DenialWrongClass       DenialReason = "wrong_authority_class"
	DenialWrongAction      DenialReason = "wrong_action"
	DenialWrongWorkspace   DenialReason = "wrong_workspace"
	DenialExpired          DenialReason = "expired"
)

// AdmissionError reports why a default-deny operation registry rejected a
// caller. It unwraps to ErrAdmissionDenied for stable transport handling.
type AdmissionError struct {
	Reason    DenialReason
	Action    Action
	Workspace string
	Class     Class
}

func (e *AdmissionError) Error() string {
	if e == nil {
		return ErrAdmissionDenied.Error()
	}
	return fmt.Sprintf("%s: %s (action=%q workspace=%q class=%q)", ErrAdmissionDenied, e.Reason, e.Action, e.Workspace, e.Class)
}

// Unwrap supports errors.Is(err, ErrAdmissionDenied).
func (e *AdmissionError) Unwrap() error { return ErrAdmissionDenied }

// OperationRule declares the authority classes accepted by one exact action.
// Unknown actions remain denied; there is no wildcard operation or class.
type OperationRule struct {
	Action  Action
	Allowed []Class
}

// Allow constructs a rule for one exact action and the listed authority
// classes. NewAdmission performs strict validation and defensive copying.
func Allow(action Action, classes ...Class) OperationRule {
	return OperationRule{Action: action, Allowed: classes}
}

// OperatorOnly constructs an operator-only operation rule.
func OperatorOnly(action Action) OperationRule {
	return Allow(action, ClassOperator)
}

// Admission is an immutable, issuer-bound, default-deny operation registry.
type Admission struct {
	seal  *issuerSeal
	now   func() time.Time
	rules map[Action]map[Class]struct{}
}

// NewAdmission creates an issuer-bound operation registry. Duplicate, empty,
// wildcard, and classless rules fail closed.
func (i *Issuer) NewAdmission(rules ...OperationRule) (*Admission, error) {
	if err := i.validate(); err != nil {
		return nil, err
	}
	registered := make(map[Action]map[Class]struct{}, len(rules))
	for index, rule := range rules {
		action, err := normalizeAction(rule.Action)
		if err != nil {
			return nil, fmt.Errorf("%w: rule %d: %v", ErrInvalidOperationRule, index, err)
		}
		if _, exists := registered[action]; exists {
			return nil, fmt.Errorf("%w: duplicate action %q", ErrInvalidOperationRule, action)
		}
		allowed := make(map[Class]struct{}, len(rule.Allowed))
		for _, class := range rule.Allowed {
			if !validClass(class) {
				return nil, fmt.Errorf("%w: action %q has unsupported class %q", ErrInvalidOperationRule, action, class)
			}
			allowed[class] = struct{}{}
		}
		if len(allowed) == 0 {
			return nil, fmt.Errorf("%w: action %q must allow at least one class", ErrInvalidOperationRule, action)
		}
		registered[action] = allowed
	}
	return &Admission{seal: i.seal, now: i.now, rules: registered}, nil
}

// Admit applies coarse operation admission. It rejects unknown operations,
// authorities from another issuer, unregistered classes, wrong action or
// workspace scopes, and expired credentials.
func (a *Admission) Admit(action Action, workspace string, authority Authority) error {
	action = Action(strings.TrimSpace(string(action)))
	workspace = strings.TrimSpace(workspace)
	if a == nil || a.seal == nil || a.now == nil || a.rules == nil {
		return deny(DenialInvalidAuthority, action, workspace, "")
	}
	allowed, known := a.rules[action]
	if action == "" || action == "*" || !known {
		return deny(DenialUnknownOperation, action, workspace, "")
	}
	value, concreteClass, ok := unpackAuthority(authority)
	if !ok || value.seal == nil || value.seal != a.seal || value.class != concreteClass {
		return deny(DenialInvalidAuthority, action, workspace, concreteClass)
	}
	if _, ok := allowed[concreteClass]; !ok {
		return deny(DenialWrongClass, action, workspace, concreteClass)
	}
	if value.action != action {
		return deny(DenialWrongAction, action, workspace, concreteClass)
	}
	if workspace == "" || value.workspace != workspace {
		return deny(DenialWrongWorkspace, action, workspace, concreteClass)
	}
	if value.expiresAt.IsZero() || !a.now().Before(value.expiresAt) {
		return deny(DenialExpired, action, workspace, concreteClass)
	}
	return nil
}

// RequireOperator applies admission to a concrete OperatorAuthority. Keeping
// this method typed prevents capability APIs from accidentally accepting a
// generic authority union for operator-only commands.
func (a *Admission) RequireOperator(action Action, workspace string, authority OperatorAuthority) error {
	return a.Admit(action, workspace, authority)
}

// RequireExecution applies admission to a concrete ExecutionAuthority. The
// capability must additionally compare ResourceKind/ResourceID and the owner
// tuple with its command or freshly loaded durable record.
func (a *Admission) RequireExecution(action Action, workspace string, authority ExecutionAuthority) error {
	return a.Admit(action, workspace, authority)
}

// RequireSystem applies admission to a concrete SystemAuthority. Keeping this
// method typed prevents internal capability APIs from treating system callers
// as a generic superuser or accepting another authority class accidentally.
func (a *Admission) RequireSystem(action Action, workspace string, authority SystemAuthority) error {
	return a.Admit(action, workspace, authority)
}

func deny(reason DenialReason, action Action, workspace string, class Class) error {
	return &AdmissionError{Reason: reason, Action: action, Workspace: workspace, Class: class}
}

func unpackAuthority(authority Authority) (grant, Class, bool) {
	switch value := authority.(type) {
	case OperatorAuthority:
		return value.grant, ClassOperator, true
	case ExecutionAuthority:
		return value.grant, ClassExecution, true
	case SessionAuthority:
		return value.grant, ClassSession, true
	case WebhookAuthority:
		return value.grant, ClassWebhook, true
	case SystemAuthority:
		return value.grant, ClassSystem, true
	default:
		return grant{}, "", false
	}
}
