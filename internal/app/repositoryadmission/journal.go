package repositoryadmission

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

var (
	ErrLocalNotFound = errors.New("local repository admission not found")
	ErrLocalConflict = errors.New("local repository admission conflict")
)

type Kind string

const (
	KindCreateWorkspace Kind = "create_workspace"
	KindAddRepositories Kind = "add_repositories"
)

// LocalIntent is the machine-local, credential-free half of a durable
// admission. It contains only placement and cleanup facts needed to resume the
// exact operation after restart.
type LocalIntent struct {
	OperationID    string   `json:"operation_id"`
	WorkspaceKey   string   `json:"workspace_key"`
	WorkspaceName  string   `json:"workspace_name,omitempty"`
	WorkspacePath  string   `json:"workspace_path"`
	Kind           Kind     `json:"kind"`
	Branch         string   `json:"branch,omitempty"`
	CloneURLs      []string `json:"clone_urls,omitempty"`
	LocalRepoPaths []string `json:"local_repo_paths,omitempty"`
}

type LocalRecord struct {
	Intent               LocalIntent `json:"intent"`
	AdmissionID          string      `json:"admission_id,omitempty"`
	PreviousAdmissionIDs []string    `json:"previous_admission_ids,omitempty"`
	SpecFingerprint      string      `json:"spec_fingerprint,omitempty"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
}

// Coordinate binds local materialization authority to one exact durable owner
// generation. No transport credential or reusable capability is present.
type Coordinate struct {
	WorkspaceKey      string
	AdmissionID       string
	OperationID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
}

type LocalProjection struct {
	WorkspaceKey      string
	AdmissionID       string
	OperationID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
	WorkspacePath     string
}

// Journal persists only machine-local recovery facts and ephemeral
// materialization authority. Durable workflow status remains in FleetDB.
type Journal interface {
	Prepare(context.Context, LocalIntent) (LocalRecord, error)
	Bind(context.Context, string, string, string) (LocalRecord, error)
	// Rebind replaces definitively missing durable coordinates with one exact
	// successor using a compare-and-swap over the prior admission and
	// fingerprint. Previous IDs remain lookup aliases for accepted UI jobs.
	Rebind(context.Context, string, string, string, string, string) (LocalRecord, error)
	GetByOperation(context.Context, string) (LocalRecord, error)
	GetByAdmission(context.Context, string) (LocalRecord, error)
	List(context.Context) ([]LocalRecord, error)
	Remove(context.Context, string) error
	AcquireMaterializationLock(context.Context, string, []string) (func(), error)
	ActivateMaterializationAuthority(Coordinate, time.Time) error
	RenewMaterializationAuthority(Coordinate, time.Time) bool
	DeactivateMaterializationAuthority(Coordinate)
	DeactivateAllMaterializationAuthorities()
	ResolveLocal(context.Context, string) (LocalProjection, error)
}

// ValidateTokenFreeRemote applies Source Control's canonical remote policy
// before a local recovery fact can be persisted.
func ValidateTokenFreeRemote(remote string) (string, error) {
	return sourcecontrol.ValidateTokenFreeRemote(remote)
}

func NormalizeID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 32 {
		return "", errors.New("repository admission ID must be 32 lowercase hex characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 || strings.ToLower(value) != value {
		return "", errors.New("repository admission ID must be 32 lowercase hex characters")
	}
	return value, nil
}
