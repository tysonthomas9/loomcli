package artifacts

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionDeclare   authority.Action = "artifacts.declare"
	ActionUpload    authority.Action = "artifacts.upload"
	ActionFinalize  authority.Action = "artifacts.finalize"
	ActionReference authority.Action = "artifacts.reference"
	ActionGet       authority.Action = "artifacts.get"
	ActionList      authority.Action = "artifacts.list"
)

// OperationRules is the complete default-deny Artifacts operation registry.
// Every public command and owner-fenced query requires an issuer-bound
// ExecutionAuthority for its exact action.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(ActionDeclare, authority.ClassExecution),
		authority.Allow(ActionUpload, authority.ClassExecution),
		authority.Allow(ActionFinalize, authority.ClassExecution),
		authority.Allow(ActionReference, authority.ClassExecution),
		authority.Allow(ActionGet, authority.ClassExecution),
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
	Reference authority.ExecutionAuthority
}

type ContentResult struct {
	Artifact  *Artifact
	Reference *ArtifactReference
}

// API is the minimal Phase 4 Artifacts lifecycle surface.
type API interface {
	Create(context.Context, authority.ExecutionAuthority, ExecutionOwner, CreateCommand) (*Artifact, error)
	Upload(context.Context, authority.ExecutionAuthority, ExecutionOwner, UploadCommand) (*Artifact, error)
	Finalize(context.Context, authority.ExecutionAuthority, ExecutionOwner, FinalizeCommand) (*Artifact, error)
	Reference(context.Context, authority.ExecutionAuthority, ExecutionOwner, ReferenceCommand) (ReferenceResult, error)
	Get(context.Context, authority.ExecutionAuthority, ExecutionOwner, GetQuery) (*Artifact, error)
	List(context.Context, authority.ExecutionAuthority, ExecutionOwner, ListFilter) ([]*Artifact, error)
	CreateContent(context.Context, ContentAuthorities, ExecutionOwner, CreateCommand, []byte, ReferenceCommand) (ContentResult, error)
}
