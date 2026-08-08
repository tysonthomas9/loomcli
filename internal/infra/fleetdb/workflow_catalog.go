package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

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
	ErrWorkflowCatalogRevisionConflict    = errFleetRevisionConflict
	ErrWorkflowCatalogVersionOwnership    = errors.New("fleetdb: workflow catalog version ownership mismatch")
	ErrWorkflowCatalogVersionNotValidated = errors.New("fleetdb: workflow catalog version not validated")
	ErrWorkflowCatalogVersionNotApproved  = errors.New("fleetdb: workflow catalog version not approved")
	ErrWorkflowCatalogAuthoringConflict   = errors.New("fleetdb: workflow catalog authoring conflict")
)

// WorkflowCatalogLifecycleResult is FleetDB's authoritative response to one
// atomic version lifecycle command.
type WorkflowCatalogLifecycleResult struct {
	CommittedRevision uint64                         `json:"committed_revision"`
	SemanticImpact    string                         `json:"semantic_impact"`
	Replayed          bool                           `json:"replayed,omitempty"`
	Driver            *workflowcatalog.Driver        `json:"driver"`
	Version           *workflowcatalog.DriverVersion `json:"version"`
}

// WorkflowCatalogAuthorVersionInput is the server-derived metadata for one
// immutable operator-authored version. WorkspaceKey and DriverID are path
// coordinates; DelegatedActor is sent only in X-Fleet-Delegated-Actor. Trust,
// activation, and the audit actor are deliberately absent from the JSON body.
type WorkflowCatalogAuthorVersionInput struct {
	WorkspaceKey     string
	DriverID         string
	DelegatedActor   string
	RequestID        string
	ExpectedRevision uint64
	DriverName       string
	VersionID        string
	SourceRef        string
	SourceDigest     string
	BundleRef        string
	BundleDigest     string
	Runtime          string
	Manifest         map[string]string
	BuildDiagnostics string
}

// WorkflowCatalogAuthorManagedVersionInput adds only the server-selected
// activation intent admitted by Workflow Catalog's SystemAuthority lane.
type WorkflowCatalogAuthorManagedVersionInput struct {
	WorkflowCatalogAuthorVersionInput
	Activate bool
}

// WorkflowCatalogAuthorVersionResult is FleetDB's authoritative response from
// either atomic authoring route.
type WorkflowCatalogAuthorVersionResult struct {
	Driver            *workflowcatalog.Driver        `json:"driver"`
	Version           *workflowcatalog.DriverVersion `json:"version"`
	CreatedDriver     bool                           `json:"created_driver"`
	CreatedVersion    bool                           `json:"created_version"`
	ReusedVersion     bool                           `json:"reused_version"`
	Activated         bool                           `json:"activated"`
	Replayed          bool                           `json:"replayed"`
	CommittedRevision uint64                         `json:"committed_revision"`
	SemanticImpact    string                         `json:"semantic_impact"`
}

// WorkflowCatalogTransport is the narrow low-level surface exposed to the
// composition root. A composition bridge translates its infrastructure DTOs
// and sentinels into the capability adapter's owned transport vocabulary. The
// implementation shares its parent Client's transport and credentials.
type WorkflowCatalogTransport interface {
	GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error)
	FindDriverByName(ctx context.Context, workspace, name string) (*workflowcatalog.Driver, error)
	ListDrivers(ctx context.Context, workspace string) ([]*workflowcatalog.Driver, error)
	GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error)
	ListVersions(ctx context.Context, workspace, driverID string) ([]*workflowcatalog.DriverVersion, error)
	ApproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
	UnapproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
	ActivateVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*WorkflowCatalogLifecycleResult, error)
	AuthorDriverVersion(context.Context, WorkflowCatalogAuthorVersionInput) (*WorkflowCatalogAuthorVersionResult, error)
	AuthorManagedDriverVersion(context.Context, WorkflowCatalogAuthorManagedVersionInput) (*WorkflowCatalogAuthorVersionResult, error)
}

type workflowCatalogStore struct{ client *Client }

var _ WorkflowCatalogTransport = (*workflowCatalogStore)(nil)

func (s *workflowCatalogStore) GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error) {
	return s.client.drivers.Get(ctx, workspace, driverID)
}

func (s *workflowCatalogStore) FindDriverByName(ctx context.Context, workspace, name string) (*workflowcatalog.Driver, error) {
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

func (s *workflowCatalogStore) ListDrivers(ctx context.Context, workspace string) ([]*workflowcatalog.Driver, error) {
	return s.client.drivers.List(ctx, workspace, store.DriverFilter{})
}

func (s *workflowCatalogStore) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	return s.client.versions.Get(ctx, workspace, versionID)
}

func (s *workflowCatalogStore) ListVersions(ctx context.Context, workspace, driverID string) ([]*workflowcatalog.DriverVersion, error) {
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

func (s *workflowCatalogStore) AuthorDriverVersion(
	ctx context.Context,
	input WorkflowCatalogAuthorVersionInput,
) (*WorkflowCatalogAuthorVersionResult, error) {
	return s.author(ctx, input, false, false)
}

func (s *workflowCatalogStore) AuthorManagedDriverVersion(
	ctx context.Context,
	input WorkflowCatalogAuthorManagedVersionInput,
) (*WorkflowCatalogAuthorVersionResult, error) {
	return s.author(ctx, input.WorkflowCatalogAuthorVersionInput, true, input.Activate)
}

type workflowCatalogAuthorVersionBody struct {
	RequestID        string            `json:"request_id"`
	ExpectedRevision uint64            `json:"expected_revision"`
	DriverName       string            `json:"driver_name"`
	VersionID        string            `json:"version_id"`
	SourceRef        string            `json:"source_ref"`
	SourceDigest     string            `json:"source_digest"`
	BundleRef        string            `json:"bundle_ref"`
	BundleDigest     string            `json:"bundle_digest"`
	Runtime          string            `json:"runtime"`
	Manifest         map[string]string `json:"manifest,omitempty"`
	BuildDiagnostics string            `json:"build_diagnostics,omitempty"`
}

func (s *workflowCatalogStore) author(
	ctx context.Context,
	input WorkflowCatalogAuthorVersionInput,
	managed, activate bool,
) (*WorkflowCatalogAuthorVersionResult, error) {
	if input.ExpectedRevision >= uint64(math.MaxInt64) {
		return nil, fmt.Errorf("workflow catalog expected revision cannot advance within FleetDB's signed persistence range: %w", ErrWorkflowCatalogInvalid)
	}
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, fmt.Errorf("workflow catalog delegated actor is invalid: %w", ErrWorkflowCatalogInvalid)
	}
	body := workflowCatalogAuthorVersionBody{
		RequestID: input.RequestID, ExpectedRevision: input.ExpectedRevision,
		DriverName: input.DriverName, VersionID: input.VersionID,
		SourceRef: input.SourceRef, SourceDigest: input.SourceDigest,
		BundleRef: input.BundleRef, BundleDigest: input.BundleDigest,
		Runtime: input.Runtime, Manifest: cloneWorkflowCatalogManifest(input.Manifest),
		BuildDiagnostics: input.BuildDiagnostics,
	}
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/drivers/" +
		pathEscape(input.DriverID) + "/versions/author"
	requestBody := any(body)
	if managed {
		path += "-managed"
		requestBody = struct {
			workflowCatalogAuthorVersionBody
			Activate bool `json:"activate"`
		}{workflowCatalogAuthorVersionBody: body, Activate: activate}
	}
	var out WorkflowCatalogAuthorVersionResult
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, requestBody, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func cloneWorkflowCatalogManifest(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
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
