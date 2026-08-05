// Package interactioncomposition assembles the Phase 5 Interaction
// capability from owner-scoped ports. The parent serve package retains only
// the public facade that shares the Workflow Catalog authority seal.
package interactioncomposition

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/serve/operatorauth"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// InteractionCapability is the narrow composition-owned Interaction handle.
// It exposes typed commands, session-authority derivation, and runtime
// registrations without exposing the authority issuer or persistence ports.
type InteractionCapability struct {
	api              interaction.API
	chatAPI          interaction.ChatAPI
	chatMessenger    interaction.ChatMessenger
	sessionAuthority InteractionSessionAuthorityResolver
	inboxDelivery    interaction.InboxEnqueuer
	forceInterrupter interaction.ForceInterrupter
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	runtime          []platformruntime.Registration
	issuer           *authority.Issuer
}

func (capability *InteractionCapability) InteractionAPI() interaction.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

func (capability *InteractionCapability) ChatAPI() interaction.ChatAPI {
	if capability == nil {
		return nil
	}
	return capability.chatAPI
}

func (capability *InteractionCapability) ChatMessenger() interaction.ChatMessenger {
	if capability == nil {
		return nil
	}
	return capability.chatMessenger
}

func (capability *InteractionCapability) SessionAuthorityResolver() InteractionSessionAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.sessionAuthority
}

func (capability *InteractionCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

func (capability *InteractionCapability) InboxEnqueuer() interaction.InboxEnqueuer {
	if capability == nil {
		return nil
	}
	return capability.inboxDelivery
}

func (capability *InteractionCapability) ForceInterrupter() interaction.ForceInterrupter {
	if capability == nil {
		return nil
	}
	return capability.forceInterrupter
}

func (capability *InteractionCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), capability.runtime...)
}

// InteractionDependencies are owner-scoped ports only. Production must
// provide implementations backed by FleetDB's compound owner-fenced commands;
// legacy independent session/lease/terminal/inbox routes are not valid
// substitutes.
type InteractionDependencies struct {
	Sessions         interaction.SessionStore
	Transcripts      interaction.TranscriptArtifactStore
	Terminals        interaction.TerminalStore
	Inbox            interaction.InboxStore
	Activity         interaction.ActivitySource
	SessionAuthority interaction.SessionAuthorityValidator
	WorkspaceLister  interaction.RuntimeWorkspaceLister
}

type InteractionConfig struct {
	WorkspaceKey                    string
	WorkspaceLister                 interaction.RuntimeWorkspaceLister
	ExternalAuth                    bool
	ExternalOperatorResolverFactory operatorauth.ExternalOperatorResolverFactory
}

type RuntimeWorkspaceLister = interaction.RuntimeWorkspaceLister
type ChatRuntime = interaction.ChatRuntime

// NewInteractionCapability composes the always-on Interaction capability for
// profiles where Workflow Catalog is disabled.
func NewInteractionCapability(
	config InteractionConfig,
	dependencies InteractionDependencies,
) (*InteractionCapability, error) {
	return newInteractionCapability(config, dependencies, authority.NewIssuer())
}

// NewInteractionCapabilityWithIssuer composes the complete capability against
// the same issuer seal as the other active serve-hosted capability modules.
func NewInteractionCapabilityWithIssuer(
	config InteractionConfig,
	dependencies InteractionDependencies,
	issuer *authority.Issuer,
) (*InteractionCapability, error) {
	if issuer == nil {
		return nil, fmt.Errorf("compose Interaction authority: Workflow Catalog authority is unavailable")
	}
	return newInteractionCapability(config, dependencies, issuer)
}

//nolint:funlen // Composition validates and binds the complete Interaction authority and runtime dependency set atomically.
func newInteractionCapability(
	config InteractionConfig,
	dependencies InteractionDependencies,
	issuer *authority.Issuer,
) (*InteractionCapability, error) {
	if issuer == nil {
		return nil, fmt.Errorf("compose Interaction authority: issuer is unavailable")
	}
	if dependencies.Sessions == nil || dependencies.Transcripts == nil || dependencies.Terminals == nil || dependencies.Inbox == nil ||
		dependencies.Activity == nil ||
		dependencies.SessionAuthority == nil ||
		(config.WorkspaceKey == "" &&
			firstInteractionWorkspaceLister(config.WorkspaceLister, dependencies.WorkspaceLister) == nil) {
		return nil, fmt.Errorf(
			"compose Interaction: atomic persistence, activity, authority validation, and runtime workspace ports are required: %w",
			interaction.ErrUnavailable,
		)
	}
	admission, err := issuer.NewAdmission(interaction.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Interaction admission: %w", err)
	}
	service, err := interaction.New(
		dependencies.Sessions,
		dependencies.Transcripts,
		dependencies.Terminals,
		dependencies.Inbox,
		dependencies.Activity,
		admission,
		time.Now,
	)
	if err != nil {
		return nil, err
	}
	sessionAuthority := newInteractionSessionAuthorityResolver(
		dependencies.SessionAuthority,
		issuer,
		time.Now,
	)
	if sessionAuthority == nil {
		return nil, fmt.Errorf("compose Interaction session authority: %w", interaction.ErrUnavailable)
	}
	operatorResolver, err := composeInteractionOperatorResolver(config, issuer)
	if err != nil {
		return nil, err
	}
	runtimeAuthority := newInteractionRuntimeAuthorityProvider(issuer, time.Now)
	registration, err := interaction.RuntimeRegistration(
		service,
		runtimeAuthority,
		interaction.RuntimeConfig{
			WorkspaceKey:    config.WorkspaceKey,
			WorkspaceLister: firstInteractionWorkspaceLister(config.WorkspaceLister, dependencies.WorkspaceLister),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("compose Interaction session recovery: %w", err)
	}
	inboxDelivery := newInteractionInboxEnqueuer(service, runtimeAuthority)
	if inboxDelivery == nil {
		return nil, fmt.Errorf("compose Interaction inbox delivery: %w", interaction.ErrUnavailable)
	}
	forceInterrupter := newInteractionForceInterrupter(service, runtimeAuthority)
	if forceInterrupter == nil {
		return nil, fmt.Errorf("compose Interaction terminal lifecycle: %w", interaction.ErrUnavailable)
	}
	return &InteractionCapability{
		api: service, sessionAuthority: sessionAuthority,
		inboxDelivery:    inboxDelivery,
		forceInterrupter: forceInterrupter,
		operatorResolver: operatorResolver,
		runtime:          []platformruntime.Registration{registration},
		issuer:           issuer,
	}, nil
}

func composeInteractionOperatorResolver(
	config InteractionConfig,
	issuer *authority.Issuer,
) (workflowcataloghttp.OperatorAuthorityResolver, error) {
	if config.ExternalAuth {
		if config.ExternalOperatorResolverFactory == nil {
			return nil, fmt.Errorf("compose Interaction external authorization: operator resolver factory is required")
		}
		resolver := config.ExternalOperatorResolverFactory(issuer, interaction.ErrNotOwner)
		if resolver == nil {
			return nil, fmt.Errorf("compose Interaction external authorization: operator resolver is unavailable")
		}
		return resolver, nil
	}
	resolver, err := operatorauth.NewLocalOpenOperatorResolver(
		issuer,
		interaction.ActionStartSession,
		interaction.ActionRecoverStart,
		interaction.ActionEnqueueInbox,
		interaction.ActionReadActivity,
		interaction.ActionDeliverChatMessage,
		interaction.ActionDeliverAssignment,
		interaction.ActionReadConversation,
	)
	if err != nil {
		return nil, fmt.Errorf("compose Interaction local open authority: %w", err)
	}
	return resolver, nil
}

func firstInteractionWorkspaceLister(
	values ...interaction.RuntimeWorkspaceLister,
) interaction.RuntimeWorkspaceLister {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
