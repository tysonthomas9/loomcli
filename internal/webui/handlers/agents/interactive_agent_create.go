package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type canonicalInteractiveAgentCreateRequest struct {
	WorkspaceKey     string   `json:"workspace_key,omitempty"`
	Name             string   `json:"name"`
	RoleName         string   `json:"role_name"`
	Kind             string   `json:"kind,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
	PromptFile       string   `json:"prompt_file,omitempty"`
	Auto             bool     `json:"auto,omitempty"`
	Backend          string   `json:"backend,omitempty"`
	FallbackBackends []string `json:"fallback_backends,omitempty"`
	Repos            []string `json:"repos,omitempty"`
	RepoGroups       []string `json:"repo_groups,omitempty"`
	CrossRepo        bool     `json:"cross_repo,omitempty"`
	Parent           string   `json:"parent,omitempty"`
}

type canonicalInteractiveAgentResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	RoleName   string   `json:"role_name"`
	Backend    string   `json:"backend,omitempty"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}

// createCanonicalInteractiveAgent replaces the retired role-agent assignment
// write. Background role agents use AgentProvisioning and never enter here;
// this route creates only browser-owned interactive identities.
func (m *Module) createCanonicalInteractiveAgent(
	w http.ResponseWriter,
	r *http.Request,
	workspace string,
	body []byte,
) {
	if m == nil || m.agentIdentityCreator == nil {
		writeAgentRecordError(w, agentsmodule.ErrUnavailable, "canonical Agents is unavailable")
		return
	}
	input, validationError := parseCanonicalInteractiveAgentCreate(body, workspace)
	if validationError != "" {
		writeAgentValidationError(w, validationError)
		return
	}

	if !m.ensureCanonicalInteractiveRole(w, r, workspace, input) {
		return
	}
	metadata, err := agentsmodule.WithRuntimeMetadata(nil, agentsmodule.RuntimeMetadata{
		RoleKind: string(domain.RoleKindInteractive), Backend: input.Backend,
		FallbackBackends: input.FallbackBackends, Repos: input.Repos,
		RepoGroups: input.RepoGroups, CrossRepo: input.CrossRepo,
	})
	if err != nil {
		writeAgentRecordError(w, err, "canonicalize interactive agent runtime policy failed")
		return
	}
	auth, ok := m.resolveAgentRecordAuthority(w, r, workspace, agentsmodule.ActionCreateAgent)
	if !ok {
		return
	}
	record, err := m.agentIdentityCreator.CreateAgent(r.Context(), auth, agentsmodule.CreateAgentCommand{
		WorkspaceKey: workspace, AgentID: input.Name, Name: input.Name,
		Kind:         agentsmodule.AgentKindLead,
		Behavior:     agentsmodule.BehaviorReference{RoleName: input.RoleName},
		DesiredState: agentsmodule.DesiredRunning, PlacementPolicy: "interactive",
		MaxInstances: 1, Metadata: metadata,
	})
	if err != nil {
		writeAgentRecordError(w, err, "create canonical interactive agent failed")
		return
	}
	if record == nil {
		writeAgentRecordError(w, agentsmodule.ErrInvalidPersistedState, "create canonical interactive agent returned no identity")
		return
	}
	broadcastAgentRefresh(m.hub, workspace, record.AgentID, r.Header.Get("X-Actor"))
	writeCanonicalInteractiveAgentResponse(w, record.AgentID, input)
}

func writeCanonicalInteractiveAgentResponse(
	w http.ResponseWriter,
	agentID string,
	input canonicalInteractiveAgentCreateRequest,
) {
	handler.WriteJSON(w, http.StatusCreated, canonicalInteractiveAgentResponse{
		ID: agentID, Name: agentID, Kind: string(domain.RoleKindInteractive),
		RoleName: input.RoleName, Backend: input.Backend,
		Repos: append([]string{}, input.Repos...), RepoGroups: append([]string{}, input.RepoGroups...),
		CrossRepo: input.CrossRepo,
	})
}

func parseCanonicalInteractiveAgentCreate(
	body []byte,
	workspace string,
) (canonicalInteractiveAgentCreateRequest, string) {
	var input canonicalInteractiveAgentCreateRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, "invalid request body"
	}
	if input.WorkspaceKey != "" && strings.TrimSpace(input.WorkspaceKey) != workspace {
		return input, "workspace_key must match request workspace"
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	input.RoleName = normalizeInteractiveRoleName(input.RoleName)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.PromptFile = strings.TrimSpace(input.PromptFile)
	input.Backend = strings.TrimSpace(input.Backend)
	if !validStoredAgentName(input.Name) {
		return input, "invalid agent name: use 1-100 lowercase letters, numbers, hyphens, dots, or underscores"
	}
	if input.RoleName == "" {
		return input, "role_name required"
	}
	if input.Kind == string(domain.RoleKindWorker) || input.Auto {
		return input, "background agents must be created from a role behavior through AgentProvisioning"
	}
	if input.Kind != "" && input.Kind != string(domain.RoleKindInteractive) {
		return input, "kind must be interactive"
	}
	if strings.TrimSpace(input.Parent) != "" {
		return input, "parent is runtime-owned and cannot be set during agent creation"
	}
	return input, ""
}

func (m *Module) ensureCanonicalInteractiveRole(
	w http.ResponseWriter,
	r *http.Request,
	workspace string,
	input canonicalInteractiveAgentCreateRequest,
) bool {
	existing, err := m.agentIdentityCreator.GetRole(r.Context(), workspace, input.RoleName)
	if err == nil {
		if existing == nil {
			writeAgentRecordError(w, agentsmodule.ErrInvalidPersistedState, "canonical Agents returned no Role")
			return false
		}
		if strings.ToLower(strings.TrimSpace(existing.Kind)) != string(domain.RoleKindInteractive) {
			writeAgentConflictError(w, "role already exists and is not interactive")
			return false
		}
		if input.Prompt != "" && strings.TrimSpace(existing.Prompt) != input.Prompt {
			writeAgentConflictError(w, "role already exists with a different prompt")
			return false
		}
		if input.PromptFile != "" && strings.TrimSpace(existing.PromptFile) != input.PromptFile {
			writeAgentConflictError(w, "role already exists with a different prompt")
			return false
		}
		return true
	}
	if !errors.Is(err, agentsmodule.ErrNotFound) {
		writeAgentRecordError(w, err, "load canonical interactive Role failed")
		return false
	}
	auth, ok := m.resolveAgentRecordAuthority(w, r, workspace, agentsmodule.ActionCreateRole)
	if !ok {
		return false
	}
	description := "Interactive terminal agent"
	if domain.IsInteractiveRoleName(input.RoleName) {
		description = "Lead/orchestrator interactive"
	}
	_, err = m.agentIdentityCreator.CreateRole(r.Context(), auth, agentsmodule.CreateRoleCommand{
		WorkspaceKey: workspace,
		Role: agentsmodule.RoleDefinition{
			Name: input.RoleName, Kind: string(domain.RoleKindInteractive),
			Description: description, Prompt: input.Prompt, PromptFile: input.PromptFile,
			Backend: input.Backend,
		},
	})
	if err != nil {
		writeAgentRecordError(w, err, "create canonical interactive Role failed")
		return false
	}
	return true
}

func normalizeInteractiveRoleName(value string) string {
	trimmed := strings.TrimSpace(value)
	if domain.IsInteractiveRoleName(trimmed) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}
