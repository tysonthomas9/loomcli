package artifacts

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionDeclare   authority.Action = "artifacts.declare"
	ActionUpload    authority.Action = "artifacts.upload"
	ActionFinalize  authority.Action = "artifacts.finalize"
	ActionFail      authority.Action = "artifacts.fail"
	ActionReference authority.Action = "artifacts.reference"
	ActionGet       authority.Action = "artifacts.get"
	ActionList      authority.Action = "artifacts.list"
)

// OperationRules is the complete default-deny Artifacts operation registry.
// Execution-owned artifacts use ExecutionAuthority for the full lifecycle;
// interactive session transcripts use SessionAuthority only for their
// declare/get/upload/finalize lifecycle. References and task-run list queries
// remain execution-only.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(ActionDeclare, authority.ClassExecution, authority.ClassSession),
		authority.Allow(ActionUpload, authority.ClassExecution, authority.ClassSession),
		authority.Allow(ActionFinalize, authority.ClassExecution, authority.ClassSession),
		authority.Allow(ActionFail, authority.ClassExecution, authority.ClassSession),
		authority.Allow(ActionReference, authority.ClassExecution),
		authority.Allow(ActionGet, authority.ClassExecution, authority.ClassSession),
		authority.Allow(ActionList, authority.ClassExecution),
	}
}

// ExecutionOwner is the exact task-run resource and fenced lease envelope
// authorized to act on an artifact. LeaseToken is credential material and is
// deliberately excluded from any wire representation or result type.
//
// The durable port must validate the entire tuple. The module validates shape
// and binds artifact ownership; a shared FleetDB service credential alone is
// never accepted as execution authority.
type ExecutionOwner struct {
	WorkspaceKey string `json:"-"`
	TaskRunID    string `json:"-"`
	NodeID       string `json:"-"`
	LeaseID      string `json:"-"`
	LeaseToken   string `json:"-"`
	FencingToken int64  `json:"-"`
}

// CreateCommand declares one task-run-owned artifact. Owner fields are absent
// by design: the service derives them exclusively from ExecutionOwner.
type CreateCommand struct {
	ArtifactID      string
	AgentID         string
	SessionID       string
	TaskID          string
	Type            string
	URI             string
	Summary         string
	MIMEType        string
	SizeBytes       int64
	Checksum        string
	ContentHash     string
	Visibility      string
	RedactionStatus string
	Metadata        map[string]string
}

// UploadCommand writes content to a declared artifact.
type UploadCommand struct {
	ArtifactID string
	Content    []byte
	MIMEType   string
}

// FinalizeCommand seals an artifact. Pointer fields preserve the distinction
// between leaving a value unchanged and explicitly setting its zero value.
type FinalizeCommand struct {
	ArtifactID      string
	URI             *string
	Summary         *string
	MIMEType        *string
	SizeBytes       *int64
	Checksum        *string
	ContentHash     *string
	Visibility      *string
	RedactionStatus *string
	Metadata        *map[string]string
}

type FailCommand struct {
	ArtifactID     string
	FailureClass   string
	FailureMessage string
	Metadata       map[string]string
}

// ReferenceCommand creates one immutable association from a finalized
// Artifact to a deterministic task-run output target.
type ReferenceCommand struct {
	ArtifactID string
	Kind       string
	TargetRef  string
}

// GetQuery resolves one Artifact within the execution owner envelope without
// changing Artifact state or recording a durable reference.
type GetQuery struct {
	ArtifactID string
}

// ListFilter lists Artifacts owned by the exact execution. It is the narrow
// runner compatibility query, not the general artifact UI/search API.
type ListFilter struct {
	Type          string
	DurableStatus DurableStatus
	Limit         int
}

// ContentAuthorities carries one independently issued authority per durable
// operation in CreateContent. Reusing one action grant for another lifecycle
// step is intentionally impossible.
type ContentAuthorities struct {
	Declare   authority.ExecutionAuthority
	Get       authority.ExecutionAuthority
	Upload    authority.ExecutionAuthority
	Finalize  authority.ExecutionAuthority
	Fail      authority.ExecutionAuthority
	Reference authority.ExecutionAuthority
}

type ContentResult struct {
	Artifact  *Artifact
	Reference *ArtifactReference
}

// SessionOwner binds one session-produced Artifact to the exact validated
// Interaction lease generation that requested it. The raw lease credential is
// intentionally absent: Interaction retains it for the subsequent atomic
// session-reference update.
type SessionOwner struct {
	WorkspaceKey string `json:"-"`
	SessionID    string `json:"-"`
	AgentID      string `json:"-"`
	NodeID       string `json:"-"`
	LeaseID      string `json:"-"`
	FencingToken int64  `json:"-"`
}

// SessionContentCommand describes one canonical session-produced content
// artifact. Ownership fields are absent and are derived from SessionOwner.
type SessionContentCommand struct {
	ArtifactID      string
	TaskID          string
	Type            string
	Summary         string
	MIMEType        string
	Visibility      string
	RedactionStatus string
	Metadata        map[string]string
	Content         []byte
}

// SessionContentAuthorities carries one independently issued session
// authority per durable operation. A publish authority cannot be replayed as
// a different lifecycle action.
type SessionContentAuthorities struct {
	Declare  authority.SessionAuthority
	Get      authority.SessionAuthority
	Upload   authority.SessionAuthority
	Finalize authority.SessionAuthority
	Fail     authority.SessionAuthority
}

// SessionAPI is the session-scoped Artifacts surface consumed by Interaction.
// It deliberately does not expose generic CRUD, references, or task-run
// queries.
type SessionAPI interface {
	CreateContent(context.Context, SessionContentAuthorities, SessionOwner, SessionContentCommand) (*Artifact, error)
}

// QueryAPI is the general read-only Artifacts surface consumed by product
// projections. Metadata reads are workspace-scoped; callers must validate the
// returned owner tuple before requesting bytes for a task or session view.
type QueryAPI interface {
	GetArtifact(context.Context, Query) (*Artifact, error)
	ListArtifacts(context.Context, SearchQuery) ([]*Artifact, error)
	ReadArtifactContent(context.Context, Query) ([]byte, error)
}

// API is the minimal Phase 4 Artifacts lifecycle surface.
type API interface {
	Create(context.Context, authority.ExecutionAuthority, ExecutionOwner, CreateCommand) (*Artifact, error)
	Upload(context.Context, authority.ExecutionAuthority, ExecutionOwner, UploadCommand) (*Artifact, error)
	Finalize(context.Context, authority.ExecutionAuthority, ExecutionOwner, FinalizeCommand) (*Artifact, error)
	Fail(context.Context, authority.ExecutionAuthority, ExecutionOwner, FailCommand) (*Artifact, error)
	Reference(context.Context, authority.ExecutionAuthority, ExecutionOwner, ReferenceCommand) (ReferenceResult, error)
	Get(context.Context, authority.ExecutionAuthority, ExecutionOwner, GetQuery) (*Artifact, error)
	List(context.Context, authority.ExecutionAuthority, ExecutionOwner, ListFilter) ([]*Artifact, error)
	CreateContent(context.Context, ContentAuthorities, ExecutionOwner, CreateCommand, []byte, ReferenceCommand) (ContentResult, error)
}
