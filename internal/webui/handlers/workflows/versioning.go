package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// versionItem is one row of GET …/versions.
type versionItem struct {
	Version        *domain.DriverVersion   `json:"version"`
	Active         bool                    `json:"active"`
	Approved       bool                    `json:"approved"`
	EffectiveTrust domain.DriverTrustLevel `json:"effective_trust"`
	Provenance     string                  `json:"provenance,omitempty"`
	SelectedBy     string                  `json:"selected_by,omitempty"`
	BundleVerified bool                    `json:"bundle_verified"`
}

// versionActionResponse is the shared body for activate/rollback.
type versionActionResponse struct {
	Driver         *domain.Driver          `json:"driver"`
	Version        *domain.DriverVersion   `json:"version"`
	Active         bool                    `json:"active"`
	Approved       bool                    `json:"approved"`
	EffectiveTrust domain.DriverTrustLevel `json:"effective_trust"`
}

func writeVersionAction(w http.ResponseWriter, drv *domain.Driver, version *domain.DriverVersion) {
	handler.WriteJSON(w, http.StatusOK, versionActionResponse{
		Driver:         drv,
		Version:        version,
		Active:         drv.ActiveVersionID == version.VersionID,
		Approved:       driver.DriverVersionApproved(drv, version),
		EffectiveTrust: driver.DriverVersionEffectiveTrust(drv, version),
	})
}

// writeVersioningError maps DEV-V5-33's error classes to HTTP status codes.
func writeVersioningError(w http.ResponseWriter, name string, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "rollback_target_missing"):
		writeError(w, http.StatusConflict, msg)
	case strings.Contains(msg, "builtin_track_invalid"), strings.Contains(msg, "not_builtin_workflow"):
		writeError(w, http.StatusBadRequest, msg)
	case strings.Contains(msg, "builtin_active_version_unavailable"):
		writeError(w, http.StatusConflict, msg)
	case errors.Is(err, packaged.ErrNotPackaged):
		if packaged.FailClosed() {
			writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("this Loom build ships no built-in workflow artifact for %s; reinstall Loom", name))
			return
		}
		writeError(w, http.StatusInternalServerError, msg)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, msg)
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, msg)
	default:
		writeError(w, http.StatusInternalServerError, msg)
	}
}

func (m *Module) listWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	if reservedWorkflowName(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is a reserved workflow name", name))
		return
	}
	ctx := r.Context()
	driverID, err := workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
	if err != nil {
		writeDomainError(w, err, "workflow not found")
		return
	}
	drv, err := m.store.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		writeDomainError(w, err, "workflow not found")
		return
	}
	versions, err := m.store.DriverVersions().List(ctx, ws, store.DriverVersionFilter{DriverID: driverID})
	if err != nil {
		writeDomainError(w, err, "list versions failed")
		return
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
	workDir := workflowdefs.BuiltinWorkflowWorkDir()
	selectedBy := strings.TrimSpace(drv.Metadata[driver.MetadataKeyActivationActor])
	items := make([]versionItem, 0, len(versions))
	for _, version := range versions {
		active := drv.ActiveVersionID == version.VersionID
		item := versionItem{
			Version:        version,
			Active:         active,
			Approved:       driver.DriverVersionApproved(drv, version),
			EffectiveTrust: driver.DriverVersionEffectiveTrust(drv, version),
			Provenance:     version.Manifest["provenance"],
			BundleVerified: driver.VerifyStagedBundle(workDir, version) == nil,
		}
		if active {
			item.SelectedBy = selectedBy
		}
		items = append(items, item)
	}
	payload := map[string]any{"driver_id": driverID, "versions": items}
	if isBuiltinWorkflowName(name) {
		info, err := workflowdefs.DescribeBuiltinVersions(ctx, m.store, ws, name)
		if err != nil {
			writeDomainError(w, err, "describe built-in versions failed")
			return
		}
		payload["builtin"] = info
	}
	handler.WriteJSON(w, http.StatusOK, payload)
}

type activateVersionRequest struct {
	Track string `json:"track,omitempty"`
}

func (m *Module) activateWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	versionID := strings.TrimSpace(r.PathValue("versionId"))
	if reservedWorkflowName(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is a reserved workflow name", name))
		return
	}
	var req activateVersionRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	track := driver.BuiltinTrackPinned
	if strings.TrimSpace(req.Track) != "" {
		parsed, ok := parseTrack(req.Track)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("builtin_track_invalid: track must be auto or pinned, got %q", req.Track))
			return
		}
		track = parsed
	}
	ctx := r.Context()
	driverID, err := workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
	if err != nil {
		writeDomainError(w, err, "workflow not found")
		return
	}
	if track == driver.BuiltinTrackAuto {
		// --track auto is valid only when versionID IS this build's packaged
		// built-in version.
		info, derr := workflowdefs.DescribeBuiltinVersions(ctx, m.store, ws, name)
		if derr != nil || info == nil || info.PackagedVersionID == "" || info.PackagedVersionID != versionID {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("builtin_track_invalid: track auto requires %q to be this build's packaged built-in version", versionID))
			return
		}
	}
	drv, version, err := driver.ActivateDriverVersionWithOptions(ctx, m.store, ws, driverID, versionID, driver.ActivationOptions{
		Actor:  driver.ActivationActorUser,
		Reason: driver.ActivationReasonOperator,
		Track:  track,
	})
	if err != nil {
		writeVersioningError(w, name, err)
		return
	}
	writeVersionAction(w, drv, version)
}

type syncBuiltinRequest struct {
	Track string `json:"track,omitempty"`
}

func (m *Module) syncBuiltinWorkflow(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	if !isBuiltinWorkflowName(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not_builtin_workflow: %q is not a built-in workflow", name))
		return
	}
	var req syncBuiltinRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	opts := workflowdefs.BuiltinSyncOptions{}
	if strings.TrimSpace(req.Track) != "" {
		parsed, ok := parseTrack(req.Track)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("builtin_track_invalid: track must be auto or pinned, got %q", req.Track))
			return
		}
		opts.ForceTrack = parsed
	}
	res, err := workflowdefs.SyncBuiltinWorkflow(r.Context(), m.store, ws, name, opts)
	if err != nil {
		writeVersioningError(w, name, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, res)
}

type rollbackRequest struct {
	VersionID string `json:"version_id,omitempty"`
}

func (m *Module) rollbackWorkflow(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	if reservedWorkflowName(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is a reserved workflow name", name))
		return
	}
	var req rollbackRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	driverID, err := workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
	if err != nil {
		writeDomainError(w, err, "workflow not found")
		return
	}
	drv, version, err := driver.RollbackDriverVersion(ctx, m.store, ws, driverID, strings.TrimSpace(req.VersionID))
	if err != nil {
		writeVersioningError(w, name, err)
		return
	}
	writeVersionAction(w, drv, version)
}

func parseTrack(value string) (driver.BuiltinTrack, bool) {
	switch driver.BuiltinTrack(strings.TrimSpace(value)) {
	case driver.BuiltinTrackAuto:
		return driver.BuiltinTrackAuto, true
	case driver.BuiltinTrackPinned:
		return driver.BuiltinTrackPinned, true
	default:
		return "", false
	}
}

// decodeOptionalJSON decodes an optional JSON body into v. An empty body is
// treated as an empty object. On a malformed body it writes a 400 and returns
// false.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
