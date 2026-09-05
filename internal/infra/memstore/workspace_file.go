package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const defaultWorkspaceFileActor = "memstore"

type workspaceFileStore struct {
	mu    sync.RWMutex
	trees map[string]map[string]*domain.WorkspaceFileTree
	bytes map[string]map[string][]byte
	actor string
	clock func() time.Time
}

var _ store.WorkspaceFileStore = (*workspaceFileStore)(nil)

func newWorkspaceFileStore() *workspaceFileStore {
	return &workspaceFileStore{
		trees: make(map[string]map[string]*domain.WorkspaceFileTree),
		bytes: make(map[string]map[string][]byte),
		actor: defaultWorkspaceFileActor,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

func (s *workspaceFileStore) setActor(actor string) {
	s.mu.Lock()
	s.actor = actor
	s.mu.Unlock()
}

func (s *workspaceFileStore) Publish(_ context.Context, workspaceKey string, inputs []domain.WorkspaceFileInput) (*domain.WorkspaceFileTreePublishResult, error) {
	if workspaceKey == "" {
		return nil, fmt.Errorf("workspace key is required: %w", domain.ErrInvalid)
	}
	files := make([]domain.WorkspaceFile, len(inputs))
	bodies := make(map[string][]byte, len(inputs))
	for i, input := range inputs {
		body := append([]byte(nil), input.Bytes...)
		digest := sha256.Sum256(body)
		contentHash := fmt.Sprintf("sha256:%x", digest)
		blobRef := workspaceFileBlobRef(workspaceKey, fmt.Sprintf("%x", digest), int64(len(body)))
		files[i] = domain.WorkspaceFile{
			Path: input.Path, BlobRef: blobRef, ContentHash: contentHash,
			SizeBytes: int64(len(body)), MediaType: input.MediaType, Executable: input.Executable,
		}
		bodies[blobRef] = body
	}
	canonical, err := domain.CanonicalWorkspaceFileManifest(files)
	if err != nil {
		return nil, err
	}
	revision := domain.WorkspaceFileTreeRevision(canonical)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.trees[workspaceKey][revision]; existing != nil {
		return &domain.WorkspaceFileTreePublishResult{Tree: existing.Clone(), Status: domain.WorkspaceFileTreeExisting}, nil
	}
	tree := &domain.WorkspaceFileTree{
		WorkspaceKey: workspaceKey,
		Revision:     revision,
		Files:        canonical,
		CreatedBy:    s.actor,
		CreatedAt:    s.clock(),
	}
	if s.trees[workspaceKey] == nil {
		s.trees[workspaceKey] = make(map[string]*domain.WorkspaceFileTree)
	}
	if s.bytes[workspaceKey] == nil {
		s.bytes[workspaceKey] = make(map[string][]byte)
	}
	for ref, body := range bodies {
		s.bytes[workspaceKey][ref] = append([]byte(nil), body...)
	}
	s.trees[workspaceKey][revision] = tree.Clone()
	return &domain.WorkspaceFileTreePublishResult{Tree: tree.Clone(), Status: domain.WorkspaceFileTreePublished}, nil
}

func (s *workspaceFileStore) GetTree(_ context.Context, workspaceKey, revision string) (*domain.WorkspaceFileTree, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tree := s.trees[workspaceKey][revision]
	if tree == nil {
		return nil, fmt.Errorf("workspace file tree %q: %w", revision, domain.ErrNotFound)
	}
	return tree.Clone(), nil
}

func (s *workspaceFileStore) Stat(_ context.Context, workspaceKey, revision, filePath string) (*domain.WorkspaceFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tree := s.trees[workspaceKey][revision]
	if tree == nil {
		return nil, fmt.Errorf("workspace file tree %q: %w", revision, domain.ErrNotFound)
	}
	for _, file := range tree.Files {
		if file.Path == filePath {
			out := file
			return &out, nil
		}
	}
	return nil, fmt.Errorf("workspace file %q: %w", filePath, domain.ErrNotFound)
}

func (s *workspaceFileStore) Download(_ context.Context, workspaceKey, revision, filePath string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tree := s.trees[workspaceKey][revision]
	if tree == nil {
		return nil, fmt.Errorf("workspace file tree %q: %w", revision, domain.ErrNotFound)
	}
	for _, file := range tree.Files {
		if file.Path != filePath {
			continue
		}
		body, ok := s.bytes[workspaceKey][file.BlobRef]
		if !ok {
			return nil, fmt.Errorf("workspace file %q bytes missing: %w", filePath, domain.ErrIntegrity)
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != file.SizeBytes || fmt.Sprintf("sha256:%x", digest) != file.ContentHash {
			return nil, fmt.Errorf("workspace file %q bytes do not match immutable metadata: %w", filePath, domain.ErrIntegrity)
		}
		return append([]byte(nil), body...), nil
	}
	return nil, fmt.Errorf("workspace file %q: %w", filePath, domain.ErrNotFound)
}

func (s *workspaceFileStore) corrupt(workspaceKey, revision, filePath string, replacement []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tree := s.trees[workspaceKey][revision]
	if tree == nil {
		return domain.ErrNotFound
	}
	for _, file := range tree.Files {
		if file.Path == filePath {
			s.bytes[workspaceKey][file.BlobRef] = append([]byte(nil), replacement...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func workspaceFileBlobRef(workspaceKey, digest string, size int64) string {
	binding := sha256.Sum256([]byte("fleetdb-blob-ref-v1\x00" + workspaceKey + "\x00" + digest + "\x00" + strconv.FormatInt(size, 10)))
	return "blob_" + base64.RawURLEncoding.EncodeToString(binding[:])
}
