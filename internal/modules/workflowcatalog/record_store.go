package workflowcatalog

import (
	"context"
)

type DriverCreate struct {
	WorkspaceKey string
	DriverID     string
	Name         string
	OwnerType    DriverOwnerType
	OwnerRef     string
	Description  string
	// Status exists for generic draft creation compatibility. FleetDB rejects
	// active here; version activation belongs to Workflow Catalog's typed
	// ActivateVersion command.
	Status DriverStatus
	// TrustLevel gates sandbox placement (§7 step 9). Stamped by the
	// registration path server-side, never from client input; empty means
	// untrusted (fail closed).
	TrustLevel DriverTrustLevel
	Metadata   map[string]string
}

type DriverFilter struct {
	Name   string
	Status DriverStatus
	Limit  int
}

type DriverUpdate struct {
	Name        *string
	OwnerType   *DriverOwnerType
	OwnerRef    *string
	Description *string
	// Status retains non-activation administration such as disable/archive.
	// FleetDB rejects DriverStatusActive on this generic surface.
	Status *DriverStatus
	// TrustLevel is the explicit ops elevation/demotion path; workflow
	// runtimes never reach a surface that sets it.
	TrustLevel *DriverTrustLevel
	Metadata   *map[string]string
}

type DriverStore interface {
	Create(ctx context.Context, in DriverCreate) (*Driver, error)
	Get(ctx context.Context, workspaceKey, driverID string) (*Driver, error)
	List(ctx context.Context, workspaceKey string, filter DriverFilter) ([]*Driver, error)
	Update(ctx context.Context, workspaceKey, driverID string, patch DriverUpdate) (*Driver, error)
}

type DriverVersionCreate struct {
	WorkspaceKey     string
	VersionID        string
	DriverID         string
	Version          int
	SourceRef        string
	SourceDigest     string
	BundleRef        string
	BundleDigest     string
	Runtime          string
	Manifest         map[string]string
	BuildDiagnostics string
	ValidationStatus DriverVersionValidationStatus
	CreatedBy        string
}

type DriverVersionFilter struct {
	DriverID         string
	ValidationStatus DriverVersionValidationStatus
	Limit            int
}

type DriverVersionStore interface {
	Create(ctx context.Context, in DriverVersionCreate) (*DriverVersion, error)
	Get(ctx context.Context, workspaceKey, versionID string) (*DriverVersion, error)
	List(ctx context.Context, workspaceKey string, filter DriverVersionFilter) ([]*DriverVersion, error)
}
