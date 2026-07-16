package taskrunapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// artifactResult is the camelCase wire view of a task-run artifact.
type artifactResult struct {
	WorkspaceKey    string            `json:"workspaceKey,omitempty"`
	ArtifactID      string            `json:"artifactId"`
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

func artifactResultFromDomain(artifact *domain.Artifact) artifactResult {
	if artifact == nil {
		return artifactResult{}
	}
	return artifactResult{
		WorkspaceKey:    artifact.WorkspaceKey,
		ArtifactID:      artifact.ArtifactID,
		TaskID:          artifact.TaskID,
		OwnerType:       artifact.OwnerType,
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
		DurableStatus:   artifact.DurableStatus,
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
	params, err := decodeParams[artifactDeclareParams](body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Type) == "" {
		return nil, fmt.Errorf("artifact type required: %w", domain.ErrInvalid)
	}
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	durableStatus := strings.TrimSpace(params.DurableStatus)
	if durableStatus == "" {
		durableStatus = "declared"
	}
	taskID := strings.TrimSpace(params.TaskID)
	if taskID == "" {
		taskID = run.TaskID
	}
	artifactID := strings.TrimSpace(params.ArtifactID)
	if artifactID == "" {
		artifactID = generatedArtifactID(id.TaskRunID)
	}
	artifact, err := m.store.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:    ws,
		ArtifactID:      artifactID,
		TaskID:          taskID,
		OwnerType:       taskRunOwnerType,
		OwnerID:         id.TaskRunID,
		Type:            params.Type,
		URI:             params.URI,
		Summary:         params.Summary,
		MIMEType:        params.MIMEType,
		SizeBytes:       params.SizeBytes,
		Checksum:        params.Checksum,
		ContentHash:     params.ContentHash,
		Visibility:      params.Visibility,
		RedactionStatus: params.RedactionStatus,
		DurableStatus:   durableStatus,
		Metadata:        params.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("declare artifact: %w", err)
	}
	return artifactResultFromDomain(artifact), nil
}

func (m *Module) artifactGet(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		ArtifactID string `json:"artifactId"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyLease(ctx, ws, id); err != nil {
		return nil, err
	}
	artifact, err := m.ownedArtifact(ctx, ws, id, params.ArtifactID)
	if err != nil {
		return nil, err
	}
	return artifactResultFromDomain(artifact), nil
}

func (m *Module) artifactList(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Type          string `json:"type"`
		DurableStatus string `json:"durableStatus"`
		Limit         int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyLease(ctx, ws, id); err != nil {
		return nil, err
	}
	artifacts, err := m.store.Artifacts().List(ctx, ws, store.ArtifactFilter{
		OwnerType: taskRunOwnerType,
		OwnerID:   id.TaskRunID,
		Type:      params.Type,
		Status:    params.DurableStatus,
		Limit:     params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	results := make([]artifactResult, 0, len(artifacts))
	for _, artifact := range artifacts {
		results = append(results, artifactResultFromDomain(artifact))
	}
	return map[string]any{"artifacts": results}, nil
}

// artifactFinalizeParams is the artifact-finalize request body. Pointer
// fields distinguish "leave unchanged" from explicit values, mirroring the
// store's ArtifactFinalize patch semantics.
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
	params, err := decodeParams[artifactFinalizeParams](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyLease(ctx, ws, id); err != nil {
		return nil, err
	}
	if _, err := m.ownedArtifact(ctx, ws, id, params.ArtifactID); err != nil {
		return nil, err
	}
	artifact, err := m.store.Artifacts().Finalize(ctx, ws, strings.TrimSpace(params.ArtifactID), store.ArtifactFinalize{
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
		return nil, fmt.Errorf("finalize artifact: %w", err)
	}
	return artifactResultFromDomain(artifact), nil
}

// handleArtifactContent is the raw-body artifact content upload route.
func (m *Module) handleArtifactContent(w http.ResponseWriter, r *http.Request) {
	id, ok := authenticate(w, r)
	if !ok {
		return
	}
	ws := r.PathValue("ws")
	artifactID := strings.TrimSpace(r.PathValue("artifactId"))
	if _, err := m.verifyLease(r.Context(), ws, id); err != nil {
		writeDomainOpError(w, err)
		return
	}
	if _, err := m.ownedArtifact(r.Context(), ws, id, artifactID); err != nil {
		writeDomainOpError(w, err)
		return
	}
	content, err := readAll(w, r, maxArtifactContentBytes)
	if err != nil {
		writeOpError(w, http.StatusBadRequest, "invalid", "read artifact content: "+err.Error(), false)
		return
	}
	artifact, err := m.store.Artifacts().UploadContent(r.Context(), ws, artifactID, store.ArtifactContentUpload{
		Body:     bytes.NewReader(content),
		MIMEType: r.Header.Get("Content-Type"),
	})
	if err != nil {
		writeDomainOpError(w, fmt.Errorf("upload artifact content: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, artifactResultFromDomain(artifact))
}

// ownedArtifact loads the artifact and proves it belongs to the caller's
// task run; foreign artifacts read as not-found so the route does not leak
// other owners' artifact existence.
func (m *Module) ownedArtifact(ctx context.Context, ws string, id leaseIdentity, artifactID string) (*domain.Artifact, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil, fmt.Errorf("artifactId required: %w", domain.ErrInvalid)
	}
	artifact, err := m.store.Artifacts().Get(ctx, ws, artifactID)
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	if artifact.OwnerType != taskRunOwnerType || artifact.OwnerID != id.TaskRunID {
		return nil, fmt.Errorf("artifact %q does not belong to task run %q: %w", artifactID, id.TaskRunID, domain.ErrNotFound)
	}
	return artifact, nil
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
