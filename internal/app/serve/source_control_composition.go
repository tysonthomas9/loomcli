// Source Control composition assembles the Phase 5 Source Control and
// Connectors checkout boundary inside the serve application root.
package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	infralocalgit "github.com/tysonthomas9/loomcli/internal/infra/localgit"
	infrastackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	connectorsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/connectors/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	sourceControlMaterializerComponentID = "source-control-materializer"
)

// SourceControlCapability is the composition-owned handle for the minimal
// Phase 5 checkout materializer. Consumers receive only the Source Control API;
// the credential source and Connectors broker remain private.
type SourceControlCapability struct {
	api      sourcecontrol.API
	issuer   *authority.Issuer
	now      func() time.Time
	outcomes sourcecontrol.TaskOutcomeRecorder
	stacks   sourcecontrol.StackLifecycle
}

type Materializer = sourcecontrol.Materializer
type RepositoryAdmissionMaterializer = sourcecontrol.RepositoryAdmissionMaterializer
type RepositoryResolver = sourcecontrol.RepositoryResolver
type RepositoryAdmissionCheckoutCommand = sourcecontrol.RepositoryAdmissionCheckoutCommand
type PreparedRepositoryCheckout = sourcecontrol.PreparedRepositoryCheckout
type TaskCheckoutCommand = sourcecontrol.TaskCheckoutCommand
type TaskCheckout = sourcecontrol.TaskCheckout
type PullRequestCheckoutCommand = sourcecontrol.PullRequestCheckoutCommand
type PullRequestCheckout = sourcecontrol.PullRequestCheckout

var ErrUnavailable = sourcecontrol.ErrUnavailable

func (capability *SourceControlCapability) SourceControlAPI() sourcecontrol.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

// SourceControlMaterializer exposes the authority-free application workflow.
// Callers cannot recover the owner API, issuer, Connectors broker, or
// credential source from this interface.
func (capability *SourceControlCapability) SourceControlMaterializer() sourcecontrol.Materializer {
	if capability == nil {
		return nil
	}
	return capability
}

var _ sourcecontrol.Materializer = (*SourceControlCapability)(nil)
var _ sourcecontrol.RepositoryAdmissionMaterializer = (*SourceControlCapability)(nil)

// RepositoryAdmissionMaterializer exposes only the trusted Workspace
// pre-admission workflow. Task, PR, and web request adapters receive the
// narrower Materializer above and cannot register a remote projection.
func (capability *SourceControlCapability) RepositoryAdmissionMaterializer() sourcecontrol.RepositoryAdmissionMaterializer {
	if capability == nil {
		return nil
	}
	return capability
}

func (capability *SourceControlCapability) PrepareRepositoryAdmissionCheckout(
	ctx context.Context,
	command sourcecontrol.RepositoryAdmissionCheckoutCommand,
) (*sourcecontrol.PreparedRepositoryCheckout, error) {
	if capability == nil || capability.api == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	if err := sourcecontrol.ValidateRepositoryAdmissionCheckoutCommand(command); err != nil {
		return nil, err
	}
	return capability.prepareRepositoryCheckout(
		ctx,
		sourcecontrol.RepositoryAdmissionCheckoutCommand{
			WorkspaceKey:      command.WorkspaceKey,
			AdmissionID:       command.AdmissionID,
			RepositoryRef:     command.RepositoryRef,
			OwnerID:           command.OwnerID,
			OwnerGenerationID: command.OwnerGenerationID,
			SpecFingerprint:   command.SpecFingerprint,
		},
	)
}

func (capability *SourceControlCapability) prepareRepositoryCheckout(
	ctx context.Context,
	command sourcecontrol.RepositoryAdmissionCheckoutCommand,
) (*sourcecontrol.PreparedRepositoryCheckout, error) {
	if capability == nil || capability.api == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	if err := sourcecontrol.ValidateRepositoryAdmissionCheckoutCommand(command); err != nil {
		return nil, err
	}
	materializationID, err := sourcecontrol.RepositoryAdmissionMaterializationID(
		command,
	)
	if err != nil {
		return nil, err
	}
	materialization, err := capability.materialize(
		ctx,
		command.WorkspaceKey,
		materializationID,
		command.RepositoryRef,
		"admit workspace repository "+command.RepositoryRef,
	)
	if err != nil {
		return nil, err
	}
	if materialization.WorkspaceKey != command.WorkspaceKey ||
		materialization.MaterializationID != materializationID ||
		materialization.RepositoryRef != command.RepositoryRef ||
		strings.TrimSpace(materialization.CheckoutPath) == "" {
		return nil, fmt.Errorf(
			"%w: repository admission returned different coordinates",
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	return &sourcecontrol.PreparedRepositoryCheckout{
		WorkspaceKey:  materialization.WorkspaceKey,
		AdmissionID:   command.AdmissionID,
		RepositoryRef: materialization.RepositoryRef,
		CheckoutPath:  materialization.CheckoutPath,
		Reused:        materialization.Reused,
	}, nil
}

func (capability *SourceControlCapability) PrepareTaskCheckout(
	ctx context.Context,
	command sourcecontrol.TaskCheckoutCommand,
) (*sourcecontrol.TaskCheckout, error) {
	if capability == nil || capability.api == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	if err := sourcecontrol.ValidateTaskCheckoutCommand(command); err != nil {
		return nil, err
	}
	identity := stableSourceControlCoordinate(
		"task-run",
		command.WorkspaceKey,
		command.TaskRunID,
		command.RepositoryRef,
	)
	materialization, err := capability.materialize(
		ctx,
		command.WorkspaceKey,
		"task-run:"+identity+":checkout",
		command.RepositoryRef,
		"prepare task run "+command.TaskRunID+" repository checkout",
	)
	if err != nil {
		return nil, err
	}
	result := &sourcecontrol.TaskCheckout{
		WorkspaceKey: command.WorkspaceKey, TaskRunID: command.TaskRunID,
		RepositoryRef: command.RepositoryRef, CheckoutPath: materialization.CheckoutPath,
	}
	destination := "refs/loom/task-runs/" + identity + "/base"
	fetched, err := capability.fetchRef(
		ctx,
		sourcecontrol.FetchRefCommand{
			WorkspaceKey: command.WorkspaceKey, OperationID: "task-run:" + identity + ":base",
			RepositoryRef: command.RepositoryRef,
			SourceRef:     "refs/heads/" + command.BaseBranch, DestinationRef: destination,
		},
		"prepare task run "+command.TaskRunID+" base ref",
	)
	if err != nil {
		return nil, err
	}
	if fetched.CheckoutPath != materialization.CheckoutPath {
		return nil, fmt.Errorf("%w: task checkout path changed between materialize and fetch", sourcecontrol.ErrInvalidMaterialization)
	}
	result.BaseRef = fetched.DestinationRef
	result.BaseCommit = fetched.CommitSHA
	return result, nil
}

//nolint:funlen // Checkout preparation keeps authority, fetched-ref verification, and cleanup ordering in one auditable flow.
func (capability *SourceControlCapability) PreparePullRequestCheckout(
	ctx context.Context,
	command sourcecontrol.PullRequestCheckoutCommand,
) (*sourcecontrol.PullRequestCheckout, error) {
	if capability == nil || capability.api == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	if err := sourcecontrol.ValidatePullRequestCheckoutCommand(command); err != nil {
		return nil, err
	}
	headCommit := strings.ToLower(command.HeadCommit)
	checkoutIdentity := stableSourceControlCoordinate(
		"pr-checkout",
		command.WorkspaceKey,
		command.ReviewID,
		command.RepositoryRef,
	)
	subjectIdentity := stableSourceControlCoordinate(
		"pr-subject",
		command.WorkspaceKey,
		command.ReviewID,
		command.RepositoryRef,
		strconv.Itoa(command.Number),
		headCommit,
		command.BaseBranch,
	)
	materialization, err := capability.materialize(
		ctx,
		command.WorkspaceKey,
		"pr-review:"+checkoutIdentity+":checkout",
		command.RepositoryRef,
		"prepare PR review "+command.ReviewID+" repository checkout",
	)
	if err != nil {
		return nil, err
	}
	headDestination := "refs/loom/pr-reviews/" + subjectIdentity + "/head"
	head, err := capability.fetchRef(
		ctx,
		sourcecontrol.FetchRefCommand{
			WorkspaceKey: command.WorkspaceKey, OperationID: "pr-review:" + subjectIdentity + ":head",
			RepositoryRef:  command.RepositoryRef,
			SourceRef:      "refs/pull/" + strconv.Itoa(command.Number) + "/head",
			DestinationRef: headDestination, ExpectedCommit: headCommit,
		},
		"prepare PR review "+command.ReviewID+" head",
	)
	if err != nil {
		return nil, err
	}
	baseDestination := "refs/loom/pr-reviews/" + subjectIdentity + "/base"
	base, err := capability.fetchRef(
		ctx,
		sourcecontrol.FetchRefCommand{
			WorkspaceKey: command.WorkspaceKey, OperationID: "pr-review:" + subjectIdentity + ":base",
			RepositoryRef:  command.RepositoryRef,
			SourceRef:      "refs/heads/" + command.BaseBranch,
			DestinationRef: baseDestination,
		},
		"prepare PR review "+command.ReviewID+" base",
	)
	if err != nil {
		return nil, err
	}
	if head.CheckoutPath != materialization.CheckoutPath ||
		base.CheckoutPath != materialization.CheckoutPath {
		return nil, fmt.Errorf("%w: PR checkout path changed between materialize and fetch", sourcecontrol.ErrInvalidMaterialization)
	}
	return &sourcecontrol.PullRequestCheckout{
		WorkspaceKey: command.WorkspaceKey, ReviewID: command.ReviewID,
		RepositoryRef: command.RepositoryRef, CheckoutPath: materialization.CheckoutPath,
		HeadRef: head.DestinationRef, HeadCommit: head.CommitSHA,
		BaseRef: base.DestinationRef, BaseCommit: base.CommitSHA,
	}, nil
}

func (capability *SourceControlCapability) materialize(
	ctx context.Context,
	workspace string,
	operationID string,
	repositoryRef string,
	reason string,
) (*sourcecontrol.Materialization, error) {
	auth, err := capability.issueSourceControlAuthority(
		workspace,
		sourcecontrol.ActionMaterializeWorkspace,
		reason,
	)
	if err != nil {
		return nil, err
	}
	return capability.api.MaterializeWorkspace(ctx, auth, sourcecontrol.MaterializeCommand{
		WorkspaceKey: workspace, MaterializationID: operationID, RepositoryRef: repositoryRef,
	})
}

func (capability *SourceControlCapability) fetchRef(
	ctx context.Context,
	command sourcecontrol.FetchRefCommand,
	reason string,
) (*sourcecontrol.FetchedRef, error) {
	auth, err := capability.issueSourceControlAuthority(
		command.WorkspaceKey,
		sourcecontrol.ActionFetchRepositoryRef,
		reason,
	)
	if err != nil {
		return nil, err
	}
	return capability.api.FetchRepositoryRef(ctx, auth, command)
}

func stableSourceControlCoordinate(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(kind))
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// NewSourceControlCapability composes the minimal Source Control and
// Connectors seam against the server's private issuer. Local Settings is read
// just in time by gitauth; no plaintext credential is retained here.
func NewSourceControlCapability(
	localSettingsDir string,
	repositories sourcecontrol.RepositoryResolver,
	issuer *authority.Issuer,
) (*SourceControlCapability, error) {
	if issuer == nil {
		return nil, fmt.Errorf("compose Source Control authority: Workflow Catalog authority is unavailable")
	}
	capability, err := newSourceControlCapability(
		issuer,
		gitauth.NewLocalSettingsSource(localSettingsDir),
		repositories,
		infralocalgit.Inspector{},
		time.Now,
	)
	if capability != nil {
		capability.outcomes, capability.stacks = newDefaultStackServices(time.Now)
	}
	return capability, err
}

// NewSourceControlCapabilityWithFleetDB composes the complete Phase 5
// Source Control + Connectors boundary. The Git-only constructor above remains
// for isolated materializer tests; production AgentProvisioning requires this
// complete grant-capable composition.
func NewSourceControlCapabilityWithFleetDB(
	localSettingsDir string,
	repositories sourcecontrol.RepositoryResolver,
	client *infrafleetdb.Client,
	issuer *authority.Issuer,
) (*SourceControlCapability, error) {
	if issuer == nil {
		return nil, fmt.Errorf("compose Source Control authority: Workflow Catalog authority is unavailable")
	}
	grantAdapter, err := connectorsfleetdb.New(newConnectorsFleetDBTransport(client))
	if err != nil {
		return nil, fmt.Errorf("compose Connectors grant adapter: %w", err)
	}
	capability, err := newSourceControlCapabilityWithGrants(
		issuer,
		gitauth.NewLocalSettingsSource(localSettingsDir),
		repositories,
		infralocalgit.Inspector{},
		grantAdapter,
		time.Now,
	)
	if capability != nil {
		capability.outcomes, capability.stacks = newDefaultStackServices(time.Now)
	}
	return capability, err
}

func newDefaultStackServices(now func() time.Time) (sourcecontrol.TaskOutcomeRecorder, sourcecontrol.StackLifecycle) {
	store, err := infrastackstore.Default()
	if err != nil {
		return nil, nil
	}
	outcomes, err := sourcecontrol.NewTaskOutcomes(store, now)
	if err != nil {
		return nil, nil
	}
	stacks, err := sourcecontrol.NewStackLifecycle(store, now)
	if err != nil {
		return nil, nil
	}
	return outcomes, stacks
}

func newSourceControlCapability(
	issuer *authority.Issuer,
	credentialSource gitauth.Source,
	repositories sourcecontrol.RepositoryResolver,
	inspector sourcecontrol.CheckoutInspector,
	now func() time.Time,
) (*SourceControlCapability, error) {
	return composeSourceControlCapability(
		issuer,
		credentialSource,
		repositories,
		inspector,
		nil,
		now,
	)
}

func newSourceControlCapabilityWithGrants(
	issuer *authority.Issuer,
	credentialSource gitauth.Source,
	repositories sourcecontrol.RepositoryResolver,
	inspector sourcecontrol.CheckoutInspector,
	grants connectors.ConnectorGrantStore,
	now func() time.Time,
) (*SourceControlCapability, error) {
	if grants == nil {
		return nil, fmt.Errorf("compose Source Control Connectors grants: %w", connectors.ErrUnavailable)
	}
	return composeSourceControlCapability(
		issuer,
		credentialSource,
		repositories,
		inspector,
		grants,
		now,
	)
}

func composeSourceControlCapability(
	issuer *authority.Issuer,
	credentialSource gitauth.Source,
	repositories sourcecontrol.RepositoryResolver,
	inspector sourcecontrol.CheckoutInspector,
	grants connectors.ConnectorGrantStore,
	now func() time.Time,
) (*SourceControlCapability, error) {
	if issuer == nil || repositories == nil || inspector == nil || now == nil {
		return nil, fmt.Errorf("compose Source Control: issuer, repository resolver, checkout inspector, and clock are required")
	}
	connectorsAdmission, err := issuer.NewAdmission(connectors.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Connectors admission: %w", err)
	}
	var connectorsService *connectors.Service
	if grants == nil {
		connectorsService, err = connectors.New(infralocalgit.New(credentialSource), connectorsAdmission)
	} else {
		connectorsService, err = connectors.NewWithGrants(
			infralocalgit.New(credentialSource),
			grants,
			connectorsAdmission,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("compose Connectors Git broker: %w", err)
	}
	broker, err := sourcecontrol.NewConnectorsGitReadBroker(
		connectorsService,
		&sourceControlBrokerAuthorityProvider{issuer: issuer, now: now},
	)
	if err != nil {
		return nil, err
	}
	sourceControlAdmission, err := issuer.NewAdmission(sourcecontrol.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Source Control admission: %w", err)
	}
	service, err := sourcecontrol.New(repositories, broker, inspector, sourceControlAdmission)
	if err != nil {
		return nil, err
	}
	return &SourceControlCapability{
		api:    service,
		issuer: issuer, now: now,
	}, nil
}

// issueMaterializeAuthority is intentionally private to composition. It
// cannot mint another Source Control or Connectors action.
//
//nolint:unparam // The workspace is a production authority scope; focused tests currently use one fixed workspace.
func (capability *SourceControlCapability) issueMaterializeAuthority(
	workspace string,
	reason string,
) (authority.SystemAuthority, error) {
	return capability.issueSourceControlAuthority(
		workspace,
		sourcecontrol.ActionMaterializeWorkspace,
		reason,
	)
}

func (capability *SourceControlCapability) issueSourceControlAuthority(
	workspace string,
	action authority.Action,
	reason string,
) (authority.SystemAuthority, error) {
	if capability == nil || capability.issuer == nil || capability.now == nil {
		return authority.SystemAuthority{}, sourcecontrol.ErrUnavailable
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf("source control authority scope and reason are required: %w", authority.ErrInvalidScope)
	}
	if action != sourcecontrol.ActionMaterializeWorkspace &&
		action != sourcecontrol.ActionFetchRepositoryRef {
		return authority.SystemAuthority{}, fmt.Errorf("source control authority action is not registered: %w", authority.ErrActionNotAllowed)
	}
	principal, err := capability.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: sourceControlMaterializerComponentID, Class: authority.ClassSystem,
		Workspace: workspace, Actions: []authority.Action{action},
		ExpiresAt: capability.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return capability.issuer.IssueSystem(
		principal,
		workspace,
		action,
		reason,
	)
}

type sourceControlBrokerAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ sourcecontrol.GitReadAuthorityProvider = (*sourceControlBrokerAuthorityProvider)(nil)

func (provider *sourceControlBrokerAuthorityProvider) AuthorityForGitRead(
	ctx context.Context,
	workspace string,
	reason string,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, connectors.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("connectors broker authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf("connectors broker authority scope and reason are required: %w", authority.ErrInvalidScope)
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: sourceControlMaterializerComponentID, Class: authority.ClassSystem,
		Workspace: workspace, Actions: []authority.Action{connectors.ActionExecuteGitRead},
		ExpiresAt: provider.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return provider.issuer.IssueSystem(
		principal,
		workspace,
		connectors.ActionExecuteGitRead,
		reason,
	)
}

type connectorsFleetDBTransport struct {
	grants infrafleetdb.ConnectorGrantTransport
}

var _ connectorsfleetdb.Transport = (*connectorsFleetDBTransport)(nil)

func newConnectorsFleetDBTransport(client *infrafleetdb.Client) connectorsfleetdb.Transport {
	if client == nil {
		return nil
	}
	return &connectorsFleetDBTransport{grants: client.ConnectorGrantCommands()}
}

func (transport *connectorsFleetDBTransport) CreateConnectorGrant(
	ctx context.Context,
	input connectorsfleetdb.CreateConnectorGrantWire,
) (*connectorsfleetdb.ConnectorGrantWire, error) {
	value, err := transport.grants.CreateConnectorGrant(ctx, infrafleetdb.ConnectorGrantCreateCommand{
		WorkspaceKey: input.WorkspaceKey, GrantID: input.GrantID,
		ConnectorID: input.ConnectorID, BindingID: input.BindingID,
		Action: input.Action, ResourcePattern: input.ResourcePattern,
	})
	return connectorGrantWire(value), translateConnectorsFleetDBError(err)
}

func (transport *connectorsFleetDBTransport) ListConnectorGrants(
	ctx context.Context,
	workspace string,
	filter connectorsfleetdb.ConnectorGrantFilterWire,
) ([]*connectorsfleetdb.ConnectorGrantWire, error) {
	values, err := transport.grants.ListConnectorGrantsByBinding(ctx, workspace, filter.BindingID)
	if err != nil {
		return nil, translateConnectorsFleetDBError(err)
	}
	out := make([]*connectorsfleetdb.ConnectorGrantWire, len(values))
	for index, value := range values {
		out[index] = connectorGrantWire(value)
	}
	return out, nil
}

func connectorGrantWire(value *infrafleetdb.ConnectorGrantRecord) *connectorsfleetdb.ConnectorGrantWire {
	if value == nil {
		return nil
	}
	return &connectorsfleetdb.ConnectorGrantWire{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
		CreatedAt: value.CreatedAt, RevokedAt: cloneConnectorTime(value.RevokedAt),
	}
}

func cloneConnectorTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func translateConnectorsFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrConnectorGrantNotFound):
		translated = connectorsfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrConnectorGrantInvalid):
		translated = connectorsfleetdb.ErrTransportInvalid
	case errors.Is(err, infrafleetdb.ErrConnectorGrantConflict):
		translated = connectorsfleetdb.ErrTransportConflict
	default:
		translated = connectorsfleetdb.ErrTransportUnavailable
	}
	return errors.Join(translated, err)
}

var _ sourcecontrol.TaskOutcomeRecorder = (*SourceControlCapability)(nil)
var _ sourcecontrol.StackBindingResolver = (*SourceControlCapability)(nil)

func (capability *SourceControlCapability) RecordTaskOutcome(
	ctx context.Context,
	command sourcecontrol.TaskOutcomeCommand,
) (bool, error) {
	if capability == nil || capability.outcomes == nil {
		return false, nil
	}
	return capability.outcomes.RecordTaskOutcome(ctx, command)
}

func (capability *SourceControlCapability) ResolveTaskStackBinding(
	ctx context.Context,
	workspace,
	repository,
	taskID string,
) (sourcecontrol.TaskStackBinding, bool, error) {
	if capability == nil || capability.stacks == nil {
		return sourcecontrol.TaskStackBinding{}, false, nil
	}
	return capability.stacks.ResolveTaskStackBinding(ctx, workspace, repository, taskID)
}
