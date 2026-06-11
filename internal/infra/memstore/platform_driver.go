package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Driver
}

func newDriverStore() *driverStore {
	return &driverStore{items: make(map[string]map[string]*domain.Driver)}
}

var _ store.DriverStore = (*driverStore)(nil)

func (s *driverStore) Create(_ context.Context, in store.DriverCreate) (*domain.Driver, error) {
	if in.WorkspaceKey == "" || in.DriverID == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + driver_id + name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Driver)
	}
	if _, ok := s.items[in.WorkspaceKey][in.DriverID]; ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	ownerType := in.OwnerType
	if ownerType == "" {
		ownerType = domain.DriverOwnerUser
	}
	status := in.Status
	if status == "" {
		status = domain.DriverStatusDraft
	}
	driver := &domain.Driver{
		WorkspaceKey:    in.WorkspaceKey,
		DriverID:        in.DriverID,
		Name:            in.Name,
		OwnerType:       ownerType,
		OwnerRef:        in.OwnerRef,
		Description:     in.Description,
		ActiveVersionID: in.ActiveVersionID,
		Status:          status,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.items[in.WorkspaceKey][in.DriverID] = driver
	return cloneDriver(driver), nil
}

func (s *driverStore) Get(_ context.Context, ws, driverID string) (*domain.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	return cloneDriver(driver), nil
}

func (s *driverStore) List(_ context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Driver, 0, len(s.items[ws]))
	for _, driver := range s.items[ws] {
		if driverMatchesMem(driver, filter) {
			out = append(out, cloneDriver(driver))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverStore) Update(_ context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	if patch.Name != nil {
		driver.Name = *patch.Name
	}
	if patch.OwnerType != nil {
		driver.OwnerType = *patch.OwnerType
	}
	if patch.OwnerRef != nil {
		driver.OwnerRef = *patch.OwnerRef
	}
	if patch.Description != nil {
		driver.Description = *patch.Description
	}
	if patch.ActiveVersionID != nil {
		driver.ActiveVersionID = *patch.ActiveVersionID
	}
	if patch.Status != nil {
		driver.Status = *patch.Status
	}
	if patch.Metadata != nil {
		driver.Metadata = cloneMap(*patch.Metadata)
	}
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

func (s *driverStore) exists(ws, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][driverID]
	return ok
}

type driverVersionStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]*domain.DriverVersion
	drivers *driverStore
}

func newDriverVersionStore(drivers *driverStore) *driverVersionStore {
	return &driverVersionStore{items: make(map[string]map[string]*domain.DriverVersion), drivers: drivers}
}

var _ store.DriverVersionStore = (*driverVersionStore)(nil)

func (s *driverVersionStore) Create(_ context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	if in.WorkspaceKey == "" || in.VersionID == "" || in.DriverID == "" || in.Version <= 0 || in.SourceDigest == "" || in.BundleDigest == "" {
		return nil, fmt.Errorf("workspace_key + version_id + driver_id + version + source_digest + bundle_digest required: %w", domain.ErrInvalid)
	}
	if s.drivers != nil && !s.drivers.exists(in.WorkspaceKey, in.DriverID) {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.DriverVersion)
	}
	if _, ok := s.items[in.WorkspaceKey][in.VersionID]; ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", in.VersionID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	status := in.ValidationStatus
	if status == "" {
		status = domain.DriverVersionValidationPending
	}
	version := &domain.DriverVersion{
		WorkspaceKey:     in.WorkspaceKey,
		VersionID:        in.VersionID,
		DriverID:         in.DriverID,
		Version:          in.Version,
		SourceRef:        in.SourceRef,
		SourceDigest:     in.SourceDigest,
		BundleRef:        in.BundleRef,
		BundleDigest:     in.BundleDigest,
		Runtime:          in.Runtime,
		Manifest:         cloneMap(in.Manifest),
		BuildDiagnostics: in.BuildDiagnostics,
		ValidationStatus: status,
		CreatedBy:        in.CreatedBy,
		CreatedAt:        time.Now().UTC(),
	}
	s.items[in.WorkspaceKey][in.VersionID] = version
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) Get(_ context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	if !ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", versionID, ws, domain.ErrNotFound)
	}
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) List(_ context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.DriverVersion, 0, len(s.items[ws]))
	for _, version := range s.items[ws] {
		if driverVersionMatchesMem(version, filter) {
			out = append(out, cloneDriverVersion(version))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverVersionStore) belongsToDriver(ws, versionID, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	return ok && version.DriverID == driverID
}

func cloneDriver(d *domain.Driver) *domain.Driver {
	out := *d
	out.Metadata = cloneMap(d.Metadata)
	return &out
}

func cloneDriverVersion(v *domain.DriverVersion) *domain.DriverVersion {
	out := *v
	out.Manifest = cloneMap(v.Manifest)
	return &out
}

func driverMatchesMem(d *domain.Driver, f store.DriverFilter) bool {
	return (f.Name == "" || d.Name == f.Name) && (f.Status == "" || d.Status == f.Status)
}

func driverVersionMatchesMem(v *domain.DriverVersion, f store.DriverVersionFilter) bool {
	return (f.DriverID == "" || v.DriverID == f.DriverID) && (f.ValidationStatus == "" || v.ValidationStatus == f.ValidationStatus)
}
