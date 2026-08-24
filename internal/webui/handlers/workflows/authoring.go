package workflows

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

// workflowSummary is one row of GET …/workflows: a driver plus the
// approval / trust / provenance of its active version, so the Workflows UI
// can render the list without a per-row round trip.
type workflowSummary struct {
	DriverID        string                  `json:"driver_id"`
	Name            string                  `json:"name"`
	Status          domain.DriverStatus     `json:"status"`
	ActiveVersionID string                  `json:"active_version_id,omitempty"`
	BuiltIn         bool                    `json:"built_in"`
	Approved        bool                    `json:"approved"`
	EffectiveTrust  domain.DriverTrustLevel `json:"effective_trust,omitempty"`
	Provenance      string                  `json:"provenance,omitempty"`
	SelectedBy      string                  `json:"selected_by,omitempty"`
}

// listWorkflows serves GET /api/workspaces/{ws}/workflows.
func (m *Module) listWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ws := r.PathValue("ws")
	drivers, err := m.store.Drivers().List(ctx, ws, store.DriverFilter{})
	if err != nil {
		writeDomainError(w, err, "list workflows failed")
		return
	}
	sort.SliceStable(drivers, func(i, j int) bool { return drivers[i].Name < drivers[j].Name })
	items := make([]workflowSummary, 0, len(drivers))
	for _, drv := range drivers {
		item := workflowSummary{
			DriverID:        drv.DriverID,
			Name:            drv.Name,
			Status:          drv.Status,
			ActiveVersionID: drv.ActiveVersionID,
			BuiltIn:         isBuiltinWorkflowName(drv.Name),
		}
		if drv.ActiveVersionID != "" {
			item.SelectedBy = strings.TrimSpace(drv.Metadata[driver.MetadataKeyActivationActor])
			if version, verr := m.store.DriverVersions().Get(ctx, ws, drv.ActiveVersionID); verr == nil {
				item.Approved = driver.DriverVersionApproved(drv, version)
				item.EffectiveTrust = driver.DriverVersionEffectiveTrust(drv, version)
				item.Provenance = version.Manifest["provenance"]
			} else {
				// The active version should always be readable; if it is not,
				// the row falls back to unapproved/blank trust (fail-safe: it
				// never renders an unverifiable version as trusted). Surface the
				// cause so a stale-looking row is diagnosable rather than silent.
				item.EffectiveTrust = "unknown"
				slog.Warn("listWorkflows: active version read failed",
					"ws", ws, "workflow", drv.Name, "version", drv.ActiveVersionID, "err", verr.Error())
			}
		}
		items = append(items, item)
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"workflows": items})
}

// approveWorkflowVersion serves POST …/versions/{versionId}/approve.
func (m *Module) approveWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	m.setVersionApproval(w, r, true)
}

// unapproveWorkflowVersion serves POST …/versions/{versionId}/unapprove.
func (m *Module) unapproveWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	m.setVersionApproval(w, r, false)
}

// setVersionApproval is the shared body for approve/unapprove: it resolves the
// driver, applies the approval change, and returns the refreshed version row.
func (m *Module) setVersionApproval(w http.ResponseWriter, r *http.Request, approve bool) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	versionID := strings.TrimSpace(r.PathValue("versionId"))
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
	var (
		drv     *domain.Driver
		version *domain.DriverVersion
	)
	if approve {
		drv, version, err = driver.ApproveDriverVersion(ctx, m.store, ws, driverID, versionID)
	} else {
		drv, version, err = driver.UnapproveDriverVersion(ctx, m.store, ws, driverID, versionID)
	}
	if err != nil {
		writeVersioningError(w, name, err)
		return
	}
	writeVersionAction(w, drv, version)
}
