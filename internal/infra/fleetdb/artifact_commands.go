package fleetdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

var (
	ErrArtifactsNotFound          = errors.New("fleetdb: artifacts not found")
	ErrArtifactsInvalid           = errors.New("fleetdb: artifacts invalid request")
	ErrArtifactsConflict          = errors.New("fleetdb: artifacts conflict")
	ErrArtifactsNotOwner          = errors.New("fleetdb: artifacts not owner")
	ErrArtifactsInvalidTransition = errors.New("fleetdb: artifacts invalid transition")
	ErrArtifactsUnavailable       = errors.New("fleetdb: artifacts unavailable")
)

// ArtifactOwner is the complete TaskRun owner envelope presented to every
// owner-fenced Artifact command and query. LeaseToken is write-only credential
// material and is never included in JSON payloads, command IDs, or results.
type ArtifactOwner struct {
	WorkspaceKey string
	TaskRunID    string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
}

// Artifact is the low-level FleetDB Artifact snapshot. Revision is retained
// only inside the infrastructure transport so capability callers never need
// to coordinate FleetDB CAS values themselves.
type Artifact struct {
	WorkspaceKey    string            `json:"workspace_key"`
	ArtifactID      string            `json:"artifact_id"`
	AgentID         string            `json:"agent_id,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	TerminalID      string            `json:"terminal_id,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	OwnerType       string            `json:"owner_type,omitempty"`
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
	DurableStatus   string            `json:"durable_status"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Revision        uint64            `json:"revision"`
	FinalizedAt     *time.Time        `json:"finalized_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type ArtifactCreateCommand struct {
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

type ArtifactUploadCommand struct {
	ArtifactID string
	Content    []byte
	MIMEType   string
}

// ArtifactFinalizeCommand uses pointers to distinguish an explicit zero value
// from "preserve the current uploaded value". The transport resolves these
// fields after its owner-fenced read and sends one full revision-CAS command.
type ArtifactFinalizeCommand struct {
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

type ArtifactReferenceCommand struct {
	ArtifactID string
	Kind       string
	TargetRef  string
}

type ArtifactReference struct {
	WorkspaceKey string    `json:"workspace_key"`
	ReferenceID  string    `json:"reference_id"`
	ArtifactID   string    `json:"artifact_id"`
	OwnerType    string    `json:"owner_type"`
	OwnerID      string    `json:"owner_id"`
	Kind         string    `json:"kind"`
	TargetRef    string    `json:"target_ref"`
	CreatedAt    time.Time `json:"created_at"`
}

type ArtifactReferenceResult struct {
	Artifact  *Artifact
	Reference *ArtifactReference
	Replayed  bool
}

type ArtifactFilter struct {
	Type          string
	DurableStatus string
	Limit         int
}

// ArtifactTransport is the shared Client's narrow owner-fenced Artifacts
// surface. It deliberately hides FleetDB revision CAS and command receipts.
type ArtifactTransport interface {
	Create(context.Context, ArtifactOwner, ArtifactCreateCommand) (*Artifact, error)
	Upload(context.Context, ArtifactOwner, ArtifactUploadCommand) (*Artifact, error)
	Finalize(context.Context, ArtifactOwner, ArtifactFinalizeCommand) (*Artifact, error)
	Reference(context.Context, ArtifactOwner, ArtifactReferenceCommand) (ArtifactReferenceResult, error)
	Get(context.Context, ArtifactOwner, string) (*Artifact, error)
	List(context.Context, ArtifactOwner, ArtifactFilter) ([]*Artifact, error)
}

type artifactCommandStore struct{ client *Client }

var _ ArtifactTransport = (*artifactCommandStore)(nil)

type artifactCommandReceipt struct {
	WorkspaceKey       string    `json:"workspace_key"`
	CommandID          string    `json:"command_id"`
	ArtifactID         string    `json:"artifact_id"`
	CommandType        string    `json:"command_type"`
	RequestFingerprint string    `json:"request_fingerprint"`
	ArtifactRevision   uint64    `json:"artifact_revision"`
	ReferenceID        string    `json:"reference_id,omitempty"`
	CommittedAt        time.Time `json:"committed_at"`
}

type artifactCommandResult struct {
	Artifact  *Artifact               `json:"artifact"`
	Reference *ArtifactReference      `json:"reference,omitempty"`
	Receipt   *artifactCommandReceipt `json:"receipt"`
	Replayed  bool                    `json:"replayed"`
}

type artifactCreateRequest struct {
	CommandID       string            `json:"command_id"`
	TaskRunID       string            `json:"task_run_id"`
	NodeID          string            `json:"node_id"`
	LeaseID         string            `json:"lease_id"`
	FencingToken    int64             `json:"fencing_token"`
	ArtifactID      string            `json:"artifact_id"`
	SessionID       string            `json:"session_id,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	Type            string            `json:"type"`
	URI             string            `json:"uri,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	MIMEType        string            `json:"mime_type,omitempty"`
	SizeBytes       int64             `json:"size_bytes"`
	Checksum        string            `json:"checksum,omitempty"`
	ContentHash     string            `json:"content_hash,omitempty"`
	Visibility      string            `json:"visibility,omitempty"`
	RedactionStatus string            `json:"redaction_status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type artifactFinalizeRequest struct {
	CommandID        string            `json:"command_id"`
	TaskRunID        string            `json:"task_run_id"`
	NodeID           string            `json:"node_id"`
	LeaseID          string            `json:"lease_id"`
	FencingToken     int64             `json:"fencing_token"`
	ExpectedRevision uint64            `json:"expected_revision"`
	URI              string            `json:"uri"`
	Summary          string            `json:"summary,omitempty"`
	MIMEType         string            `json:"mime_type,omitempty"`
	SizeBytes        int64             `json:"size_bytes"`
	Checksum         string            `json:"checksum,omitempty"`
	ContentHash      string            `json:"content_hash,omitempty"`
	Visibility       string            `json:"visibility,omitempty"`
	RedactionStatus  string            `json:"redaction_status,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

func (s *artifactCommandStore) Create(ctx context.Context, owner ArtifactOwner, command ArtifactCreateCommand) (*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" || strings.TrimSpace(command.Type) == "" {
		return nil, fmt.Errorf("artifact create identity and type are required: %w", ErrArtifactsInvalid)
	}
	if command.SizeBytes < 0 {
		return nil, fmt.Errorf("artifact create size must be non-negative: %w", ErrArtifactsInvalid)
	}
	request := artifactCreateRequest{
		TaskRunID: owner.TaskRunID, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		FencingToken: owner.FencingToken, ArtifactID: command.ArtifactID, Type: command.Type,
		SessionID: command.SessionID, TaskID: command.TaskID, URI: command.URI,
		Summary: command.Summary, MIMEType: command.MIMEType, SizeBytes: command.SizeBytes,
		Checksum: command.Checksum, ContentHash: command.ContentHash, Visibility: command.Visibility,
		RedactionStatus: command.RedactionStatus, Metadata: cloneArtifactMetadata(command.Metadata),
	}
	request.CommandID = deterministicArtifactCommandID("create", owner, command.ArtifactID, request)
	var result artifactCommandResult
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifact-commands/create"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, request, &result, map[string]string{"X-Lease-Token": owner.LeaseToken}); err != nil {
		return nil, mapArtifactTransportError("create", err)
	}
	artifact, err := validateArtifactCommandResult(owner, command.ArtifactID, request.CommandID, "artifact_create", &result)
	if err != nil {
		return nil, err
	}
	if artifact.SessionID != command.SessionID ||
		(command.TaskID != "" && artifact.TaskID != command.TaskID) ||
		artifact.URI != command.URI || artifact.SizeBytes != command.SizeBytes ||
		artifact.Checksum != command.Checksum || artifact.ContentHash != command.ContentHash {
		return nil, fmt.Errorf("artifact create returned divergent execution metadata: %w", ErrArtifactsUnavailable)
	}
	return artifact, nil
}

func (s *artifactCommandStore) Upload(ctx context.Context, owner ArtifactOwner, command ArtifactUploadCommand) (*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" {
		return nil, fmt.Errorf("artifact upload id is required: %w", ErrArtifactsInvalid)
	}
	digest := artifactContentDigest(command.Content)
	commandID := deterministicArtifactCommandID("upload", owner, command.ArtifactID, struct {
		Digest   string `json:"digest"`
		MIMEType string `json:"mime_type"`
		Size     int    `json:"size"`
	}{Digest: digest, MIMEType: command.MIMEType, Size: len(command.Content)})
	headers := artifactOwnerHeaders(owner)
	headers["X-Command-ID"] = commandID
	headers["X-Task-Run-ID"] = owner.TaskRunID
	// A command-created Artifact starts at revision 1 and upload is its only
	// valid first lifecycle transition. Sending that original CAS value on
	// every retry lets FleetDB consult the immutable receipt before validating a
	// now-expired owner; returning a matching current projection here would
	// incorrectly turn same-content writes with different intent into replay.
	headers["X-Expected-Revision"] = "1"
	var result artifactCommandResult
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(command.ArtifactID) + "/commands/upload"
	contentType := strings.TrimSpace(command.MIMEType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := s.client.doRawWithHeaders(ctx, http.MethodPost, path, bytes.NewReader(command.Content), contentType, &result, headers); err != nil {
		return nil, mapArtifactTransportError("upload", err)
	}
	artifact, err := validateArtifactCommandResult(owner, command.ArtifactID, commandID, "artifact_upload", &result)
	if err != nil {
		return nil, err
	}
	if artifact.DurableStatus != "uploading" || !strings.EqualFold(artifactDigest(artifact), digest) {
		return nil, fmt.Errorf("artifact upload returned divergent content state: %w", ErrArtifactsUnavailable)
	}
	return artifact, nil
}

func (s *artifactCommandStore) Finalize(ctx context.Context, owner ArtifactOwner, command ArtifactFinalizeCommand) (*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" {
		return nil, fmt.Errorf("artifact finalize id is required: %w", ErrArtifactsInvalid)
	}
	// See Upload: replay preparation must not fail on a now-terminated owner
	// before FleetDB gets the opportunity to return its immutable receipt.
	current, err := s.getArtifactForCommand(ctx, owner, command.ArtifactID)
	if err != nil {
		return nil, err
	}
	desired := resolveArtifactFinalize(current, command)
	if desired.SizeBytes < 0 {
		return nil, fmt.Errorf("artifact finalize size must be non-negative: %w", ErrArtifactsInvalid)
	}
	if current.DurableStatus == "finalized" && !artifactMatchesFinalize(current, desired) {
		return nil, fmt.Errorf("artifact %q is finalized with different metadata: %w", command.ArtifactID, ErrArtifactsConflict)
	}
	commandID := deterministicArtifactCommandID("finalize", owner, command.ArtifactID, desired)
	lower := current.Revision
	if current.DurableStatus == "finalized" {
		lower = uint64(1)
		if current.Revision > artifactCommandReplayRevisionWindow {
			lower = current.Revision - artifactCommandReplayRevisionWindow + 1
		}
	}
	var lastConflict error
	for expected := current.Revision; expected >= lower; expected-- {
		artifact, finalizeErr := s.finalizeAtRevision(ctx, owner, command.ArtifactID, commandID, desired, expected)
		if finalizeErr == nil {
			return artifact, nil
		}
		if !errors.Is(finalizeErr, ErrArtifactsConflict) {
			return nil, finalizeErr
		}
		lastConflict = finalizeErr
		if expected == lower {
			break
		}
	}
	return nil, lastConflict
}

const artifactCommandReplayRevisionWindow uint64 = 128

func (s *artifactCommandStore) finalizeAtRevision(ctx context.Context, owner ArtifactOwner, artifactID, commandID string, desired resolvedArtifactFinalize, expectedRevision uint64) (*Artifact, error) {
	request := artifactFinalizeRequest{
		CommandID: commandID, TaskRunID: owner.TaskRunID, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		FencingToken: owner.FencingToken, ExpectedRevision: expectedRevision,
		URI: desired.URI, Summary: desired.Summary, MIMEType: desired.MIMEType,
		SizeBytes: desired.SizeBytes, Checksum: desired.Checksum, ContentHash: desired.ContentHash,
		Visibility: desired.Visibility, RedactionStatus: desired.RedactionStatus,
		Metadata: cloneArtifactMetadata(desired.Metadata),
	}
	var result artifactCommandResult
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(artifactID) + "/commands/finalize"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, request, &result, map[string]string{"X-Lease-Token": owner.LeaseToken}); err != nil {
		return nil, mapArtifactTransportError("finalize", err)
	}
	artifact, err := validateArtifactCommandResult(owner, artifactID, commandID, "artifact_finalize", &result)
	if err != nil {
		return nil, err
	}
	if artifact.DurableStatus != "finalized" || artifact.FinalizedAt == nil || !artifactMatchesFinalize(artifact, desired) {
		return nil, fmt.Errorf("artifact finalize returned divergent state: %w", ErrArtifactsUnavailable)
	}
	return artifact, nil
}

func (s *artifactCommandStore) Reference(ctx context.Context, owner ArtifactOwner, command ArtifactReferenceCommand) (ArtifactReferenceResult, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return ArtifactReferenceResult{}, err
	}
	command.ArtifactID = strings.TrimSpace(command.ArtifactID)
	command.Kind = strings.TrimSpace(command.Kind)
	command.TargetRef = strings.TrimSpace(command.TargetRef)
	if command.ArtifactID == "" || command.Kind == "" || command.TargetRef == "" {
		return ArtifactReferenceResult{}, fmt.Errorf("artifact reference id, kind, and target are required: %w", ErrArtifactsInvalid)
	}
	current, err := s.getArtifactForCommand(ctx, owner, command.ArtifactID)
	if err != nil {
		return ArtifactReferenceResult{}, err
	}
	if current.DurableStatus != "finalized" {
		return ArtifactReferenceResult{}, fmt.Errorf("artifact %q must be finalized before reference: %w", command.ArtifactID, ErrArtifactsInvalidTransition)
	}
	commandID := deterministicArtifactCommandID("reference", owner, command.ArtifactID, struct {
		Kind      string `json:"kind"`
		TargetRef string `json:"target_ref"`
	}{Kind: command.Kind, TargetRef: command.TargetRef})

	// A lost response leaves the immutable reference committed and increments the
	// Artifact revision. Fleet fingerprints the original expected revision, so
	// retry the bounded preceding revision window with the same deterministic
	// command ID until the receipt replays. The current revision is attempted
	// first, preserving normal optimistic concurrency when no receipt exists.
	lower := uint64(1)
	if current.Revision > artifactCommandReplayRevisionWindow {
		lower = current.Revision - artifactCommandReplayRevisionWindow + 1
	}
	var lastConflict error
	for expected := current.Revision; expected >= lower; expected-- {
		result, referenceErr := s.referenceAtRevision(ctx, owner, command, commandID, expected)
		if referenceErr == nil {
			return result, nil
		}
		if !errors.Is(referenceErr, ErrArtifactsConflict) {
			return ArtifactReferenceResult{}, referenceErr
		}
		lastConflict = referenceErr
		if expected == lower {
			break
		}
	}
	return ArtifactReferenceResult{}, lastConflict
}

func (s *artifactCommandStore) referenceAtRevision(
	ctx context.Context,
	owner ArtifactOwner,
	command ArtifactReferenceCommand,
	commandID string,
	expectedRevision uint64,
) (ArtifactReferenceResult, error) {
	request := struct {
		CommandID        string `json:"command_id"`
		TaskRunID        string `json:"task_run_id"`
		NodeID           string `json:"node_id"`
		LeaseID          string `json:"lease_id"`
		FencingToken     int64  `json:"fencing_token"`
		ExpectedRevision uint64 `json:"expected_revision"`
		Kind             string `json:"kind"`
		TargetRef        string `json:"target_ref"`
	}{
		CommandID: commandID, TaskRunID: owner.TaskRunID, NodeID: owner.NodeID,
		LeaseID: owner.LeaseID, FencingToken: owner.FencingToken,
		ExpectedRevision: expectedRevision, Kind: command.Kind, TargetRef: command.TargetRef,
	}
	var result artifactCommandResult
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(command.ArtifactID) + "/commands/reference"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, request, &result, map[string]string{"X-Lease-Token": owner.LeaseToken}); err != nil {
		return ArtifactReferenceResult{}, mapArtifactTransportError("reference", err)
	}
	artifact, err := validateArtifactCommandResult(owner, command.ArtifactID, commandID, "artifact_reference", &result)
	if err != nil {
		return ArtifactReferenceResult{}, err
	}
	if err := validateArtifactReferenceResult(owner, command, commandID, &result); err != nil {
		return ArtifactReferenceResult{}, err
	}
	return ArtifactReferenceResult{Artifact: artifact, Reference: result.Reference, Replayed: result.Replayed}, nil
}

func (s *artifactCommandStore) Get(ctx context.Context, owner ArtifactOwner, artifactID string) (*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(artifactID) == "" {
		return nil, fmt.Errorf("artifact get id is required: %w", ErrArtifactsInvalid)
	}
	var out Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/task-runs/" + pathEscape(owner.TaskRunID) + "/artifacts/" + pathEscape(artifactID)
	if err := s.client.doWithHeaders(ctx, http.MethodGet, path, nil, &out, artifactOwnerHeaders(owner)); err != nil {
		return nil, mapArtifactTransportError("get", err)
	}
	if err := validateArtifactSnapshot(owner, artifactID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactCommandStore) getArtifactForCommand(ctx context.Context, owner ArtifactOwner, artifactID string) (*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(artifactID) == "" {
		return nil, fmt.Errorf("artifact command id is required: %w", ErrArtifactsInvalid)
	}
	var out Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(artifactID)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, mapArtifactTransportError("prepare command", err)
	}
	if err := validateArtifactSnapshot(owner, artifactID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *artifactCommandStore) List(ctx context.Context, owner ArtifactOwner, filter ArtifactFilter) ([]*Artifact, error) {
	if err := validateArtifactOwner(owner); err != nil {
		return nil, err
	}
	if filter.Limit < 0 {
		return nil, fmt.Errorf("artifact list limit must be non-negative: %w", ErrArtifactsInvalid)
	}
	query := url.Values{}
	if filter.Type != "" {
		query.Set("type", filter.Type)
	}
	if filter.DurableStatus != "" {
		query.Set("durable_status", filter.DurableStatus)
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	var result struct {
		Artifacts []*Artifact `json:"artifacts"`
		Count     int         `json:"count"`
	}
	path := withQuery("/api/v1/"+pathEscape(owner.WorkspaceKey)+"/task-runs/"+pathEscape(owner.TaskRunID)+"/artifacts", query)
	if err := s.client.doWithHeaders(ctx, http.MethodGet, path, nil, &result, artifactOwnerHeaders(owner)); err != nil {
		return nil, mapArtifactTransportError("list", err)
	}
	if result.Count != len(result.Artifacts) {
		return nil, fmt.Errorf("artifact list count %d does not match %d rows: %w", result.Count, len(result.Artifacts), ErrArtifactsUnavailable)
	}
	for _, artifact := range result.Artifacts {
		if err := validateArtifactSnapshot(owner, "", artifact); err != nil {
			return nil, err
		}
	}
	if result.Artifacts == nil {
		result.Artifacts = []*Artifact{}
	}
	return result.Artifacts, nil
}

func validateArtifactOwner(owner ArtifactOwner) error {
	if strings.TrimSpace(owner.WorkspaceKey) == "" || strings.TrimSpace(owner.TaskRunID) == "" || strings.TrimSpace(owner.NodeID) == "" || strings.TrimSpace(owner.LeaseID) == "" || strings.TrimSpace(owner.LeaseToken) == "" || owner.FencingToken <= 0 {
		return fmt.Errorf("complete artifact owner fence is required: %w", ErrArtifactsInvalid)
	}
	return nil
}

func artifactOwnerHeaders(owner ArtifactOwner) map[string]string {
	return map[string]string{
		"X-Node-ID":       owner.NodeID,
		"X-Lease-ID":      owner.LeaseID,
		"X-Lease-Token":   owner.LeaseToken,
		"X-Fencing-Token": strconv.FormatInt(owner.FencingToken, 10),
	}
}

func deterministicArtifactCommandID(operation string, owner ArtifactOwner, artifactID string, payload any) string {
	material, _ := json.Marshal(struct {
		Operation    string `json:"operation"`
		WorkspaceKey string `json:"workspace_key"`
		TaskRunID    string `json:"task_run_id"`
		NodeID       string `json:"node_id"`
		LeaseID      string `json:"lease_id"`
		FencingToken int64  `json:"fencing_token"`
		ArtifactID   string `json:"artifact_id"`
		Payload      any    `json:"payload"`
	}{operation, owner.WorkspaceKey, owner.TaskRunID, owner.NodeID, owner.LeaseID, owner.FencingToken, artifactID, payload})
	sum := sha256.Sum256(material)
	return "loom-artifact-" + operation + "-" + hex.EncodeToString(sum[:])
}

func artifactContentDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func artifactDigest(artifact *Artifact) string {
	if artifact == nil {
		return ""
	}
	if value := strings.TrimSpace(artifact.ContentHash); value != "" {
		return value
	}
	return strings.TrimSpace(artifact.Checksum)
}

func validateArtifactCommandResult(owner ArtifactOwner, artifactID, commandID, commandType string, result *artifactCommandResult) (*Artifact, error) {
	if result == nil || result.Receipt == nil || result.Receipt.WorkspaceKey != owner.WorkspaceKey ||
		result.Receipt.CommandID != commandID || result.Receipt.ArtifactID != artifactID ||
		result.Receipt.CommandType != commandType || result.Receipt.ArtifactRevision == 0 ||
		strings.TrimSpace(result.Receipt.RequestFingerprint) == "" || result.Receipt.CommittedAt.IsZero() {
		return nil, fmt.Errorf("artifact command returned invalid receipt: %w", ErrArtifactsUnavailable)
	}
	if err := validateArtifactSnapshot(owner, artifactID, result.Artifact); err != nil {
		return nil, err
	}
	if result.Artifact.Revision != result.Receipt.ArtifactRevision {
		return nil, fmt.Errorf("artifact command receipt revision %d does not match artifact revision %d: %w", result.Receipt.ArtifactRevision, result.Artifact.Revision, ErrArtifactsUnavailable)
	}
	return result.Artifact, nil
}

func validateArtifactReferenceResult(owner ArtifactOwner, command ArtifactReferenceCommand, commandID string, result *artifactCommandResult) error {
	if result == nil || result.Reference == nil || result.Receipt == nil {
		return fmt.Errorf("artifact reference command returned no reference: %w", ErrArtifactsUnavailable)
	}
	reference := result.Reference
	if result.Artifact == nil || result.Artifact.DurableStatus != "finalized" || result.Artifact.FinalizedAt == nil ||
		result.Receipt.ReferenceID != commandID || reference.ReferenceID != commandID ||
		reference.WorkspaceKey != owner.WorkspaceKey || reference.ArtifactID != command.ArtifactID ||
		reference.OwnerType != "task_run" || reference.OwnerID != owner.TaskRunID ||
		reference.Kind != command.Kind || reference.TargetRef != command.TargetRef || reference.CreatedAt.IsZero() {
		return fmt.Errorf("artifact reference command returned divergent reference: %w", ErrArtifactsUnavailable)
	}
	return nil
}

func validateArtifactSnapshot(owner ArtifactOwner, artifactID string, artifact *Artifact) error {
	if artifact == nil || artifact.WorkspaceKey != owner.WorkspaceKey || artifact.OwnerType != "task_run" || artifact.OwnerID != owner.TaskRunID || artifact.Revision == 0 || strings.TrimSpace(artifact.ArtifactID) == "" {
		return fmt.Errorf("artifact response escaped owner scope: %w", ErrArtifactsUnavailable)
	}
	if artifactID != "" && artifact.ArtifactID != artifactID {
		return fmt.Errorf("artifact response id %q does not match %q: %w", artifact.ArtifactID, artifactID, ErrArtifactsUnavailable)
	}
	return nil
}

func mapArtifactTransportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, ErrArtifactsNotFound), errors.Is(err, ErrArtifactsInvalid), errors.Is(err, ErrArtifactsConflict), errors.Is(err, ErrArtifactsNotOwner), errors.Is(err, ErrArtifactsInvalidTransition), errors.Is(err, ErrArtifactsUnavailable):
		return err
	case errors.Is(err, domain.ErrNotFound):
		sentinel = ErrArtifactsNotFound
	case errors.Is(err, domain.ErrNotOwner), errors.Is(err, domain.ErrGone):
		sentinel = ErrArtifactsNotOwner
	case errors.Is(err, domain.ErrInvalidTransition):
		sentinel = ErrArtifactsInvalidTransition
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed), errors.Is(err, domain.ErrConflict), errors.Is(err, ErrWorkflowCatalogRevisionConflict):
		sentinel = ErrArtifactsConflict
	case errors.Is(err, domain.ErrInvalid):
		sentinel = ErrArtifactsInvalid
	default:
		sentinel = ErrArtifactsUnavailable
	}
	return fmt.Errorf("artifact %s: %w", operation, errors.Join(sentinel, err))
}

func cloneArtifactMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

type resolvedArtifactFinalize struct {
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

func resolveArtifactFinalize(current *Artifact, command ArtifactFinalizeCommand) resolvedArtifactFinalize {
	desired := resolvedArtifactFinalize{
		URI: current.URI, Summary: current.Summary, MIMEType: current.MIMEType,
		SizeBytes: current.SizeBytes, Checksum: current.Checksum, ContentHash: current.ContentHash,
		Visibility: current.Visibility, RedactionStatus: current.RedactionStatus,
		Metadata: cloneArtifactMetadata(current.Metadata),
	}
	assignString := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	assignString(&desired.URI, command.URI)
	assignString(&desired.Summary, command.Summary)
	assignString(&desired.MIMEType, command.MIMEType)
	if command.SizeBytes != nil {
		desired.SizeBytes = *command.SizeBytes
	}
	assignString(&desired.Checksum, command.Checksum)
	assignString(&desired.ContentHash, command.ContentHash)
	assignString(&desired.Visibility, command.Visibility)
	assignString(&desired.RedactionStatus, command.RedactionStatus)
	if command.Metadata != nil {
		desired.Metadata = cloneArtifactMetadata(*command.Metadata)
	}
	return desired
}

func artifactMatchesFinalize(artifact *Artifact, desired resolvedArtifactFinalize) bool {
	return artifact != nil && artifact.URI == desired.URI && artifact.Summary == desired.Summary &&
		artifact.MIMEType == desired.MIMEType && artifact.SizeBytes == desired.SizeBytes &&
		strings.EqualFold(artifact.Checksum, desired.Checksum) && strings.EqualFold(artifact.ContentHash, desired.ContentHash) &&
		artifact.Visibility == desired.Visibility && artifact.RedactionStatus == desired.RedactionStatus &&
		reflect.DeepEqual(artifact.Metadata, desired.Metadata)
}
