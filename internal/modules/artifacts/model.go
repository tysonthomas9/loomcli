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
	WorkspaceKey    string
	ArtifactID      string
	AgentID         string
	SessionID       string
	TaskID          string
	OwnerType       OwnerType
	OwnerID         string
	Type            string
	URI             string
	Summary         string
	MIMEType        string
	SizeBytes       int64
	Checksum        string
	ContentHash     string
	Visibility      string
	RedactionStatus string
	DurableStatus   DurableStatus
	Metadata        map[string]string
	FinalizedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
