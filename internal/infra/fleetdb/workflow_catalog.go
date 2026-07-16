package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	// ErrWorkflowCatalogNotFound is the transport-level not-found sentinel.
	ErrWorkflowCatalogNotFound = domain.ErrNotFound
	// ErrWorkflowCatalogInvalid keeps generic FleetDB validation failures out
	// of the capability adapter's legacy-domain dependency surface.
	ErrWorkflowCatalogInvalid = domain.ErrInvalid
	// These transport sentinels preserve FleetDB's machine-readable lifecycle
	// failures until the capability adapter maps them to catalog-owned errors.
	ErrWorkflowCatalogRevisionConflict    = errors.New("fleetdb: workflow catalog revision conflict")
	ErrWorkflowCatalogVersionOwnership    = errors.New("fleetdb: workflow catalog version ownership mismatch")
	ErrWorkflowCatalogVersionNotValidated = errors.New("fleetdb: workflow catalog version not validated")
	ErrWorkflowCatalogVersionNotApproved  = errors.New("fleetdb: workflow catalog version not approved")
)

// WorkflowCatalogLifecycleResult is FleetDB's authoritative response to one
// atomic version lifecycle command.
type WorkflowCatalogLifecycleResult struct {
	CommittedRevision uint64                `json:"committed_revision"`
	SemanticImpact    string                `json:"semantic_impact"`
	Replayed          bool                  `json:"replayed,omitempty"`
	Driver            *domain.Driver        `json:"driver"`
	Version           *domain.DriverVersion `json:"version"`
}

// WorkflowCatalogTransport is the narrow low-level surface exposed to the
// composition root. A composition bridge translates its infrastructure DTOs
// and sentinels into the capability adapter's owned transport vocabulary. The
// implementation shares its parent Client's transport and credentials.
type WorkflowCatalogTransport interface {
	GetDriver(ctx context.Context, workspace, driverID string) (*domain.Driver, error)
	FindDriverByName(ctx context.Context, workspace, name string) (*domain.Driver, error)
	ListDrivers(ctx context.Context, workspace string) ([]*domain.Driver, error)
	GetVersion(ctx context.Context, workspace, versionID string) (*domain.DriverVersion, error)
	ListVersions(ctx context.Context, workspace, driverID string) ([]*domain.DriverVersion, error)
	ApproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
	UnapproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
	ActivateVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
}

type workflowCatalogStore struct{ client *Client }

var _ WorkflowCatalogTransport = (*workflowCatalogStore)(nil)

func (s *workflowCatalogStore) GetDriver(ctx context.Context, workspace, driverID string) (*domain.Driver, error) {
	return s.client.drivers.Get(ctx, workspace, driverID)
}

func (s *workflowCatalogStore) FindDriverByName(ctx context.Context, workspace, name string) (*domain.Driver, error) {
	drivers, err := s.client.drivers.List(ctx, workspace, store.DriverFilter{Name: name, Limit: 2})
	if err != nil {
		return nil, err
	}
	for _, driver := range drivers {
		if driver != nil && driver.Name == name {
			return driver, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *workflowCatalogStore) ListDrivers(ctx context.Context, workspace string) ([]*domain.Driver, error) {
	return s.client.drivers.List(ctx, workspace, store.DriverFilter{})
}

func (s *workflowCatalogStore) GetVersion(ctx context.Context, workspace, versionID string) (*domain.DriverVersion, error) {
	return s.client.versions.Get(ctx, workspace, versionID)
}

func (s *workflowCatalogStore) ListVersions(ctx context.Context, workspace, driverID string) ([]*domain.DriverVersion, error) {
	return s.client.versions.List(ctx, workspace, store.DriverVersionFilter{DriverID: driverID})
}

func (s *workflowCatalogStore) ApproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error) {
	return s.apply(ctx, workspace, driverID, versionID, "approve", expectedRevision)
}

func (s *workflowCatalogStore) UnapproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error) {
	return s.apply(ctx, workspace, driverID, versionID, "unapprove", expectedRevision)
}

func (s *workflowCatalogStore) ActivateVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error) {
	return s.apply(ctx, workspace, driverID, versionID, "activate", expectedRevision)
}

func (s *workflowCatalogStore) apply(ctx context.Context, workspace, driverID, versionID, action string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error) {
	if expectedRevision == 0 {
		return nil, fmt.Errorf("workflow catalog expected revision must be at least 1: %w", ErrWorkflowCatalogInvalid)
	}
	if expectedRevision >= uint64(math.MaxInt64) {
		return nil, fmt.Errorf("workflow catalog expected revision cannot advance within FleetDB's signed persistence range: %w", ErrWorkflowCatalogInvalid)
	}
	path := "/api/v1/" + pathEscape(workspace) + "/drivers/" + pathEscape(driverID) + "/versions/" + pathEscape(versionID) + "/" + action
	body := map[string]uint64{"expected_revision": expectedRevision}
	var out WorkflowCatalogLifecycleResult
	if err := s.client.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
