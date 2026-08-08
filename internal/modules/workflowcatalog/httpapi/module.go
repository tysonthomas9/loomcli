// Package httpapi exposes the Workflow Catalog's management HTTP surface.
// Authentication is resolved outside request DTOs and all product behavior is
// delegated to the capability's public API.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/httptransport"
)

const maxLifecycleRequestBytes = 1 << 20

// ErrUnauthenticated is returned by resolvers when the request has no valid
// credential from which an operator authority can be derived. It aliases the
// platform sentinel so every inbound adapter shares one classification.
var ErrUnauthenticated = authority.ErrUnauthenticated

// OperatorAuthorityResolver verifies request credentials and derives one
// action- and workspace-scoped operator authority. Implementations must never
// accept authority, actor, workspace scope, or trust from a request body.
type OperatorAuthorityResolver interface {
	ResolveOperatorAuthority(r *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error)
}

// BuiltInWorkflowClassifier reports whether an exact driver ID belongs to the
// process's authoritative built-in workflow registry.
type BuiltInWorkflowClassifier func(driverID string) bool

// Module is the capability-owned inbound HTTP adapter.
type Module struct {
	catalog              workflowcatalog.API
	authority            OperatorAuthorityResolver
	workspaceFromContext func(context.Context) string
	isBuiltInWorkflow    BuiltInWorkflowClassifier
}

// New constructs the Workflow Catalog HTTP adapter. Missing dependencies fail
// closed at request time so a misconfigured route never bypasses authority.
func New(catalog workflowcatalog.API, resolver OperatorAuthorityResolver, workspaceFromContext func(context.Context) string, isBuiltInWorkflow BuiltInWorkflowClassifier) *Module {
	return &Module{
		catalog: catalog, authority: resolver, workspaceFromContext: workspaceFromContext,
		isBuiltInWorkflow: isBuiltInWorkflow,
	}
}

// Register adds only Workflow Catalog-owned routes. The legacy static
// GET /workflows route remains owned by the existing builtin-source handler.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/workflow-catalog/drivers", m.listDrivers)
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/{name}/versions", m.listVersions)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/approve", m.approveVersion)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/unapprove", m.unapproveVersion)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/activate", m.activateVersion)
}

type workflowListItem struct {
	DriverID        string                       `json:"driver_id"`
	Name            string                       `json:"name"`
	Status          workflowcatalog.DriverStatus `json:"status"`
	ActiveVersionID string                       `json:"active_version_id"`
	Revision        uint64                       `json:"revision"`
	BuiltIn         bool                         `json:"built_in"`
}

type workflowListResponse struct {
	Workflows []workflowListItem `json:"workflows"`
}

func (m *Module) listDrivers(w http.ResponseWriter, r *http.Request) {
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "workspace is required")
		return
	}
	if m.catalog == nil || m.isBuiltInWorkflow == nil {
		writeMappedError(w, workflowcatalog.ErrUnavailable)
		return
	}
	drivers, err := m.catalog.ListDrivers(r.Context(), workspace)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	items := make([]workflowListItem, 0, len(drivers))
	for _, driver := range drivers {
		if driver == nil {
			writeMappedError(w, workflowcatalog.ErrInvalidPersistedState)
			return
		}
		items = append(items, workflowListItem{
			DriverID:        driver.DriverID,
			Name:            driver.Name,
			Status:          driver.Status,
			ActiveVersionID: driver.ActiveVersionID,
			Revision:        driver.Revision,
			BuiltIn:         m.isBuiltInWorkflow(driver.DriverID),
		})
	}
	writeJSON(w, http.StatusOK, workflowListResponse{Workflows: items})
}

type workflowVersionsResponse struct {
	Driver          *workflowcatalog.Driver          `json:"driver"`
	DriverID        string                           `json:"driver_id"`
	ActiveVersionID string                           `json:"active_version_id"`
	Revision        uint64                           `json:"revision"`
	Versions        []*workflowcatalog.DriverVersion `json:"versions"`
}

func (m *Module) listVersions(w http.ResponseWriter, r *http.Request) {
	workspace, workspaceOK := m.canonicalWorkspace(r)
	name := strings.TrimSpace(r.PathValue("name"))
	if !workspaceOK || name == "" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "workspace and workflow name are required")
		return
	}
	if m.catalog == nil {
		writeMappedError(w, workflowcatalog.ErrUnavailable)
		return
	}
	set, err := m.catalog.ListVersions(r.Context(), workspace, name)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if set == nil || set.Driver == nil {
		writeMappedError(w, workflowcatalog.ErrInvalidPersistedState)
		return
	}
	writeJSON(w, http.StatusOK, workflowVersionsResponse{
		Driver:          set.Driver,
		DriverID:        set.Driver.DriverID,
		ActiveVersionID: set.Driver.ActiveVersionID,
		Revision:        set.Driver.Revision,
		Versions:        nonNilVersions(set.Versions),
	})
}

func nonNilVersions(versions []*workflowcatalog.DriverVersion) []*workflowcatalog.DriverVersion {
	if versions == nil {
		return []*workflowcatalog.DriverVersion{}
	}
	return versions
}

type versionLifecycleRequest struct {
	ExpectedRevision *uint64 `json:"expected_revision,omitempty"`
}

type versionLifecycleResponse struct {
	Action  string                         `json:"action"`
	Driver  *workflowcatalog.Driver        `json:"driver"`
	Version *workflowcatalog.DriverVersion `json:"version"`
}

func (m *Module) approveVersion(w http.ResponseWriter, r *http.Request) {
	m.versionAction(w, r, "approve", workflowcatalog.ActionApproveVersion)
}

func (m *Module) unapproveVersion(w http.ResponseWriter, r *http.Request) {
	m.versionAction(w, r, "unapprove", workflowcatalog.ActionUnapproveVersion)
}

func (m *Module) activateVersion(w http.ResponseWriter, r *http.Request) {
	m.versionAction(w, r, "activate", workflowcatalog.ActionActivateVersion)
}

func (m *Module) versionAction(w http.ResponseWriter, r *http.Request, actionLabel string, action authority.Action) {
	workspace, workspaceOK := m.canonicalWorkspace(r)
	name := strings.TrimSpace(r.PathValue("name"))
	versionID := r.PathValue("versionId")
	if !workspaceOK || name == "" || strings.TrimSpace(versionID) == "" || versionID != strings.TrimSpace(versionID) {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "workspace, workflow name and version id are required")
		return
	}
	if m.catalog == nil {
		writeMappedError(w, workflowcatalog.ErrUnavailable)
		return
	}
	if m.authority == nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "workflow catalog authority is unavailable")
		return
	}
	auth, err := m.authority.ResolveOperatorAuthority(r, workspace, action)
	if err != nil {
		writeMappedError(w, err)
		return
	}

	command, err := m.lifecycleCommand(w, r, workspace, name, versionID)
	if err != nil {
		writeMappedError(w, err)
		return
	}

	result, err := m.executeVersionAction(r.Context(), action, auth, command)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if result == nil || result.Driver == nil || result.Version == nil {
		writeMappedError(w, workflowcatalog.ErrInvalidPersistedState)
		return
	}
	writeJSON(w, http.StatusOK, versionLifecycleResponse{
		Action: actionLabel, Driver: result.Driver, Version: result.Version,
	})
}

func (m *Module) lifecycleCommand(w http.ResponseWriter, r *http.Request, workspace, name, versionID string) (workflowcatalog.VersionCommand, error) {
	request, err := decodeLifecycleRequest(w, r)
	if err != nil {
		return workflowcatalog.VersionCommand{}, fmt.Errorf("decode lifecycle request: %w", workflowcatalog.ErrInvalid)
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision == 0 {
		return workflowcatalog.VersionCommand{}, fmt.Errorf("expected revision must be at least 1: %w", workflowcatalog.ErrInvalid)
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision > workflowcatalog.MaxExpectedRevision {
		return workflowcatalog.VersionCommand{}, fmt.Errorf("expected revision cannot advance within FleetDB's signed persistence range: %w", workflowcatalog.ErrInvalid)
	}
	driver, err := m.catalog.GetDriver(r.Context(), workspace, name)
	if err != nil {
		return workflowcatalog.VersionCommand{}, err
	}
	if driver == nil || strings.TrimSpace(driver.DriverID) == "" {
		return workflowcatalog.VersionCommand{}, workflowcatalog.ErrInvalidPersistedState
	}
	expectedRevision := driver.Revision
	if request.ExpectedRevision != nil {
		expectedRevision = *request.ExpectedRevision
	} else if expectedRevision == 0 {
		return workflowcatalog.VersionCommand{}, workflowcatalog.ErrInvalidPersistedState
	}
	if expectedRevision > workflowcatalog.MaxExpectedRevision {
		return workflowcatalog.VersionCommand{}, fmt.Errorf("expected revision cannot advance within FleetDB's signed persistence range: %w", workflowcatalog.ErrInvalid)
	}
	return workflowcatalog.VersionCommand{
		WorkspaceKey:     workspace,
		DriverID:         driver.DriverID,
		VersionID:        versionID,
		ExpectedRevision: expectedRevision,
	}, nil
}

// canonicalWorkspace accepts only the identity resolved by the outer
// workspace middleware. PathValue may be an alias and is intentionally never
// used for persistence or authority scope.
func (m *Module) canonicalWorkspace(r *http.Request) (string, bool) {
	if m == nil || r == nil || strings.TrimSpace(r.PathValue("ws")) == "" || m.workspaceFromContext == nil {
		return "", false
	}
	workspace := strings.TrimSpace(m.workspaceFromContext(r.Context()))
	return workspace, workspace != ""
}

func (m *Module) executeVersionAction(ctx context.Context, action authority.Action, auth authority.OperatorAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	switch action {
	case workflowcatalog.ActionApproveVersion:
		return m.catalog.ApproveVersion(ctx, auth, command)
	case workflowcatalog.ActionUnapproveVersion:
		return m.catalog.UnapproveVersion(ctx, auth, command)
	case workflowcatalog.ActionActivateVersion:
		return m.catalog.ActivateVersion(ctx, auth, command)
	default:
		return nil, authority.ErrAdmissionDenied
	}
}

func decodeLifecycleRequest(w http.ResponseWriter, r *http.Request) (versionLifecycleRequest, error) {
	var request versionLifecycleRequest
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return request, nil
	}
	if err := httptransport.DecodeOneJSONRequest(w, r, &request, httptransport.JSONDecodeOptions{
		MaxBytes:              maxLifecycleRequestBytes,
		DisallowUnknownFields: true,
	}); err != nil {
		if errors.Is(err, io.EOF) {
			return request, nil
		}
		if errors.Is(err, httptransport.ErrTrailingJSON) {
			return versionLifecycleRequest{}, errors.New("request body contains trailing content")
		}
		return versionLifecycleRequest{}, err
	}
	return request, nil
}
