package taskrunapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// artifactResult is the camelCase wire view of a task-run artifact.
type artifactResult struct {
	WorkspaceKey    string            `json:"workspaceKey,omitempty"`
	ArtifactID      string            `json:"artifactId"`
	SessionID       string            `json:"sessionId,omitempty"`
	TaskID          string            `json:"taskId,omitempty"`
	OwnerType       string            `json:"ownerType,omitempty"`
	OwnerID         string            `json:"ownerId,omitempty"`
	Type            string            `json:"type"`
	URI             string            `json:"uri,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	MIMEType        string            `json:"mimeType,omitempty"`
	SizeBytes       int64             `json:"sizeBytes,omitempty"`
	Checksum        string            `json:"checksum,omitempty"`
	ContentHash     string            `json:"contentHash,omitempty"`
	Visibility      string            `json:"visibility,omitempty"`
	RedactionStatus string            `json:"redactionStatus,omitempty"`
	DurableStatus   string            `json:"durableStatus,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	FinalizedAt     *time.Time        `json:"finalizedAt,omitempty"`
	CreatedAt       time.Time         `json:"createdAt,omitzero"`
	UpdatedAt       time.Time         `json:"updatedAt,omitzero"`
}

func artifactResultFromModule(artifact *artifactsmodule.Artifact) artifactResult {
	if artifact == nil {
		return artifactResult{}
	}
	return artifactResult{
		WorkspaceKey:    artifact.WorkspaceKey,
		ArtifactID:      artifact.ArtifactID,
		SessionID:       artifact.SessionID,
		TaskID:          artifact.TaskID,
		OwnerType:       string(artifact.OwnerType),
		OwnerID:         artifact.OwnerID,
		Type:            artifact.Type,
		URI:             artifact.URI,
		Summary:         artifact.Summary,
		MIMEType:        artifact.MIMEType,
		SizeBytes:       artifact.SizeBytes,
		Checksum:        artifact.Checksum,
		ContentHash:     artifact.ContentHash,
		Visibility:      artifact.Visibility,
		RedactionStatus: artifact.RedactionStatus,
		DurableStatus:   string(artifact.DurableStatus),
		Metadata:        artifact.Metadata,
		FinalizedAt:     artifact.FinalizedAt,
		CreatedAt:       artifact.CreatedAt,
		UpdatedAt:       artifact.UpdatedAt,
	}
}

// artifactDeclareParams is the artifact-declare request body. Owner fields
// are NOT accepted from the client: the artifact is force-scoped to the
// verified task run.
type artifactDeclareParams struct {
	ArtifactID      string            `json:"artifactId"`
	SessionID       string            `json:"sessionId"`
	TaskID          string            `json:"taskId"`
	Type            string            `json:"type"`
	URI             string            `json:"uri"`
	Summary         string            `json:"summary"`
	MIMEType        string            `json:"mimeType"`
	SizeBytes       int64             `json:"sizeBytes"`
	Checksum        string            `json:"checksum"`
	ContentHash     string            `json:"contentHash"`
	Visibility      string            `json:"visibility"`
	RedactionStatus string            `json:"redactionStatus"`
	DurableStatus   string            `json:"durableStatus"`
	Metadata        map[string]string `json:"metadata"`
}

func (m *Module) artifactDeclare(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	service, err := m.artifactAPI()
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[artifactDeclareParams](body)
	if err != nil {
		return nil, err
	}
	artifactID := strings.TrimSpace(params.ArtifactID)
	if artifactID == "" {
		artifactID = generatedArtifactID(id.TaskRunID)
	}
	_, auth, err := m.taskRunAuthority(ctx, ws, artifactsmodule.ActionDeclare, id)
	if err != nil {
		return nil, fmt.Errorf("authorize artifact declare: %w", err)
	}
	artifact, err := service.Create(ctx, auth, artifactExecutionOwner(ws, id), artifactsmodule.CreateCommand{
		ArtifactID:      artifactID,
		SessionID:       params.SessionID,
		TaskID:          params.TaskID,
		Type:            params.Type,
		URI:             params.URI,
		Summary:         params.Summary,
		MIMEType:        params.MIMEType,
		SizeBytes:       params.SizeBytes,
		Checksum:        params.Checksum,
		ContentHash:     params.ContentHash,
		Visibility:      params.Visibility,
		RedactionStatus: params.RedactionStatus,
		Metadata:        params.Metadata,
	})
	if err != nil {
		return nil, artifactDomainError(err)
	}
	return artifactResultFromModule(artifact), nil
}

func (m *Module) artifactGet(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	service, err := m.artifactAPI()
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[struct {
		ArtifactID string `json:"artifactId"`
	}](body)
	if err != nil {
		return nil, err
	}
	_, auth, err := m.taskRunAuthority(ctx, ws, artifactsmodule.ActionGet, id)
	if err != nil {
		return nil, fmt.Errorf("authorize artifact get: %w", err)
	}
	artifact, err := service.Get(ctx, auth, artifactExecutionOwner(ws, id), artifactsmodule.GetQuery{ArtifactID: strings.TrimSpace(params.ArtifactID)})
	if err != nil {
		return nil, artifactDomainError(err)
	}
	return artifactResultFromModule(artifact), nil
}

func (m *Module) artifactList(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	service, err := m.artifactAPI()
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[struct {
		Type          string `json:"type"`
		DurableStatus string `json:"durableStatus"`
		Limit         int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	_, auth, err := m.taskRunAuthority(ctx, ws, artifactsmodule.ActionList, id)
	if err != nil {
		return nil, fmt.Errorf("authorize artifact list: %w", err)
	}
	artifacts, err := service.List(ctx, auth, artifactExecutionOwner(ws, id), artifactsmodule.ListFilter{
		Type: params.Type, DurableStatus: artifactsmodule.DurableStatus(params.DurableStatus), Limit: params.Limit,
	})
	if err != nil {
		return nil, artifactDomainError(err)
	}
	results := make([]artifactResult, 0, len(artifacts))
	for _, artifact := range artifacts {
		results = append(results, artifactResultFromModule(artifact))
	}
	return map[string]any{"artifacts": results}, nil
}

// artifactFinalizeParams is the artifact-finalize request body. Pointer
// fields distinguish "leave unchanged" from explicit values.
type artifactFinalizeParams struct {
	ArtifactID      string             `json:"artifactId"`
	URI             *string            `json:"uri"`
	Summary         *string            `json:"summary"`
	MIMEType        *string            `json:"mimeType"`
	SizeBytes       *int64             `json:"sizeBytes"`
	Checksum        *string            `json:"checksum"`
	ContentHash     *string            `json:"contentHash"`
	Visibility      *string            `json:"visibility"`
	RedactionStatus *string            `json:"redactionStatus"`
	Metadata        *map[string]string `json:"metadata"`
}

func (m *Module) artifactFinalize(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	service, err := m.artifactAPI()
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[artifactFinalizeParams](body)
	if err != nil {
		return nil, err
	}
	_, auth, err := m.taskRunAuthority(ctx, ws, artifactsmodule.ActionFinalize, id)
	if err != nil {
		return nil, fmt.Errorf("authorize artifact finalize: %w", err)
	}
	artifact, err := service.Finalize(ctx, auth, artifactExecutionOwner(ws, id), artifactsmodule.FinalizeCommand{
		ArtifactID:      strings.TrimSpace(params.ArtifactID),
		URI:             params.URI,
		Summary:         params.Summary,
		MIMEType:        params.MIMEType,
		SizeBytes:       params.SizeBytes,
		Checksum:        params.Checksum,
		ContentHash:     params.ContentHash,
		Visibility:      params.Visibility,
		RedactionStatus: params.RedactionStatus,
		Metadata:        params.Metadata,
	})
	if err != nil {
		return nil, artifactDomainError(err)
	}
	return artifactResultFromModule(artifact), nil
}

// handleArtifactContent is the raw-body artifact content upload route.
func (m *Module) handleArtifactContent(w http.ResponseWriter, r *http.Request) {
	id, ok := authenticate(w, r)
	if !ok {
		return
	}
	ws := r.PathValue("ws")
	artifactID := strings.TrimSpace(r.PathValue("artifactId"))
	service, err := m.artifactAPI()
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	owner := artifactExecutionOwner(ws, id)
	// Authorize and hide foreign ownership before accepting a potentially
	// large request body. Upload performs the owner command again after read.
	_, getAuth, err := m.taskRunAuthority(r.Context(), ws, artifactsmodule.ActionGet, id)
	if err != nil {
		writeDomainOpError(w, artifactDomainError(err))
		return
	}
	if _, err := service.Get(r.Context(), getAuth, owner, artifactsmodule.GetQuery{ArtifactID: artifactID}); err != nil {
		writeDomainOpError(w, artifactDomainError(err))
		return
	}
	content, err := readAll(w, r, maxArtifactContentBytes)
	if err != nil {
		writeOpError(w, http.StatusBadRequest, "invalid", "read artifact content: "+err.Error(), false)
		return
	}
	_, uploadAuth, err := m.taskRunAuthority(r.Context(), ws, artifactsmodule.ActionUpload, id)
	if err != nil {
		writeDomainOpError(w, artifactDomainError(err))
		return
	}
	artifact, err := service.Upload(r.Context(), uploadAuth, owner, artifactsmodule.UploadCommand{
		ArtifactID: artifactID,
		Content:    content,
		MIMEType:   r.Header.Get("Content-Type"),
	})
	if err != nil {
		writeDomainOpError(w, artifactDomainError(err))
		return
	}
	writeJSON(w, http.StatusOK, artifactResultFromModule(artifact))
}

func (m *Module) artifactAPI() (artifactsmodule.API, error) {
	if m == nil || m.artifacts == nil {
		return nil, artifactsmodule.ErrUnavailable
	}
	return m.artifacts, nil
}

func artifactExecutionOwner(workspace string, id leaseIdentity) artifactsmodule.ExecutionOwner {
	return artifactsmodule.ExecutionOwner{
		WorkspaceKey: workspace,
		TaskRunID:    id.TaskRunID,
		NodeID:       id.NodeID,
		LeaseID:      id.LeaseID,
		LeaseToken:   id.LeaseToken,
		FencingToken: id.FencingToken,
	}
}

func artifactDomainError(err error) error {
	if err == nil || errors.Is(err, errLeaseDenied) {
		return err
	}
	var mapped error
	switch {
	case errors.Is(err, artifactsmodule.ErrNotFound):
		mapped = persistence.ErrNotFound
	case errors.Is(err, artifactsmodule.ErrAlreadyExists):
		mapped = persistence.ErrAlreadyExists
	case errors.Is(err, artifactsmodule.ErrNotOwner):
		mapped = persistence.ErrNotOwner
	case errors.Is(err, artifactsmodule.ErrInvalidTransition):
		mapped = persistence.ErrInvalidTransition
	case errors.Is(err, artifactsmodule.ErrInvalid):
		mapped = persistence.ErrInvalid
	default:
		return err
	}
	return errors.Join(mapped, err)
}

func readAll(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
}

// generatedArtifactID mints an artifact ID for declares that omit one,
// scoped under the owning task run for traceability.
func generatedArtifactID(taskRunID string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "artifact-" + taskRunID + "-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("artifact-%s-%d", taskRunID, time.Now().UTC().UnixNano())
}
