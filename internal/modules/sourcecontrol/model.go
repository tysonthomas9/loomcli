package sourcecontrol

// MaterializeCommand contains only opaque Workspace-owned references. The
// public Source Control API does not accept a remote URL, so embedded URL
// credentials cannot cross this boundary even as a rejected request.
type MaterializeCommand struct {
	WorkspaceKey      string `json:"workspace_key"`
	MaterializationID string `json:"materialization_id"`
	RepositoryRef     string `json:"repository_ref"`
}

// Materialization is a credential-free local checkout receipt.
type Materialization struct {
	WorkspaceKey      string `json:"workspace_key"`
	MaterializationID string `json:"materialization_id"`
	RepositoryRef     string `json:"repository_ref"`
	CheckoutPath      string `json:"checkout_path"`
	Reused            bool   `json:"reused"`
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
	WorkspaceKey   string `json:"workspace_key"`
	OperationID    string `json:"operation_id"`
	RepositoryRef  string `json:"repository_ref"`
	SourceRef      string `json:"source_ref"`
	DestinationRef string `json:"destination_ref"`
	ExpectedCommit string `json:"expected_commit,omitempty"`
}

// FetchedRef is a credential-free, postcondition-verified receipt.
type FetchedRef struct {
	WorkspaceKey   string `json:"workspace_key"`
	OperationID    string `json:"operation_id"`
	RepositoryRef  string `json:"repository_ref"`
	CheckoutPath   string `json:"checkout_path"`
	RemoteName     string `json:"remote_name"`
	SourceRef      string `json:"source_ref"`
	DestinationRef string `json:"destination_ref"`
	CommitSHA      string `json:"commit_sha"`
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
