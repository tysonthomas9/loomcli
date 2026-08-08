// Interaction composition assembles the Phase 5 capability from owner-scoped
// ports and shares only the Workflow Catalog authority seal.
package serve

import (
	"context"
	"fmt"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"time"
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
	ExternalOperatorResolverFactory ExternalOperatorResolverFactory
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
	resolver, err := NewLocalOpenOperatorResolver(
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

type interactionForceInterrupter struct {
	commands    interaction.RuntimeForceInterruptAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.ForceInterrupter = (*interactionForceInterrupter)(nil)

func newInteractionForceInterrupter(
	commands interaction.RuntimeForceInterruptAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.ForceInterrupter {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionForceInterrupter{commands: commands, authorities: authorities}
}

func (interrupter *interactionForceInterrupter) ForceInterrupt(
	ctx context.Context,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	if interrupter == nil || interrupter.commands == nil || interrupter.authorities == nil {
		return interaction.ForceInterruptResult{}, interaction.ErrUnavailable
	}
	auth, err := interrupter.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.TerminalLifecycleComponentID,
		command.WorkspaceKey,
		interaction.ActionForceInterrupt,
	)
	if err != nil {
		return interaction.ForceInterruptResult{}, err
	}
	return interrupter.commands.ForceInterrupt(ctx, auth, command)
}

type interactionInboxEnqueuer struct {
	commands    interaction.RuntimeInboxAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.InboxEnqueuer = (*interactionInboxEnqueuer)(nil)

func newInteractionInboxEnqueuer(
	commands interaction.RuntimeInboxAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.InboxEnqueuer {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionInboxEnqueuer{commands: commands, authorities: authorities}
}

func (enqueuer *interactionInboxEnqueuer) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	if enqueuer == nil || enqueuer.commands == nil || enqueuer.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := enqueuer.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.InboxDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionEnqueueInbox,
	)
	if err != nil {
		return nil, err
	}
	return enqueuer.commands.EnqueueInboxAsSystem(ctx, auth, command)
}

// ComposeInteractionChat attaches Interaction's provider-neutral chat surface
// to an already composed capability. The runtime may depend on the
// capability's authority-free InboxEnqueuer, so this intentionally runs after
// the atomic session/inbox owner has been constructed.
func ComposeInteractionChat(
	capability *InteractionCapability,
	runtime interaction.ChatRuntime,
) error {
	if capability == nil || capability.issuer == nil || runtime == nil {
		return fmt.Errorf(
			"compose Interaction chat: capability issuer and runtime are required: %w",
			interaction.ErrUnavailable,
		)
	}
	if capability.chatAPI != nil || capability.chatMessenger != nil {
		return fmt.Errorf(
			"compose Interaction chat: chat surface is already composed: %w",
			interaction.ErrConflict,
		)
	}
	admission, err := capability.issuer.NewAdmission(
		interaction.ChatOperationRules()...,
	)
	if err != nil {
		return fmt.Errorf("compose Interaction chat admission: %w", err)
	}
	service, err := interaction.NewChat(runtime, admission)
	if err != nil {
		return err
	}
	authorities := newInteractionRuntimeAuthorityProvider(
		capability.issuer,
		time.Now,
	)
	messenger := newInteractionChatMessenger(service, authorities)
	if messenger == nil {
		return fmt.Errorf(
			"compose Interaction chat messenger: %w",
			interaction.ErrUnavailable,
		)
	}
	capability.chatAPI = service
	capability.chatMessenger = messenger
	return nil
}

type interactionChatMessenger struct {
	commands    interaction.RuntimeChatAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.ChatMessenger = (*interactionChatMessenger)(nil)

func newInteractionChatMessenger(
	commands interaction.RuntimeChatAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.ChatMessenger {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionChatMessenger{
		commands:    commands,
		authorities: authorities,
	}
}

func (messenger *interactionChatMessenger) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	if messenger == nil || messenger.commands == nil ||
		messenger.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := messenger.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.ChatDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionDeliverChatMessage,
	)
	if err != nil {
		return nil, err
	}
	return messenger.commands.DeliverChatMessageAsSystem(ctx, auth, command)
}

func (messenger *interactionChatMessenger) DeliverAssignment(
	ctx context.Context,
	command interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	if messenger == nil || messenger.commands == nil ||
		messenger.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := messenger.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.ChatDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionDeliverAssignment,
	)
	if err != nil {
		return nil, err
	}
	return messenger.commands.DeliverAssignmentAsSystem(ctx, auth, command)
}
