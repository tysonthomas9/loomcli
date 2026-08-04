package connector

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// Deprecated: connector authorization policy is owned by modules/connectors.
type DenyReason = connectorsmodule.GrantDenyReason

const (
	DenyReasonNoGrants           = connectorsmodule.GrantDenyNoGrants
	DenyReasonActionNotGranted   = connectorsmodule.GrantDenyActionNotGranted
	DenyReasonResourceNotGranted = connectorsmodule.GrantDenyResourceNotGranted
	DenyReasonGrantRevoked       = connectorsmodule.GrantDenyRevoked
)

// GrantDenied preserves legacy domain sentinel matching while the dispatcher
// and new callers consume the Connectors-owned structured denial directly.
// Deprecated: use connectors.GrantDenied.
type GrantDenied struct {
	BindingID string
	Action    string
	Resource  string
	Reason    DenyReason
}

func (denied *GrantDenied) Error() string {
	return fmt.Sprintf(
		"connector: grant denied for binding %q action %q resource %q: %s",
		denied.BindingID,
		denied.Action,
		denied.Resource,
		denied.Reason,
	)
}

func (denied *GrantDenied) Unwrap() []error {
	if denied.Reason == DenyReasonGrantRevoked {
		return []error{domain.ErrGrantDenied, domain.ErrGrantRevoked}
	}
	return []error{domain.ErrGrantDenied}
}

// Deprecated: use connectors.GrantAuthorization.
type Decision struct {
	Allowed bool
	GrantID string
	Denied  *GrantDenied
}

func (decision Decision) Err() error {
	if decision.Allowed {
		return nil
	}
	return decision.Denied
}

// Evaluate is a compatibility adapter over the Connectors-owned policy.
// Deprecated: use connectors.EvaluateGrantAuthorization.
func Evaluate(
	bindingID string,
	grants []*domain.ConnectorGrant,
	action string,
	resource string,
) Decision {
	owned := make([]*connectorsmodule.ConnectorGrant, 0, len(grants))
	for _, grant := range grants {
		owned = append(owned, ownerGrant(grant))
	}
	authorization := connectorsmodule.EvaluateGrantAuthorization(bindingID, owned, action, resource)
	decision := Decision{Allowed: authorization.Allowed, GrantID: authorization.GrantID}
	if authorization.Denied != nil {
		decision.Denied = &GrantDenied{
			BindingID: authorization.Denied.BindingID,
			Action:    authorization.Denied.Action,
			Resource:  authorization.Denied.Resource,
			Reason:    authorization.Denied.Reason,
		}
	}
	return decision
}

func ownerGrant(grant *domain.ConnectorGrant) *connectorsmodule.ConnectorGrant {
	if grant == nil {
		return nil
	}
	owned := &connectorsmodule.ConnectorGrant{
		WorkspaceKey:    grant.WorkspaceKey,
		GrantID:         grant.GrantID,
		ConnectorID:     grant.ConnectorID,
		BindingID:       grant.BindingID,
		Action:          grant.Action,
		ResourcePattern: grant.ResourcePattern,
		CreatedAt:       grant.CreatedAt,
	}
	if grant.RevokedAt != nil {
		revokedAt := *grant.RevokedAt
		owned.RevokedAt = &revokedAt
	}
	return owned
}

// Deprecated: use connectors.MatchGrantResource.
func MatchResource(pattern string, resource string) bool {
	return connectorsmodule.MatchGrantResource(pattern, resource)
}

// Deprecated: use connectors.IsIrreversibleAction.
func IsIrreversible(action string) bool {
	return connectorsmodule.IsIrreversibleAction(action)
}

// Deprecated: use connectors.RequiredActionPreconditions.
func RequiredPreconditions(action string) []string {
	return connectorsmodule.RequiredActionPreconditions(action)
}
