// Package executionmanagement exposes authenticated operator management
// routes for Execution-owned resources. Request bodies carry intent only;
// workspace scope and authority are derived by the server.
package executionmanagement

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxWorkerProfileRequestBytes = 1 << 20

type Config struct {
	WorkerProfiles execution.WorkerProfileAPI
	Authority      workflowcataloghttp.OperatorAuthorityResolver
}

type Module struct {
	workerProfiles execution.WorkerProfileAPI
	authority      workflowcataloghttp.OperatorAuthorityResolver
}

func New(config Config) *Module {
	return &Module{workerProfiles: config.WorkerProfiles, authority: config.Authority}
}

func (module *Module) Register(mux *http.ServeMux) {
	if module == nil || mux == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/execution/worker-profiles", module.createWorkerProfile)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/execution/worker-profiles/{profileId}", module.updateWorkerProfile)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/execution/worker-profiles/{profileId}", module.deleteWorkerProfile)
}

func (module *Module) createWorkerProfile(w http.ResponseWriter, request *http.Request) {
	workspace, ok := canonicalWorkspace(w, request)
	if !ok {
		return
	}
	var command execution.CreateWorkerProfileCommand
	if err := decodeOneObject(w, request, &command); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	command.WorkspaceKey = workspace
	command.RequestID = "worker-profile-create:" + strings.TrimSpace(command.ProfileID)
	auth, ok := module.resolveOperator(w, request, workspace, execution.ActionCreateWorkerProfile)
	if !ok {
		return
	}
	if module.workerProfiles == nil {
		writeMappedError(w, execution.ErrUnavailable)
		return
	}
	profile, err := module.workerProfiles.CreateWorkerProfile(request.Context(), auth, command)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (module *Module) updateWorkerProfile(w http.ResponseWriter, request *http.Request) {
	workspace, ok := canonicalWorkspace(w, request)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(request.PathValue("profileId"))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "profile id is required")
		return
	}
	var patch execution.WorkerProfilePatch
	if err := decodeOneObject(w, request, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(w, request, workspace, execution.ActionUpdateWorkerProfile)
	if !ok {
		return
	}
	if module.workerProfiles == nil {
		writeMappedError(w, execution.ErrUnavailable)
		return
	}
	profile, err := module.workerProfiles.UpdateWorkerProfile(request.Context(), auth, execution.UpdateWorkerProfileCommand{
		WorkspaceKey: workspace, RequestID: "worker-profile-update:" + profileID, ProfileID: profileID, Patch: patch,
	})
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (module *Module) deleteWorkerProfile(w http.ResponseWriter, request *http.Request) {
	workspace, ok := canonicalWorkspace(w, request)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(request.PathValue("profileId"))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "profile id is required")
		return
	}
	if err := requireEmptyBody(w, request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(w, request, workspace, execution.ActionDeleteWorkerProfile)
	if !ok {
		return
	}
	if module.workerProfiles == nil {
		writeMappedError(w, execution.ErrUnavailable)
		return
	}
	if _, err := module.workerProfiles.DeleteWorkerProfile(request.Context(), auth, execution.DeleteWorkerProfileCommand{
		WorkspaceKey: workspace, RequestID: "worker-profile-delete:" + profileID, ProfileID: profileID,
	}); err != nil {
		writeMappedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func canonicalWorkspace(w http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil {
		writeError(w, http.StatusBadRequest, "invalid", "canonical workspace is required")
		return "", false
	}
	workspace := strings.TrimSpace(middleware.WorkspaceFromContext(request.Context()))
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "invalid", "canonical workspace is required")
		return "", false
	}
	return workspace, true
}

func (module *Module) resolveOperator(w http.ResponseWriter, request *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, bool) {
	if module.authority == nil {
		writeMappedError(w, execution.ErrUnavailable)
		return authority.OperatorAuthority{}, false
	}
	auth, err := module.authority.ResolveOperatorAuthority(request, workspace, action)
	if err != nil {
		writeMappedError(w, err)
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func decodeOneObject(w http.ResponseWriter, request *http.Request, output any) error {
	err := serverhandler.DecodeOneJSON(w, request, output, serverhandler.JSONDecodeOptions{
		MaxBytes: maxWorkerProfileRequestBytes, DisallowUnknownFields: true,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		return errors.New("worker profile request must contain exactly one JSON object")
	}
	if err != nil {
		return errors.New("invalid worker profile JSON: " + err.Error())
	}
	return nil
}

func requireEmptyBody(w http.ResponseWriter, request *http.Request) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxWorkerProfileRequestBytes))
	if err != nil {
		return errors.New("read worker profile request: " + err.Error())
	}
	if strings.TrimSpace(string(data)) != "" {
		return errors.New("delete worker profile request body must be empty")
	}
	return nil
}

func writeMappedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflowcataloghttp.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "unauthenticated", "operator authentication required")
	case errors.Is(err, authority.ErrWorkspaceMismatch), errors.Is(err, authority.ErrAdmissionDenied),
		errors.Is(err, authority.ErrActionNotAllowed):
		writeError(w, http.StatusForbidden, "forbidden", "operator is not allowed to manage this workspace")
	case errors.Is(err, execution.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, execution.ErrNotFound), errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, execution.ErrConflict), errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, execution.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "worker profile management failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
