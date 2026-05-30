package workflow

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type tsContextRuntimeProfile struct {
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Provider string   `json:"provider"`
	Image    string   `json:"image,omitempty"`
	Repos    []string `json:"repos,omitempty"`
	Env      []string `json:"env,omitempty"`
	CPU      string   `json:"cpu,omitempty"`
	Memory   string   `json:"memory,omitempty"`
	Status   string   `json:"status,omitempty"`
}

func tsContextRuntimeProfileForDefinition(ctx context.Context, st store.Store, def *domain.WorkflowDefinition) *tsContextRuntimeProfile {
	if def == nil || def.RuntimeProfileName == "" || st == nil || st.RuntimeProfiles() == nil {
		return nil
	}
	profile, err := st.RuntimeProfiles().Get(ctx, def.WorkspaceKey, def.RuntimeProfileName)
	if err != nil || profile == nil {
		return nil
	}
	return &tsContextRuntimeProfile{
		Name:     profile.Name,
		Version:  profile.Version,
		Provider: string(profile.Provider),
		Image:    profile.Image,
		Repos:    cloneStrings(profile.Repos),
		Env:      cloneStrings(profile.Env),
		CPU:      profile.CPU,
		Memory:   profile.Memory,
		Status:   string(profile.Status),
	}
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
	if op.Type == "runtime.workspace" {
		appendRuntimeWorkspaceReadEvent(ctx, st, run, op.Params)
		return
	}
	appendRuntimeProfileReadEvent(ctx, st, run, op.Params)
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
