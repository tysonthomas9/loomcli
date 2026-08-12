package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

var _ artifacts.QueryStore = (*artifactStore)(nil)

// SeedArtifact inserts owner-typed artifact state into this test-only store.
// It is fixture setup, not a runtime repository or fallback mutation surface.
func (s *Store) SeedArtifact(ctx context.Context, value artifacts.Artifact, content []byte) (*artifacts.Artifact, error) {
	if s == nil || s.artifacts == nil {
		return nil, artifacts.ErrUnavailable
	}
	return s.artifacts.seed(ctx, value, content)
}

func (s *artifactStore) seed(ctx context.Context, value artifacts.Artifact, content []byte) (*artifacts.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if value.WorkspaceKey == "" || value.ArtifactID == "" || value.Type == "" {
		return nil, fmt.Errorf("workspace, artifact id, and type are required: %w", artifacts.ErrInvalid)
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	}
	if value.DurableStatus == "" {
		value.DurableStatus = artifacts.StatusDeclared
	}
	value.Metadata = cloneMap(value.Metadata)
	if len(content) > 0 {
		hash := sha256.Sum256(content)
		digest := "sha256:" + hex.EncodeToString(hash[:])
		value.SizeBytes = int64(len(content))
		value.Checksum = digest
		value.ContentHash = digest
		if value.URI == "" {
			value.URI = fmt.Sprintf("mem://artifacts/%s/%s/%s", value.WorkspaceKey, value.ArtifactID, digest)
		}
	}
	if value.DurableStatus == artifacts.StatusFinalized && value.FinalizedAt == nil {
		value.FinalizedAt = &now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[value.WorkspaceKey] == nil {
		s.items[value.WorkspaceKey] = make(map[string]*artifacts.Artifact)
	}
	if s.content[value.WorkspaceKey] == nil {
		s.content[value.WorkspaceKey] = make(map[string][]byte)
	}
	s.items[value.WorkspaceKey][value.ArtifactID] = cloneArtifact(&value)
	if content != nil {
		s.content[value.WorkspaceKey][value.ArtifactID] = append([]byte(nil), content...)
	}
	return cloneArtifact(&value), nil
}

func (s *artifactStore) GetArtifactRecord(ctx context.Context, workspace, artifactID string) (*artifacts.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.items[workspace][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, workspace, artifacts.ErrNotFound)
	}
	return cloneArtifact(value), nil
}

func (s *artifactStore) ListArtifactRecords(ctx context.Context, workspace string, filter artifacts.SearchFilter) ([]*artifacts.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*artifacts.Artifact, 0, len(s.items[workspace]))
	for _, value := range s.items[workspace] {
		if artifactMatchesOwnerQuery(value, filter) {
			result = append(result, cloneArtifact(value))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *artifactStore) ReadArtifactContent(ctx context.Context, workspace, artifactID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.items[workspace][artifactID]; !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, workspace, artifacts.ErrNotFound)
	}
	content, ok := s.content[workspace][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q content in workspace %q: %w", artifactID, workspace, artifacts.ErrNotFound)
	}
	return append([]byte(nil), content...), nil
}

func artifactMatchesOwnerQuery(value *artifacts.Artifact, filter artifacts.SearchFilter) bool {
	return (filter.AgentID == "" || value.AgentID == filter.AgentID) &&
		(filter.SessionID == "" || value.SessionID == filter.SessionID) &&
		(filter.TaskID == "" || value.TaskID == filter.TaskID) &&
		(filter.OwnerType == "" || value.OwnerType == filter.OwnerType) &&
		(filter.OwnerID == "" || value.OwnerID == filter.OwnerID) &&
		(filter.Type == "" || value.Type == filter.Type) &&
		(filter.DurableStatus == "" || value.DurableStatus == filter.DurableStatus)
}
