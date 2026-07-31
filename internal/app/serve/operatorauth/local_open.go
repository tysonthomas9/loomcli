// Package operatorauth owns the shared local-open request authority adapter
// used by serve-hosted capabilities.
package operatorauth

import (
	"net/http"
	"strings"
	"time"

	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	// LocalOpenOperatorSubject is the durable audit subject used when endpoint
	// reachability is the deployment trust boundary.
	LocalOpenOperatorSubject = "local-open-operator"

	localOpenOperatorAuthorityTTL = time.Minute
)

type OperatorAuthorityResolver = workflowcataloghttp.OperatorAuthorityResolver

// ExternalOperatorResolverFactory keeps identity-middleware adaptation at the
// outer server boundary while a capability retains its authority issuer.
type ExternalOperatorResolverFactory func(
	*authority.Issuer,
	error,
) OperatorAuthorityResolver

// LocalOpenOperatorResolver derives one sealed, short-lived authority for one
// route-selected action. Request content cannot select or widen its scope.
type LocalOpenOperatorResolver struct {
	issuer  *authority.Issuer
	actions map[authority.Action]struct{}
}

func NewLocalOpenOperatorResolver(
	issuer *authority.Issuer,
	actions ...authority.Action,
) (*LocalOpenOperatorResolver, error) {
	if issuer == nil {
		return nil, authority.ErrInvalidIssuer
	}
	allowed := make(map[authority.Action]struct{}, len(actions))
	for _, action := range actions {
		if strings.TrimSpace(string(action)) == "" {
			return nil, authority.ErrActionNotAllowed
		}
		allowed[action] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, authority.ErrActionNotAllowed
	}
	return &LocalOpenOperatorResolver{issuer: issuer, actions: allowed}, nil
}

func (resolver *LocalOpenOperatorResolver) ResolveOperatorAuthority(
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	if resolver == nil || resolver.issuer == nil {
		return authority.OperatorAuthority{}, authority.ErrInvalidIssuer
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if _, ok := resolver.actions[action]; !ok {
		return authority.OperatorAuthority{}, authority.ErrActionNotAllowed
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   LocalOpenOperatorSubject,
		Class:     authority.ClassOperator,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(localOpenOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return resolver.issuer.IssueOperator(principal, workspace, action)
}
