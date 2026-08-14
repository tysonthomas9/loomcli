package skills

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Module registers store-backed skill routes behind the file-browser session
// capability boundary. Whole-skill CRUD is role-scope only; workspace
// mutations return amendment A3's read-only response, and skill packs remain
// CLI-only in this module.
type Module struct {
	handler *Handler
	access  middleware.Middleware
}

// NewModule constructs a skills module. The optional access configuration has
// the same open-mode and remote-RBAC behavior as the workspace file module.
func NewModule(st store.Store, accessCfg ...middleware.FileAccessConfig) *Module {
	cfg := middleware.FileAccessConfig{}
	if len(accessCfg) > 0 {
		cfg = accessCfg[0]
	}
	return &Module{
		handler: &Handler{Store: st},
		access:  middleware.FileAccess(cfg),
	}
}

// Register installs reads for both scopes and role-scope whole-skill and
// per-document mutations. Workspace mutation patterns are also registered so
// they return the A3-specific refusal instead of a generic 405.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.handler == nil || m.handler.Store == nil {
		return
	}
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, m.access(h))
	}

	handle("GET /api/workspaces/{ws}/skill-capabilities", m.handler.getCapabilities)
	handle("GET /api/workspaces/{ws}/skills", m.handler.getCatalog)
	handle("POST /api/workspaces/{ws}/skills", m.handler.createWorkspaceSkill)
	handle("GET /api/workspaces/{ws}/skills/{name}", m.handler.getWorkspaceSkill)
	handle("PATCH /api/workspaces/{ws}/skills/{name}", m.handler.patchWorkspaceSkill)
	handle("DELETE /api/workspaces/{ws}/skills/{name}", m.handler.deleteWorkspaceSkill)
	handle("POST /api/workspaces/{ws}/roles/{role}/skills", m.handler.createRoleSkill)
	handle("GET /api/workspaces/{ws}/roles/{role}/skills/{name}", m.handler.getRoleSkill)
	handle("PATCH /api/workspaces/{ws}/roles/{role}/skills/{name}", m.handler.patchRoleSkill)
	handle("DELETE /api/workspaces/{ws}/roles/{role}/skills/{name}", m.handler.deleteRoleSkill)

	handle("GET /api/workspaces/{ws}/skills/{name}/files/{path...}", m.handler.getWorkspaceSkillFile)
	handle("PUT /api/workspaces/{ws}/skills/{name}/files/{path...}", m.handler.putWorkspaceSkillFile)
	handle("DELETE /api/workspaces/{ws}/skills/{name}/files/{path...}", m.handler.deleteWorkspaceSkillFile)
	handle("GET /api/workspaces/{ws}/roles/{role}/skills/{name}/files/{path...}", m.handler.getRoleSkillFile)
	handle("PUT /api/workspaces/{ws}/roles/{role}/skills/{name}/files/{path...}", m.handler.putRoleSkillFile)
	handle("DELETE /api/workspaces/{ws}/roles/{role}/skills/{name}/files/{path...}", m.handler.deleteRoleSkillFile)
}
