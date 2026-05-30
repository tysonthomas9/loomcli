package workflow

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type tsContextRuntimeProfile struct {
	Name               string                           `json:"name"`
	Version            string                           `json:"version,omitempty"`
	Provider           string                           `json:"provider"`
	Image              string                           `json:"image,omitempty"`
	Repos              []string                         `json:"repos,omitempty"`
	Env                []string                         `json:"env,omitempty"`
	CPU                string                           `json:"cpu,omitempty"`
	Memory             string                           `json:"memory,omitempty"`
	Status             string                           `json:"status,omitempty"`
	Capabilities       *tsContextRuntimeCapabilities    `json:"capabilities,omitempty"`
	Workspace          *tsContextRuntimeWorkspacePolicy `json:"workspace,omitempty"`
	CWD                string                           `json:"-"`
	SourcePath         string                           `json:"-"`
	WorkspaceSkillDirs []string                         `json:"-"`
}

type tsContextRuntimeCapabilities struct {
	Filesystem *tsContextRuntimeFilesystemCapabilities `json:"filesystem,omitempty"`
	Shell      *tsContextRuntimeShellCapabilities      `json:"shell,omitempty"`
	Network    *tsContextRuntimeNetworkCapabilities    `json:"network,omitempty"`
	Env        *tsContextRuntimeEnvCapabilities        `json:"env,omitempty"`
	Workspace  *tsContextRuntimeWorkspaceCapabilities  `json:"workspace,omitempty"`
	Lifecycle  *tsContextRuntimeLifecycleCapabilities  `json:"lifecycle,omitempty"`
}

type tsContextRuntimeFilesystemCapabilities struct {
	Read        *bool  `json:"read,omitempty"`
	Write       *bool  `json:"write,omitempty"`
	ArtifactURI *bool  `json:"artifact_uri,omitempty"`
	Policy      string `json:"policy,omitempty"`
	Persistence string `json:"persistence,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Retention   string `json:"retention,omitempty"`
}

type tsContextRuntimeShellCapabilities struct {
	Enabled  *bool    `json:"enabled,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Policy   string   `json:"policy,omitempty"`
}

type tsContextRuntimeNetworkCapabilities struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

type tsContextRuntimeEnvCapabilities struct {
	Forwarded []string `json:"forwarded,omitempty"`
	Policy    string   `json:"policy,omitempty"`
}

type tsContextRuntimeWorkspaceCapabilities struct {
	ProviderWorkspaceID string   `json:"provider_workspace_id,omitempty"`
	Owner               string   `json:"owner,omitempty"`
	CWD                 string   `json:"cwd,omitempty"`
	Repos               []string `json:"repos,omitempty"`
	SkillDirs           []string `json:"skill_dirs,omitempty"`
}

type tsContextRuntimeLifecycleCapabilities struct {
	Materialize    *bool  `json:"materialize,omitempty"`
	Cleanup        *bool  `json:"cleanup,omitempty"`
	Release        *bool  `json:"release,omitempty"`
	Cancellation   *bool  `json:"cancellation,omitempty"`
	DefaultTimeout string `json:"default_timeout,omitempty"`
	Policy         string `json:"policy,omitempty"`
}

type tsContextRuntimeWorkspacePolicy struct {
	ProviderWorkspaceID string                            `json:"providerWorkspaceId,omitempty"`
	Owner               string                            `json:"owner,omitempty"`
	Cleanup             *tsContextRuntimeCleanupPolicy    `json:"cleanup,omitempty"`
	Filesystem          *tsContextRuntimeFilesystemPolicy `json:"filesystem,omitempty"`
}

type tsContextRuntimeCleanupPolicy struct {
	Mode      string `json:"mode,omitempty"`
	TTL       string `json:"ttl,omitempty"`
	Retention string `json:"retention,omitempty"`
}

type tsContextRuntimeFilesystemPolicy struct {
	Persistence string `json:"persistence,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Retention   string `json:"retention,omitempty"`
}

func tsContextRuntimeProfileForDefinition(ctx context.Context, st store.Store, def *domain.WorkflowDefinition) *tsContextRuntimeProfile {
	if def == nil || def.RuntimeProfileName == "" || st == nil || st.RuntimeProfiles() == nil {
		return nil
	}
	profile, err := st.RuntimeProfiles().Get(ctx, def.WorkspaceKey, def.RuntimeProfileName)
	if err != nil || profile == nil {
		return nil
	}
	manifest := tsContextRuntimeProfileManifest(profile.Manifest)
	return &tsContextRuntimeProfile{
		Name:               profile.Name,
		Version:            profile.Version,
		Provider:           string(profile.Provider),
		Image:              profile.Image,
		Repos:              cloneStrings(profile.Repos),
		Env:                cloneStrings(profile.Env),
		CPU:                profile.CPU,
		Memory:             profile.Memory,
		Status:             string(profile.Status),
		Capabilities:       manifest.Capabilities,
		Workspace:          runtimeWorkspacePolicyFromManifest(manifest.Workspace),
		CWD:                manifest.CWD,
		SourcePath:         manifest.SourcePath,
		WorkspaceSkillDirs: compactUniqueStrings(manifest.WorkspaceSkillDirs),
	}
}

func tsContextRuntimeProfileManifest(data json.RawMessage) struct {
	CWD                string                        `json:"cwd"`
	SourcePath         string                        `json:"source_path"`
	WorkspaceSkillDirs []string                      `json:"workspace_skill_dirs"`
	Capabilities       *tsContextRuntimeCapabilities `json:"capabilities"`
	Workspace          *struct {
		ProviderWorkspaceID string                            `json:"provider_workspace_id"`
		Owner               string                            `json:"owner"`
		Cleanup             *tsContextRuntimeCleanupPolicy    `json:"cleanup"`
		Filesystem          *tsContextRuntimeFilesystemPolicy `json:"filesystem"`
	} `json:"workspace"`
} {
	var manifest struct {
		CWD                string                        `json:"cwd"`
		SourcePath         string                        `json:"source_path"`
		WorkspaceSkillDirs []string                      `json:"workspace_skill_dirs"`
		Capabilities       *tsContextRuntimeCapabilities `json:"capabilities"`
		Workspace          *struct {
			ProviderWorkspaceID string                            `json:"provider_workspace_id"`
			Owner               string                            `json:"owner"`
			Cleanup             *tsContextRuntimeCleanupPolicy    `json:"cleanup"`
			Filesystem          *tsContextRuntimeFilesystemPolicy `json:"filesystem"`
		} `json:"workspace"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &manifest)
	}
	return manifest
}

func runtimeWorkspacePolicyFromManifest(in *struct {
	ProviderWorkspaceID string                            `json:"provider_workspace_id"`
	Owner               string                            `json:"owner"`
	Cleanup             *tsContextRuntimeCleanupPolicy    `json:"cleanup"`
	Filesystem          *tsContextRuntimeFilesystemPolicy `json:"filesystem"`
}) *tsContextRuntimeWorkspacePolicy {
	if in == nil {
		return nil
	}
	out := &tsContextRuntimeWorkspacePolicy{
		ProviderWorkspaceID: in.ProviderWorkspaceID,
		Owner:               in.Owner,
		Cleanup:             emptyRuntimeCleanupPolicyAsNil(in.Cleanup),
		Filesystem:          emptyRuntimeFilesystemPolicyAsNil(in.Filesystem),
	}
	if out.ProviderWorkspaceID == "" && out.Owner == "" && out.Cleanup == nil && out.Filesystem == nil {
		return nil
	}
	return out
}

func emptyRuntimeCleanupPolicyAsNil(policy *tsContextRuntimeCleanupPolicy) *tsContextRuntimeCleanupPolicy {
	if policy == nil || (policy.Mode == "" && policy.TTL == "" && policy.Retention == "") {
		return nil
	}
	return policy
}

func emptyRuntimeFilesystemPolicyAsNil(policy *tsContextRuntimeFilesystemPolicy) *tsContextRuntimeFilesystemPolicy {
	if policy == nil || (policy.Persistence == "" && policy.Durability == "" && policy.Retention == "") {
		return nil
	}
	return policy
}

func appendRuntimeProfileReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "runtime_profile_read",
		Message:       "workflow runtime profile read from TypeScript WorkflowContext",
		Data:          mustJSON(data),
	})
}

func appendRuntimeReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, op tsWorkflowOperation) {
	switch op.Type {
	case "runtime.workspace":
		appendRuntimeWorkspaceReadEvent(ctx, st, run, op.Params)
	case "runtime.skills":
		appendRuntimeWorkspaceSkillsReadEvent(ctx, st, run, op.Params)
	default:
		appendRuntimeProfileReadEvent(ctx, st, run, op.Params)
	}
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
