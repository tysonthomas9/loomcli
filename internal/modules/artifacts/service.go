package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Service implements the Artifacts lifecycle over one owner-scoped durable
// port. It contains no transport, legacy Store, or shared domain dependency.
type Service struct {
	store     Store
	admission *authority.Admission
}

var _ API = (*Service)(nil)

// New constructs Artifacts over its owner-scoped durable port and an
// issuer-bound, default-deny admission registry. Missing dependencies fail
// closed at composition time.
func New(store Store, admission *authority.Admission) (*Service, error) {
	if store == nil || admission == nil {
		return nil, fmt.Errorf("compose Artifacts: durable port and admission are required: %w", ErrUnavailable)
	}
	return &Service{store: store, admission: admission}, nil
}

func (s *Service) Create(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, command CreateCommand) (*Artifact, error) {
	owner, err := s.authorize(ActionDeclare, owner, auth)
	if err != nil {
		return nil, err
	}
	command, err = normalizeCreate(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	// Keep an immutable semantic copy on this side of the durable port. Map
	// fields are reference values, so an adapter must not be able to mutate the
	// expected create envelope and thereby make a divergent response look valid.
	expected := cloneCreateCommand(command)
	artifact, err := s.store.Create(ctx, owner, cloneCreateCommand(command))
	if err != nil {
		return nil, fmt.Errorf("create artifact %q: %w", command.ArtifactID, err)
	}
	if err := validatePersistedArtifact(artifact, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	if err := validateCreatedArtifact(artifact, expected); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (s *Service) Upload(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, command UploadCommand) (*Artifact, error) {
	owner, err := s.authorize(ActionUpload, owner, auth)
	if err != nil {
		return nil, err
	}
	command, err = normalizeUpload(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	expected := cloneUploadCommand(command)
	artifact, err := s.store.Upload(ctx, owner, cloneUploadCommand(command))
	if err != nil {
		return nil, fmt.Errorf("upload artifact %q: %w", command.ArtifactID, err)
	}
	if err := validatePersistedArtifact(artifact, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	if err := validateUploadedArtifact(artifact, expected); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (s *Service) Finalize(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, command FinalizeCommand) (*Artifact, error) {
	owner, err := s.authorize(ActionFinalize, owner, auth)
	if err != nil {
		return nil, err
	}
	command, err = normalizeFinalize(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	expected := cloneFinalizeCommand(command)
	artifact, err := s.store.Finalize(ctx, owner, cloneFinalizeCommand(command))
	if err != nil {
		return nil, fmt.Errorf("finalize artifact %q: %w", command.ArtifactID, err)
	}
	if err := validatePersistedArtifact(artifact, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	if artifact.DurableStatus != StatusFinalized || artifact.FinalizedAt == nil {
		return nil, fmt.Errorf("finalize artifact %q returned status %q: %w", command.ArtifactID, artifact.DurableStatus, ErrInvalidPersistedState)
	}
	if err := validateFinalizedArtifact(artifact, expected); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (s *Service) Reference(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, command ReferenceCommand) (ReferenceResult, error) {
	owner, err := s.authorize(ActionReference, owner, auth)
	if err != nil {
		return ReferenceResult{}, err
	}
	command, err = normalizeReference(command)
	if err != nil {
		return ReferenceResult{}, err
	}
	result, err := s.store.Reference(ctx, owner, command)
	if err != nil {
		return ReferenceResult{}, fmt.Errorf("reference artifact %q: %w", command.ArtifactID, err)
	}
	if err := validateReferenceResult(result, owner, command); err != nil {
		return ReferenceResult{}, err
	}
	return cloneReferenceResult(result), nil
}

func (s *Service) Get(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, query GetQuery) (*Artifact, error) {
	owner, err := s.authorize(ActionGet, owner, auth)
	if err != nil {
		return nil, err
	}
	query.ArtifactID, err = requireCanonical("artifact id", query.ArtifactID)
	if err != nil {
		return nil, err
	}
	artifact, err := s.store.Get(ctx, owner, query)
	if err != nil {
		return nil, fmt.Errorf("get artifact %q: %w", query.ArtifactID, err)
	}
	if err := validatePersistedArtifact(artifact, owner, query.ArtifactID); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (s *Service) List(ctx context.Context, auth authority.ExecutionAuthority, owner ExecutionOwner, filter ListFilter) ([]*Artifact, error) {
	owner, err := s.authorize(ActionList, owner, auth)
	if err != nil {
		return nil, err
	}
	filter.Type = strings.TrimSpace(filter.Type)
	filter.DurableStatus = DurableStatus(strings.TrimSpace(string(filter.DurableStatus)))
	if filter.Limit < 0 {
		return nil, fmt.Errorf("artifact list limit must not be negative: %w", ErrInvalid)
	}
	if filter.DurableStatus != "" && !validDurableStatus(filter.DurableStatus) {
		return nil, fmt.Errorf("unsupported durable status %q: %w", filter.DurableStatus, ErrInvalid)
	}
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	values, err := s.store.List(ctx, owner, filter)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	out := make([]*Artifact, 0, len(values))
	for _, artifact := range values {
		if err := validatePersistedArtifact(artifact, owner, ""); err != nil {
			return nil, err
		}
		if filter.Type != "" && artifact.Type != filter.Type {
			return nil, fmt.Errorf("artifact list returned type %q outside filter %q: %w", artifact.Type, filter.Type, ErrInvalidPersistedState)
		}
		if filter.DurableStatus != "" && artifact.DurableStatus != filter.DurableStatus {
			return nil, fmt.Errorf("artifact list returned status %q outside filter %q: %w", artifact.DurableStatus, filter.DurableStatus, ErrInvalidPersistedState)
		}
		out = append(out, cloneArtifact(artifact))
	}
	return out, nil
}

// CreateContent owns the retry-safe declare, upload, and finalize sequence
// used by execution-produced transcript/log/patch artifacts. A retry may reuse
// only the same task-run-owned logical artifact; a finalized match is returned
// without rewriting content.
func (s *Service) CreateContent(
	ctx context.Context,
	auth ContentAuthorities,
	owner ExecutionOwner,
	command CreateCommand,
	content []byte,
	reference ReferenceCommand,
) (ContentResult, error) {
	authorizedOwner, err := s.authorizeContent(owner, auth)
	if err != nil {
		return ContentResult{}, err
	}
	owner = authorizedOwner
	artifact, complete, result, err := s.prepareContentArtifact(ctx, auth, owner, command, content, reference)
	if err != nil {
		return ContentResult{}, err
	}
	if complete {
		return result, nil
	}
	return s.persistContentArtifact(ctx, auth, owner, artifact, command.MIMEType, content, reference)
}

func (s *Service) prepareContentArtifact(
	ctx context.Context,
	auth ContentAuthorities,
	owner ExecutionOwner,
	command CreateCommand,
	content []byte,
	reference ReferenceCommand,
) (*Artifact, bool, ContentResult, error) {
	artifact, err := s.Create(ctx, auth.Declare, owner, command)
	if err == nil {
		return artifact, false, ContentResult{}, nil
	}
	if !errors.Is(err, ErrAlreadyExists) {
		return nil, false, ContentResult{}, err
	}
	normalized, normalizeErr := normalizeCreate(command)
	if normalizeErr != nil {
		return nil, false, ContentResult{}, normalizeErr
	}
	existing, getErr := s.Get(ctx, auth.Get, owner, GetQuery{ArtifactID: normalized.ArtifactID})
	if getErr != nil {
		return nil, false, ContentResult{}, fmt.Errorf("reuse existing artifact %q: %w", command.ArtifactID, getErr)
	}
	if !reusableContentArtifact(existing, normalized) {
		return nil, false, ContentResult{}, err
	}
	if existing.DurableStatus != StatusFinalized {
		return existing, false, ContentResult{}, nil
	}
	result, referenceErr := s.reuseFinalizedContentArtifact(ctx, auth.Reference, owner, existing, command.ArtifactID, content, reference)
	return existing, true, result, referenceErr
}

func (s *Service) reuseFinalizedContentArtifact(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	owner ExecutionOwner,
	existing *Artifact,
	artifactID string,
	content []byte,
	reference ReferenceCommand,
) (ContentResult, error) {
	persistedHash := strings.TrimSpace(existing.ContentHash)
	if persistedHash == "" {
		persistedHash = strings.TrimSpace(existing.Checksum)
	}
	if !strings.EqualFold(persistedHash, artifactContentHash(content)) {
		return ContentResult{}, fmt.Errorf("reuse finalized artifact %q with different content: %w", artifactID, ErrAlreadyExists)
	}
	referenced, err := s.referenceContentArtifact(ctx, auth, owner, existing, reference)
	if err != nil {
		return ContentResult{}, err
	}
	return ContentResult{Artifact: existing, Reference: referenced.Reference}, nil
}

func (s *Service) persistContentArtifact(
	ctx context.Context,
	auth ContentAuthorities,
	owner ExecutionOwner,
	artifact *Artifact,
	mimeType string,
	content []byte,
	reference ReferenceCommand,
) (ContentResult, error) {
	_, err := s.Upload(ctx, auth.Upload, owner, UploadCommand{
		ArtifactID: artifact.ArtifactID,
		Content:    content,
		MIMEType:   mimeType,
	})
	if err != nil {
		return ContentResult{}, err
	}
	hash := artifactContentHash(content)
	finalized, err := s.Finalize(ctx, auth.Finalize, owner, FinalizeCommand{ArtifactID: artifact.ArtifactID, ContentHash: &hash})
	if err != nil {
		return ContentResult{}, err
	}
	referenced, err := s.referenceContentArtifact(ctx, auth.Reference, owner, finalized, reference)
	if err != nil {
		return ContentResult{}, err
	}
	return ContentResult{Artifact: finalized, Reference: referenced.Reference}, nil
}

func (s *Service) referenceContentArtifact(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	owner ExecutionOwner,
	artifact *Artifact,
	reference ReferenceCommand,
) (ReferenceResult, error) {
	reference.ArtifactID = artifact.ArtifactID
	return s.Reference(ctx, auth, owner, reference)
}

func (s *Service) authorizeContent(owner ExecutionOwner, auth ContentAuthorities) (ExecutionOwner, error) {
	operations := []struct {
		action authority.Action
		auth   authority.ExecutionAuthority
	}{
		{ActionDeclare, auth.Declare},
		{ActionGet, auth.Get},
		{ActionUpload, auth.Upload},
		{ActionFinalize, auth.Finalize},
		{ActionReference, auth.Reference},
	}
	var authorized ExecutionOwner
	for _, operation := range operations {
		value, err := s.authorize(operation.action, owner, operation.auth)
		if err != nil {
			return ExecutionOwner{}, err
		}
		authorized = value
	}
	return authorized, nil
}

func artifactContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *Service) authorize(
	action authority.Action,
	owner ExecutionOwner,
	auth authority.ExecutionAuthority,
) (ExecutionOwner, error) {
	owner, err := normalizeExecutionOwner(owner)
	if err != nil {
		return ExecutionOwner{}, err
	}
	if s == nil || s.store == nil || s.admission == nil {
		return ExecutionOwner{}, ErrUnavailable
	}
	if err := s.admission.RequireExecution(action, owner.WorkspaceKey, auth); err != nil {
		return ExecutionOwner{}, err
	}
	if auth.ResourceKind() != authority.ExecutionResourceTaskRun ||
		auth.ResourceID() != owner.TaskRunID || auth.NodeID() != owner.NodeID ||
		auth.LeaseID() != owner.LeaseID || auth.FencingToken() != owner.FencingToken {
		return ExecutionOwner{}, ErrNotOwner
	}
	return owner, nil
}

func normalizeExecutionOwner(owner ExecutionOwner) (ExecutionOwner, error) {
	var err error
	owner.WorkspaceKey, err = requireCanonical("workspace", owner.WorkspaceKey)
	if err != nil {
		return ExecutionOwner{}, err
	}
	owner.TaskRunID, err = requireCanonical("task run id", owner.TaskRunID)
	if err != nil {
		return ExecutionOwner{}, err
	}
	owner.NodeID, err = requireCanonical("node id", owner.NodeID)
	if err != nil {
		return ExecutionOwner{}, err
	}
	owner.LeaseID, err = requireCanonical("lease id", owner.LeaseID)
	if err != nil {
		return ExecutionOwner{}, err
	}
	owner.LeaseToken = strings.TrimSpace(owner.LeaseToken)
	if owner.LeaseToken == "" {
		return ExecutionOwner{}, fmt.Errorf("lease token is required: %w", ErrInvalid)
	}
	if owner.FencingToken <= 0 {
		return ExecutionOwner{}, fmt.Errorf("positive fencing token is required: %w", ErrInvalid)
	}
	return owner, nil
}

func normalizeCreate(command CreateCommand) (CreateCommand, error) {
	var err error
	command.ArtifactID, err = requireCanonical("artifact id", command.ArtifactID)
	if err != nil {
		return CreateCommand{}, err
	}
	command.Type, err = requireCanonical("artifact type", command.Type)
	if err != nil {
		return CreateCommand{}, err
	}
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.URI = strings.TrimSpace(command.URI)
	command.MIMEType = strings.TrimSpace(command.MIMEType)
	command.Checksum = strings.TrimSpace(command.Checksum)
	command.ContentHash = strings.TrimSpace(command.ContentHash)
	command.Visibility = strings.TrimSpace(command.Visibility)
	command.RedactionStatus = strings.TrimSpace(command.RedactionStatus)
	if command.SizeBytes < 0 {
		return CreateCommand{}, fmt.Errorf("artifact size must not be negative: %w", ErrInvalid)
	}
	command.Metadata = cloneMetadata(command.Metadata)
	return command, nil
}

func cloneCreateCommand(command CreateCommand) CreateCommand {
	command.Metadata = cloneMetadata(command.Metadata)
	return command
}

func normalizeUpload(command UploadCommand) (UploadCommand, error) {
	var err error
	command.ArtifactID, err = requireCanonical("artifact id", command.ArtifactID)
	if err != nil {
		return UploadCommand{}, err
	}
	command.MIMEType = strings.TrimSpace(command.MIMEType)
	command.Content = append([]byte(nil), command.Content...)
	return command, nil
}

func normalizeReference(command ReferenceCommand) (ReferenceCommand, error) {
	var err error
	command.ArtifactID, err = requireCanonical("artifact id", command.ArtifactID)
	if err != nil {
		return ReferenceCommand{}, err
	}
	command.Kind, err = requireCanonical("reference kind", command.Kind)
	if err != nil {
		return ReferenceCommand{}, err
	}
	command.TargetRef, err = requireCanonical("reference target", command.TargetRef)
	if err != nil {
		return ReferenceCommand{}, err
	}
	return command, nil
}

func cloneUploadCommand(command UploadCommand) UploadCommand {
	command.Content = append([]byte(nil), command.Content...)
	return command
}

func normalizeFinalize(command FinalizeCommand) (FinalizeCommand, error) {
	var err error
	command.ArtifactID, err = requireCanonical("artifact id", command.ArtifactID)
	if err != nil {
		return FinalizeCommand{}, err
	}
	if command.SizeBytes != nil && *command.SizeBytes < 0 {
		return FinalizeCommand{}, fmt.Errorf("artifact size must not be negative: %w", ErrInvalid)
	}
	command.URI = cloneStringPointer(command.URI)
	command.Summary = cloneStringPointer(command.Summary)
	command.MIMEType = cloneStringPointer(command.MIMEType)
	command.SizeBytes = cloneInt64Pointer(command.SizeBytes)
	command.Checksum = cloneStringPointer(command.Checksum)
	command.ContentHash = cloneStringPointer(command.ContentHash)
	command.Visibility = cloneStringPointer(command.Visibility)
	command.RedactionStatus = cloneStringPointer(command.RedactionStatus)
	if command.Metadata != nil {
		metadata := cloneMetadata(*command.Metadata)
		command.Metadata = &metadata
	}
	return command, nil
}

func cloneFinalizeCommand(command FinalizeCommand) FinalizeCommand {
	command.URI = cloneStringPointer(command.URI)
	command.Summary = cloneStringPointer(command.Summary)
	command.MIMEType = cloneStringPointer(command.MIMEType)
	command.SizeBytes = cloneInt64Pointer(command.SizeBytes)
	command.Checksum = cloneStringPointer(command.Checksum)
	command.ContentHash = cloneStringPointer(command.ContentHash)
	command.Visibility = cloneStringPointer(command.Visibility)
	command.RedactionStatus = cloneStringPointer(command.RedactionStatus)
	if command.Metadata != nil {
		metadata := cloneMetadata(*command.Metadata)
		command.Metadata = &metadata
	}
	return command
}

func cloneStringPointer(in *string) *string {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

func cloneInt64Pointer(in *int64) *int64 {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

func validatePersistedArtifact(artifact *Artifact, owner ExecutionOwner, artifactID string) error {
	if artifact == nil {
		return fmt.Errorf("empty artifact result: %w", ErrInvalidPersistedState)
	}
	if artifact.WorkspaceKey != owner.WorkspaceKey || artifact.OwnerType != OwnerTaskRun || artifact.OwnerID != owner.TaskRunID {
		return fmt.Errorf("artifact %q escaped execution owner scope: %w", artifact.ArtifactID, ErrInvalidPersistedState)
	}
	if artifactID != "" && artifact.ArtifactID != artifactID {
		return fmt.Errorf("artifact result id %q does not match %q: %w", artifact.ArtifactID, artifactID, ErrInvalidPersistedState)
	}
	if _, err := requireCanonical("persisted artifact id", artifact.ArtifactID); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if _, err := requireCanonical("persisted artifact type", artifact.Type); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if artifact.SizeBytes < 0 {
		return fmt.Errorf("artifact %q has negative size: %w", artifact.ArtifactID, ErrInvalidPersistedState)
	}
	if !validDurableStatus(artifact.DurableStatus) {
		return fmt.Errorf("artifact %q has unsupported status %q: %w", artifact.ArtifactID, artifact.DurableStatus, ErrInvalidPersistedState)
	}
	return nil
}

func validateReferenceResult(result ReferenceResult, owner ExecutionOwner, command ReferenceCommand) error {
	if err := validatePersistedArtifact(result.Artifact, owner, command.ArtifactID); err != nil {
		return err
	}
	if result.Artifact.DurableStatus != StatusFinalized || result.Artifact.FinalizedAt == nil {
		return fmt.Errorf("artifact %q reference returned non-finalized state: %w", command.ArtifactID, ErrInvalidPersistedState)
	}
	reference := result.Reference
	if reference == nil || strings.TrimSpace(reference.ReferenceID) == "" || reference.CreatedAt.IsZero() ||
		reference.WorkspaceKey != owner.WorkspaceKey || reference.ArtifactID != command.ArtifactID ||
		reference.OwnerType != OwnerTaskRun || reference.OwnerID != owner.TaskRunID ||
		reference.Kind != command.Kind || reference.TargetRef != command.TargetRef {
		return fmt.Errorf("artifact %q reference result escaped immutable command envelope: %w", command.ArtifactID, ErrInvalidPersistedState)
	}
	return nil
}

// validateCreatedArtifact proves that the durable create command did not
// silently discard caller-supplied ownership and content-integrity metadata.
// Empty optional strings may be filled by backend policy, but every supplied
// value and the scalar size must survive the command exactly. Hash comparison
// is case-insensitive because SHA algorithm/digest casing is not semantic.
func validateCreatedArtifact(artifact *Artifact, command CreateCommand) error {
	if artifact == nil {
		return fmt.Errorf("empty artifact create result: %w", ErrInvalidPersistedState)
	}
	if artifact.Type != command.Type || artifact.SizeBytes != command.SizeBytes ||
		!matchesSuppliedArtifactField(command.AgentID, artifact.AgentID) ||
		!matchesSuppliedArtifactField(command.SessionID, artifact.SessionID) ||
		!matchesSuppliedArtifactField(command.TaskID, artifact.TaskID) ||
		!matchesSuppliedArtifactField(command.URI, artifact.URI) ||
		!matchesSuppliedArtifactField(command.Summary, artifact.Summary) ||
		!matchesSuppliedArtifactField(command.MIMEType, artifact.MIMEType) ||
		!matchesSuppliedArtifactField(command.Visibility, artifact.Visibility) ||
		!matchesSuppliedArtifactField(command.RedactionStatus, artifact.RedactionStatus) ||
		(command.Checksum != "" && !strings.EqualFold(command.Checksum, artifact.Checksum)) ||
		(command.ContentHash != "" && !strings.EqualFold(command.ContentHash, artifact.ContentHash)) ||
		(command.Metadata != nil && !maps.Equal(command.Metadata, artifact.Metadata)) {
		return fmt.Errorf("artifact %q create result lost semantic fields: %w", command.ArtifactID, ErrInvalidPersistedState)
	}
	return nil
}

func validateUploadedArtifact(artifact *Artifact, command UploadCommand) error {
	if artifact == nil {
		return fmt.Errorf("empty artifact upload result: %w", ErrInvalidPersistedState)
	}
	expectedHash := artifactContentHash(command.Content)
	persistedHash := strings.TrimSpace(artifact.ContentHash)
	if persistedHash == "" {
		persistedHash = strings.TrimSpace(artifact.Checksum)
	}
	if (artifact.DurableStatus != StatusUploading && artifact.DurableStatus != StatusFinalized) ||
		artifact.SizeBytes != int64(len(command.Content)) || !strings.EqualFold(persistedHash, expectedHash) ||
		(command.MIMEType != "" && artifact.MIMEType != command.MIMEType) {
		return fmt.Errorf("artifact %q upload result lost content state: %w", command.ArtifactID, ErrInvalidPersistedState)
	}
	return nil
}

func validateFinalizedArtifact(artifact *Artifact, command FinalizeCommand) error {
	if artifact == nil {
		return fmt.Errorf("empty artifact finalize result: %w", ErrInvalidPersistedState)
	}
	if !matchesOptionalArtifactField(command.URI, artifact.URI) ||
		!matchesOptionalArtifactField(command.Summary, artifact.Summary) ||
		!matchesOptionalArtifactField(command.MIMEType, artifact.MIMEType) ||
		(command.SizeBytes != nil && artifact.SizeBytes != *command.SizeBytes) ||
		(command.Checksum != nil && !strings.EqualFold(*command.Checksum, artifact.Checksum)) ||
		(command.ContentHash != nil && !strings.EqualFold(*command.ContentHash, artifact.ContentHash)) ||
		!matchesOptionalArtifactField(command.Visibility, artifact.Visibility) ||
		!matchesOptionalArtifactField(command.RedactionStatus, artifact.RedactionStatus) ||
		(command.Metadata != nil && !maps.Equal(*command.Metadata, artifact.Metadata)) {
		return fmt.Errorf("artifact %q finalize result lost semantic fields: %w", command.ArtifactID, ErrInvalidPersistedState)
	}
	return nil
}

func matchesOptionalArtifactField(expected *string, actual string) bool {
	return expected == nil || actual == *expected
}

func matchesSuppliedArtifactField(expected, actual string) bool {
	return expected == "" || actual == expected
}

func validDurableStatus(status DurableStatus) bool {
	switch status {
	case StatusDeclared, StatusUploading, StatusFinalized, StatusFailed:
		return true
	default:
		return false
	}
}

func reusableContentArtifact(existing *Artifact, command CreateCommand) bool {
	if existing == nil || existing.Type != strings.TrimSpace(command.Type) {
		return false
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if sessionID != "" && existing.SessionID != sessionID {
		return false
	}
	taskID := strings.TrimSpace(command.TaskID)
	if taskID != "" && existing.TaskID != taskID {
		return false
	}
	if !matchesSuppliedArtifactField(command.Summary, existing.Summary) ||
		!matchesSuppliedArtifactField(command.URI, existing.URI) ||
		!matchesSuppliedArtifactField(command.MIMEType, existing.MIMEType) ||
		!matchesSuppliedArtifactField(command.Visibility, existing.Visibility) ||
		!matchesSuppliedArtifactField(command.RedactionStatus, existing.RedactionStatus) ||
		(command.Metadata != nil && !maps.Equal(command.Metadata, existing.Metadata)) {
		return false
	}
	if existing.DurableStatus == StatusFailed {
		return false
	}
	if command.SizeBytes > 0 && existing.SizeBytes != command.SizeBytes {
		return false
	}
	if command.Checksum != "" && !strings.EqualFold(command.Checksum, existing.Checksum) {
		return false
	}
	return command.ContentHash == "" || strings.EqualFold(command.ContentHash, existing.ContentHash)
}

func requireCanonical(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required: %w", label, ErrInvalid)
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must be canonical: %w", label, ErrInvalid)
	}
	return value, nil
}
