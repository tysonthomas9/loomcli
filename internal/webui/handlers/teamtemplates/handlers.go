package teamtemplates

import (
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type catalogResponse struct {
	TeamTemplates []catalogTeamTemplate `json:"templates"`
}

type catalogTeamTemplate struct {
	ID            string             `json:"id"`
	Label         string             `json:"label"`
	Description   string             `json:"description"`
	Revision      int                `json:"revision"`
	SchemaVersion int                `json:"schema_version"`
	Roles         []catalogAgentRole `json:"roles"`
	Agents        []catalogAgent     `json:"agents"`
}

type catalogAgentRole struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	DisplayLabel string `json:"display_label"`
	Description  string `json:"description"`
}

type catalogAgent struct {
	Name     string `json:"name"`
	RoleName string `json:"role_name"`
}

type applyRequest struct {
	DryRun bool `json:"dry_run"`
}

type applyResponse struct {
	Status string                   `json:"status"`
	Report teamtemplate.ApplyReport `json:"report"`
}

func HandleCatalog(service TeamTemplateService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamTemplates, err := service.CatalogTeamTemplates(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		response := catalogResponse{TeamTemplates: make([]catalogTeamTemplate, 0, len(teamTemplates))}
		for _, tpl := range teamTemplates {
			response.TeamTemplates = append(response.TeamTemplates, catalogEntry(tpl))
		}
		handler.WriteJSON(w, http.StatusOK, response)
	}
}

func HandleApply(service TeamTemplateService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Applying a template can materialize several Git worktrees. It is still
		// request-owned, but must not be cut off by the server-wide 30s response
		// deadline after the mutations have completed successfully.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		var request applyRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := handler.ReadJSON(w, r, &request); err != nil {
				handler.HandleServiceError(w, err)
				return
			}
		}
		workspaceKey := middleware.WorkspaceFromContext(r.Context())
		if workspaceKey == "" {
			workspaceKey = r.PathValue("ws")
		}
		report, err := service.ApplyTeamTemplate(
			r.Context(),
			strings.TrimSpace(workspaceKey),
			strings.TrimSpace(r.PathValue("id")),
			request.DryRun,
		)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		// "done" means the synchronous apply finished. Per-step failures stay
		// in the report so a partial result remains inspectable and retryable.
		handler.WriteJSON(w, http.StatusOK, applyResponse{Status: "done", Report: report})
	}
}

func catalogEntry(tpl teamtemplate.TeamTemplate) catalogTeamTemplate {
	out := catalogTeamTemplate{
		ID:            tpl.ID,
		Label:         tpl.Label,
		Description:   tpl.Description,
		Revision:      tpl.Revision,
		SchemaVersion: tpl.SchemaVersion,
		Roles:         make([]catalogAgentRole, 0, len(tpl.Roles)),
		Agents:        make([]catalogAgent, 0, len(tpl.Agents)),
	}
	for _, role := range tpl.Roles {
		out.Roles = append(out.Roles, catalogAgentRole{
			Name:         role.Name,
			Kind:         role.Kind,
			DisplayLabel: role.DisplayLabel,
			Description:  role.Description,
		})
	}
	for _, agent := range tpl.Agents {
		out.Agents = append(out.Agents, catalogAgent{Name: agent.Name, RoleName: agent.RoleName})
	}
	return out
}
