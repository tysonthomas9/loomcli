package sourcecontrol

import "time"

// MaterializeCommand contains only opaque Workspace-owned references. The
// public Source Control API does not accept a remote URL, so embedded URL
// credentials cannot cross this boundary even as a rejected request.
type MaterializeCommand struct {
	WorkspaceKey      string
	MaterializationID string
	RepositoryRef     string
}

// Materialization is a credential-free local checkout receipt.
type Materialization struct {
	WorkspaceKey      string
	MaterializationID string
	RepositoryRef     string
	CheckoutPath      string
	Reused            bool
}

// CheckoutMatch is the complete result of comparing an existing target with
// the expected token-free remote. The observed remote never crosses the port.
type CheckoutMatch string

const (
	CheckoutMissing  CheckoutMatch = "missing"
	CheckoutMatched  CheckoutMatch = "matched"
	CheckoutConflict CheckoutMatch = "conflict"
)

// RepositoryCheckout is the trusted Workspace-reference projection consumed
// through RepositoryResolver. It is not part of the Source Control API. Its
// remote is independently required to be token-free at this boundary and
// again at the Connectors boundary.
type RepositoryCheckout struct {
	WorkspaceKey  string
	RepositoryRef string
	RemoteURL     string
	RemoteName    string
	WorkspacePath string
	CheckoutName  string
}

// GitCloneRequest is the exact credential-free request Source Control sends
// to its Connectors broker adapter.
type GitCloneRequest struct {
	WorkspaceKey  string
	OperationID   string
	RepositoryRef string
	RemoteURL     string
	RemoteName    string
	WorkspacePath string
	TargetPath    string
}

// GitCloneReceipt echoes the bounded operation coordinates without exposing
// provider authority.
type GitCloneReceipt struct {
	WorkspaceKey  string
	OperationID   string
	RepositoryRef string
	TargetPath    string
}

// FetchRefCommand identifies one exact remote read and one private
// Source-Control-owned destination ref. Remote URL, checkout paths, and
// credentials are all resolved behind the repository port.
type FetchRefCommand struct {
	WorkspaceKey   string
	OperationID    string
	RepositoryRef  string
	SourceRef      string
	DestinationRef string
	ExpectedCommit string
}

// FetchedRef is a credential-free, postcondition-verified receipt.
type FetchedRef struct {
	WorkspaceKey   string
	OperationID    string
	RepositoryRef  string
	CheckoutPath   string
	RemoteName     string
	SourceRef      string
	DestinationRef string
	CommitSHA      string
}

// GitFetchRequest is the exact credential-free request Source Control sends
// to Connectors for a single forced ref update.
type GitFetchRequest struct {
	WorkspaceKey   string
	OperationID    string
	RepositoryRef  string
	RemoteURL      string
	WorkspacePath  string
	TargetPath     string
	RemoteName     string
	SourceRef      string
	DestinationRef string
}

// GitFetchReceipt echoes every bounded fetch coordinate.
type GitFetchReceipt struct {
	WorkspaceKey   string
	OperationID    string
	RepositoryRef  string
	TargetPath     string
	RemoteName     string
	SourceRef      string
	DestinationRef string
}

// RepositoryAdmissionCheckoutCommand is the Workspace-only admission
// workflow used after FleetDB has durably reserved the complete repository
// batch. The owner coordinates bind local Git side effects to the exact live
// FleetDB generation without exposing a remote URL, checkout path, provider
// credential, or reusable mutation authority.
type RepositoryAdmissionCheckoutCommand struct {
	WorkspaceKey      string
	AdmissionID       string
	RepositoryRef     string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
}

// PreparedRepositoryCheckout is the credential-free receipt returned to
// Workspace after Source Control has verified the exact local checkout.
type PreparedRepositoryCheckout struct {
	WorkspaceKey  string
	AdmissionID   string
	RepositoryRef string
	CheckoutPath  string
	Reused        bool
}

// RepositoryAdmissionLocalProjection is the machine-local half of one
// durable FleetDB admission. FleetDB owns the token-free repository spec and
// all process state; this projection owns the checkout root plus the live,
// process-local owner-generation authority that cannot be reconstructed on
// another machine.
type RepositoryAdmissionLocalProjection struct {
	WorkspaceKey      string
	OperationID       string
	AdmissionID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
	WorkspacePath     string
}

// TaskCheckoutCommand is the durable task-run coordinate accepted by the
// authority-free application materializer.
type TaskCheckoutCommand struct {
	WorkspaceKey  string
	TaskRunID     string
	RepositoryRef string
	BaseBranch    string
}

// TaskCheckout is ready for local-only worktree creation.
type TaskCheckout struct {
	WorkspaceKey  string
	TaskRunID     string
	RepositoryRef string
	CheckoutPath  string
	BaseRef       string
	BaseCommit    string
}

// PullRequestCheckoutCommand identifies one immutable PR subject. HeadCommit
// is required and is rechecked after fetch; BaseBranch is fetched separately.
type PullRequestCheckoutCommand struct {
	WorkspaceKey  string
	ReviewID      string
	RepositoryRef string
	Number        int
	HeadCommit    string
	BaseBranch    string
}

// PullRequestCheckout contains only local refs and verified commit IDs.
type PullRequestCheckout struct {
	WorkspaceKey  string
	ReviewID      string
	RepositoryRef string
	CheckoutPath  string
	HeadRef       string
	HeadCommit    string
	BaseRef       string
	BaseCommit    string
}

// TaskStack is the Source-Control-owned projection needed to locate one task's
// stack without exposing the local stackstore representation to callers.
type TaskStack struct {
	StackID      string
	WorkspaceKey string
	Repository   string
}

type TaskStackNode struct {
	TaskID string
}

type TaskOutcomeState string

const (
	TaskOutcomePublished TaskOutcomeState = "published"
	TaskOutcomeEmpty     TaskOutcomeState = "empty"
)

// TaskStackOutcomeMutation is the exact lineage state transition derived from
// trusted runner delivery evidence by Source Control.
type TaskStackOutcomeMutation struct {
	State       TaskOutcomeState
	OutputSHA   string
	PublishedAt *time.Time
}

// StackID identifies a Source Control stack. The conventional forms are
// "epic:<id>", "manual:<name>", and "auto:<scope>".
type StackID = string

// CommitMode controls how a task's work becomes the commit on its output branch.
type CommitMode = string

const (
	CommitModeLoom   CommitMode = "loom_commit"
	CommitModeAgent  CommitMode = "agent_commit"
	CommitModeSquash CommitMode = "squash_on_publish"
)

// NodeState is the lifecycle state of one task slot in a stack.
type NodeState = string

const (
	NodeStatePending    NodeState = "pending"
	NodeStatePublished  NodeState = "published"
	NodeStateConflicted NodeState = "conflicted"
	NodeStateEmpty      NodeState = "empty"
	NodeStateMerged     NodeState = "merged"
	NodeStateClosed     NodeState = "closed"
)

// Stack is Source Control's canonical transport-neutral stack-lineage model.
// Persistence and delivery adapters map it to their independently versioned
// wire shapes.
type Stack struct {
	ID                StackID
	WorkspaceKey      string
	Repository        string
	RootBase          string
	DefaultCommitMode CommitMode
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// StackNode is Source Control's canonical model for one stable task slot.
type StackNode struct {
	StackID         StackID
	TaskID          string
	BaseTaskID      string
	OutputBranch    string
	CommitMode      CommitMode
	State           NodeState
	PRNumber        int
	PRURL           string
	OutputSHA       string
	LastPublishedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type EnsureStackCommand struct {
	WorkspaceKey      string
	StackID           string
	Repository        string
	RootBase          string
	DefaultCommitMode string
}

type AddStackNodeCommand struct {
	WorkspaceKey string
	StackID      string
	TaskID       string
	AfterTaskID  string
	Root         bool
	CommitMode   string
}

type MoveStackNodeCommand struct {
	WorkspaceKey string
	StackID      string
	TaskID       string
	AfterTaskID  string
}

type SetStackNodeBaseCommand struct {
	WorkspaceKey string
	StackID      string
	TaskID       string
	BaseTaskID   string
}

type RemoveStackNodeCommand struct {
	WorkspaceKey string
	StackID      string
	TaskID       string
}

// DesiredStackNode is a caller-computed topology input. Source Control owns
// the idempotent reconciliation and stable output-branch preservation.
type DesiredStackNode struct {
	TaskID     string
	BaseTaskID string
}

type ReconcileStackCommand struct {
	Stack EnsureStackCommand
	Nodes []DesiredStackNode
}

type StackLineage struct {
	StackID      string
	BaseRef      string
	OutputBranch string
}

type ReconcileStackResult struct {
	Stack      Stack
	Nodes      []StackNode
	Created    []string
	Reparented []string
	Lineage    map[string]StackLineage
}

type StackPublicationState string

const (
	StackPublicationPublished StackPublicationState = "published"
	StackPublicationMerged    StackPublicationState = "merged"
	StackPublicationEmpty     StackPublicationState = "empty"
)

// RecordStackNodePublicationCommand is the bounded publication outcome a
// Source Control publisher may record. Source Control supplies the timestamp
// and persistence mapping.
type RecordStackNodePublicationCommand struct {
	WorkspaceKey string
	StackID      string
	TaskID       string
	State        StackPublicationState
	PRNumber     int
	PRURL        string
	OutputSHA    string
}

type StackNodePublicationMutation struct {
	State       StackPublicationState
	PRNumber    int
	PRURL       string
	OutputSHA   string
	PublishedAt *time.Time
}

// TaskStackBinding is the exact stack materialization input Execution needs
// before creating a task worktree.
type TaskStackBinding struct {
	StackID      string
	BaseRef      string
	OutputBranch string
}
