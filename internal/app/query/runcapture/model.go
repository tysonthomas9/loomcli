// Package runcapture composes immutable evidence views from lifecycle-owner
// snapshots and Artifacts. It owns no state and exposes no write port.
package runcapture

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
)

var (
	ErrInvalid               = errors.New("run capture: invalid request")
	ErrNotFound              = errors.New("run capture: not found")
	ErrUnavailable           = errors.New("run capture: unavailable")
	ErrInvalidPersistedState = errors.New("run capture: invalid persisted state")
	ErrEvidenceCorrupt       = errors.New("run capture: evidence corrupt")
)

type OwnerKind string

const (
	OwnerExecution   OwnerKind = "execution"
	OwnerInteraction OwnerKind = "interaction"
)

type EvidenceState string

const (
	EvidenceMissing            EvidenceState = "missing"
	EvidencePending            EvidenceState = "pending"
	EvidenceFinalized          EvidenceState = "finalized"
	EvidenceTruncated          EvidenceState = "truncated"
	EvidenceCaptureFailed      EvidenceState = "capture_failed"
	EvidenceContentUnavailable EvidenceState = "content_unavailable"
	EvidenceCorrupt            EvidenceState = "corrupt"
)

type Query struct {
	WorkspaceKey string
	OwnerKind    OwnerKind
	OwnerID      string
	AgentID      string
	WorkItemID   string
}

type ArchiveQuery struct {
	WorkspaceKey string
	OwnerKind    OwnerKind
	OwnerID      string
	AgentID      string
	WorkItemID   string
	Limit        int
}

type RunCapture struct {
	WorkspaceKey string
	OwnerKind    OwnerKind
	OwnerID      string
	AgentID      string
	WorkItemID   string
	Status       string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Evidence     []Evidence
}

type Evidence struct {
	Kind            artifacts.EvidenceKind
	ArtifactID      string
	State           EvidenceState
	MIMEType        string
	SizeBytes       int64
	ContentHash     string
	RedactionStatus string
	Truncated       bool
	FailureClass    string
	Artifact        *artifacts.Artifact
}

type TranscriptEvidence struct {
	Capture  *RunCapture
	Evidence Evidence
	Events   []transcript.Event
}

type API interface {
	Get(context.Context, Query) (*RunCapture, error)
	List(context.Context, ArchiveQuery) ([]*RunCapture, error)
	Transcript(context.Context, Query) (*TranscriptEvidence, error)
}
