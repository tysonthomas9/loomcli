package artifacts

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// QueryService owns general Artifact metadata/content reads. It validates
// workspace scope and every returned projection but leaves task/session
// visibility relationships to the consuming capability that owns them.
type QueryService struct {
	store QueryStore
}

var _ QueryAPI = (*QueryService)(nil)

func NewQuery(store QueryStore) (*QueryService, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Artifacts queries: durable read port is required: %w", ErrUnavailable)
	}
	return &QueryService{store: store}, nil
}

func (service *QueryService) GetArtifact(ctx context.Context, query Query) (*Artifact, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	artifact, err := service.store.GetArtifactRecord(ctx, query.WorkspaceKey, query.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("get artifact %q: %w", query.ArtifactID, err)
	}
	if err := validateQueryArtifact(artifact, query.WorkspaceKey, query.ArtifactID); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (service *QueryService) ListArtifacts(ctx context.Context, query SearchQuery) ([]*Artifact, error) {
	query, err := normalizeSearchQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	values, err := service.store.ListArtifactRecords(ctx, query.WorkspaceKey, query.Filter)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	result := make([]*Artifact, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, artifact := range values {
		if err := validateQueryArtifact(artifact, query.WorkspaceKey, ""); err != nil {
			return nil, err
		}
		if !artifactMatchesSearch(artifact, query.Filter) {
			return nil, fmt.Errorf("artifact %q escaped search filter: %w", artifact.ArtifactID, ErrInvalidPersistedState)
		}
		if _, duplicate := seen[artifact.ArtifactID]; duplicate {
			return nil, fmt.Errorf("duplicate artifact %q: %w", artifact.ArtifactID, ErrInvalidPersistedState)
		}
		seen[artifact.ArtifactID] = struct{}{}
		result = append(result, cloneArtifact(artifact))
	}
	return result, nil
}

func (service *QueryService) ReadArtifactContent(ctx context.Context, query Query) ([]byte, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	// Re-resolve and validate metadata immediately before touching managed
	// bytes. A content-capable adapter cannot become an ID-only read bypass for
	// malformed or cross-workspace persisted rows.
	artifact, err := service.GetArtifact(ctx, query)
	if err != nil {
		return nil, err
	}
	if artifact.DurableStatus != StatusFinalized {
		return nil, fmt.Errorf("read artifact %q before finalization: %w", query.ArtifactID, ErrContentUnavailable)
	}
	content, err := service.store.ReadArtifactContent(ctx, query.WorkspaceKey, query.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read artifact %q content: %w", query.ArtifactID, err)
	}
	persistedHash := strings.TrimSpace(artifact.ContentHash)
	if persistedHash == "" {
		persistedHash = strings.TrimSpace(artifact.Checksum)
	}
	if len(content) == 0 || artifact.SizeBytes != int64(len(content)) || persistedHash == "" ||
		!strings.EqualFold(persistedHash, artifactContentHash(content)) {
		return nil, fmt.Errorf("artifact %q content failed durable integrity validation: %w", query.ArtifactID, ErrEvidenceCorrupt)
	}
	return append([]byte(nil), content...), nil
}

func normalizeQuery(query Query) (Query, error) {
	var err error
	query.WorkspaceKey, err = requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return Query{}, err
	}
	query.ArtifactID, err = requireCanonical("artifact id", query.ArtifactID)
	if err != nil {
		return Query{}, err
	}
	return query, nil
}

func normalizeSearchQuery(query SearchQuery) (SearchQuery, error) {
	workspace, err := requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return SearchQuery{}, err
	}
	query.WorkspaceKey = workspace
	filter := &query.Filter
	filter.AgentID = strings.TrimSpace(filter.AgentID)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	filter.TaskID = strings.TrimSpace(filter.TaskID)
	filter.OwnerID = strings.TrimSpace(filter.OwnerID)
	filter.Type = strings.TrimSpace(filter.Type)
	filter.OwnerType = OwnerType(strings.TrimSpace(string(filter.OwnerType)))
	filter.DurableStatus = DurableStatus(strings.TrimSpace(string(filter.DurableStatus)))
	if filter.OwnerType != "" && !validOwnerType(filter.OwnerType) {
		return SearchQuery{}, fmt.Errorf("unsupported owner type %q: %w", filter.OwnerType, ErrInvalid)
	}
	if filter.DurableStatus != "" && !validDurableStatus(filter.DurableStatus) {
		return SearchQuery{}, fmt.Errorf("unsupported durable status %q: %w", filter.DurableStatus, ErrInvalid)
	}
	if filter.Limit < 0 {
		return SearchQuery{}, fmt.Errorf("artifact search limit must not be negative: %w", ErrInvalid)
	}
	return query, nil
}

func validateQueryArtifact(artifact *Artifact, workspace, artifactID string) error {
	if artifact == nil || artifact.WorkspaceKey != workspace ||
		(artifactID != "" && artifact.ArtifactID != artifactID) {
		return ErrInvalidPersistedState
	}
	if _, err := requireCanonical("persisted artifact id", artifact.ArtifactID); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if _, err := requireCanonical("persisted artifact type", artifact.Type); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if _, err := requireCanonical("persisted artifact owner id", artifact.OwnerID); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if !validOwnerType(artifact.OwnerType) || !validDurableStatus(artifact.DurableStatus) || artifact.SizeBytes < 0 {
		return ErrInvalidPersistedState
	}
	return nil
}

func validOwnerType(owner OwnerType) bool {
	return owner == OwnerTaskRun || owner == OwnerSession
}

func artifactMatchesSearch(artifact *Artifact, filter SearchFilter) bool {
	return (filter.AgentID == "" || artifact.AgentID == filter.AgentID) &&
		(filter.SessionID == "" || artifact.SessionID == filter.SessionID) &&
		(filter.TaskID == "" || artifact.TaskID == filter.TaskID) &&
		(filter.OwnerType == "" || artifact.OwnerType == filter.OwnerType) &&
		(filter.OwnerID == "" || artifact.OwnerID == filter.OwnerID) &&
		(filter.Type == "" || artifact.Type == filter.Type) &&
		(filter.DurableStatus == "" || artifact.DurableStatus == filter.DurableStatus)
}
