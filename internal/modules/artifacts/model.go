package artifacts

import "time"

// OwnerType identifies the aggregate that produced an artifact. Execution and
// Interaction use distinct typed authority contracts even though both retain
// durable references to this Artifacts-owned aggregate.
type OwnerType string

const (
	OwnerTaskRun OwnerType = "task_run"
	OwnerSession OwnerType = "session"
)

// DurableStatus is the Artifacts-owned content lifecycle.
type DurableStatus string

const (
	StatusDeclared  DurableStatus = "declared"
	StatusUploading DurableStatus = "uploading"
	StatusFinalized DurableStatus = "finalized"
	StatusFailed    DurableStatus = "failed"
)

// Artifact is the transport-neutral Artifacts aggregate returned by public
// commands. Metadata is defensively copied at the module boundary.
type Artifact struct {
	WorkspaceKey    string            `json:"workspace_key"`
	ArtifactID      string            `json:"artifact_id"`
	AgentID         string            `json:"agent_id,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	TerminalID      string            `json:"terminal_id,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	OwnerType       OwnerType         `json:"owner_type,omitempty"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Type            string            `json:"type"`
	URI             string            `json:"uri,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	MIMEType        string            `json:"mime_type,omitempty"`
	SizeBytes       int64             `json:"size_bytes,omitempty"`
	Checksum        string            `json:"checksum,omitempty"`
	ContentHash     string            `json:"content_hash,omitempty"`
	Visibility      string            `json:"visibility,omitempty"`
	RedactionStatus string            `json:"redaction_status,omitempty"`
	DurableStatus   DurableStatus     `json:"durable_status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	FinalizedAt     *time.Time        `json:"finalized_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ArtifactReference is the immutable durable association committed by the
// owner-fenced reference command. ReferenceID is the backend idempotency
// identity and cannot be redirected to another Artifact or target.
type ArtifactReference struct {
	WorkspaceKey string
	ReferenceID  string
	ArtifactID   string
	OwnerType    OwnerType
	OwnerID      string
	Kind         string
	TargetRef    string
	CreatedAt    time.Time
}

type ReferenceResult struct {
	Artifact  *Artifact
	Reference *ArtifactReference
}

// Query identifies one Artifact within a workspace. Read-only callers never
// provide an execution lease or session credential; their consuming feature
// remains responsible for applying its own task/session visibility policy to
// the returned owner projection before requesting content.
type Query struct {
	WorkspaceKey string
	ArtifactID   string
}

// SearchFilter is the general read-only Artifacts query vocabulary used by UI
// projections. Every nonempty field is an exact filter and is revalidated on
// every returned row by the owner service.
type SearchFilter struct {
	AgentID       string
	SessionID     string
	TaskID        string
	OwnerType     OwnerType
	OwnerID       string
	Type          string
	DurableStatus DurableStatus
	Limit         int
}

type SearchQuery struct {
	WorkspaceKey string
	Filter       SearchFilter
}

func cloneArtifact(in *Artifact) *Artifact {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMetadata(in.Metadata)
	if in.FinalizedAt != nil {
		value := *in.FinalizedAt
		out.FinalizedAt = &value
	}
	return &out
}

func cloneArtifactReference(in *ArtifactReference) *ArtifactReference {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneReferenceResult(in ReferenceResult) ReferenceResult {
	return ReferenceResult{Artifact: cloneArtifact(in.Artifact), Reference: cloneArtifactReference(in.Reference)}
}

func cloneMetadata(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
