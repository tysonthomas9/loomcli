package agentprovisioning

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

var (
	ErrInvalid           = errors.New("agent provisioning: invalid")
	ErrConflict          = errors.New("agent provisioning: conflict")
	ErrNotFound          = errors.New("agent provisioning: not found")
	ErrUnavailable       = errors.New("agent provisioning: unavailable")
	ErrConcurrentWrite   = errors.New("agent provisioning: concurrent write")
	ErrInvalidTransition = errors.New("agent provisioning: invalid transition")
	ErrPermanentFailure  = errors.New("agent provisioning: permanent failure")
)

const ActionBeginProvisioning authority.Action = "agentprovisioning.begin"

// OperationRules is the complete default-deny authority registry for
// AgentProvisioning commands.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.OperatorOnly(ActionBeginProvisioning),
	}
}

// Commands is the request-facing AgentProvisioning surface. Begin durably
// records the complete immutable intent; Run converges that exact intent and
// is safe to replay after a lost response or process restart.
type Commands interface {
	Begin(context.Context, authority.OperatorAuthority, Spec) (*Record, error)
	Run(context.Context, string, string) (*Record, error)
}

// ProgressStore is the process manager's sole owned durable port. Begin sends
// only the immutable, secret-free intent. The durable backend derives the
// authenticated requester, canonicalizes set-like fields, computes the
// fingerprint, stamps timestamps/version, and applies first-writer-wins
// semantics by provisioning ID. Save is version-CAS. ListPending returns only
// pending, running, and retryable-failed records; completed and
// permanent-failed records are terminal.
type ProgressStore interface {
	Begin(context.Context, Spec, string) (*Record, error)
	Get(context.Context, string, string) (*Record, error)
	Save(context.Context, *Record, int64) (*Record, error)
	ListPending(context.Context, string, int) ([]*Record, error)
}

type EnsureRoleCommand struct {
	CommandID                string
	WorkspaceKey             string
	ProvisioningID           string
	ProvisioningGenerationID string
	Role                     RoleSpec
}

type EnsureAgentCommand struct {
	CommandID                string
	WorkspaceKey             string
	ProvisioningID           string
	ProvisioningGenerationID string
	Agent                    AgentSpec
}

type EnsureBindingCommand struct {
	CommandID                string
	WorkspaceKey             string
	ProvisioningID           string
	ProvisioningGenerationID string
	AgentID                  string
	Binding                  BindingSpec
}

type EnsureGrantCommand struct {
	CommandID                string
	WorkspaceKey             string
	ProvisioningID           string
	ProvisioningGenerationID string
	BindingID                string
	Grant                    GrantSpec
}

type RoleOperations interface {
	EnsureRole(context.Context, EnsureRoleCommand) error
}

type AgentOperations interface {
	EnsureAgent(context.Context, EnsureAgentCommand) error
}

type BindingOperations interface {
	EnsureBinding(context.Context, EnsureBindingCommand) error
}

type GrantOperations interface {
	EnsureGrant(context.Context, EnsureGrantCommand) error
}

// FaultInjector is test-only composition used to prove that every externally
// committed step can be replayed after a process crash.
type FaultInjector interface {
	AfterExternalCommit(Step, string) error
}
