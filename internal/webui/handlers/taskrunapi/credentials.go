package taskrunapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
)

func (m *Module) runtimeCredential(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Provider string `json:"provider"`
	}](body)
	if err != nil {
		return nil, err
	}
	provider := strings.ToLower(strings.TrimSpace(params.Provider))
	if provider == "" {
		return nil, fmt.Errorf("provider required: %w", domain.ErrInvalid)
	}
	switch provider {
	case runtimesettings.RuntimeCredentialProviderDaytona, runtimesettings.RuntimeCredentialProviderGitHub:
	default:
		return nil, fmt.Errorf("runtime credential provider %q is not supported: %w", provider, domain.ErrInvalid)
	}
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if !taskRunCanReadRuntimeCredential(run, provider) {
		return nil, fmt.Errorf("task runner %q cannot read %s runtime credentials: %w", run.Runner, provider, domain.ErrNotOwner)
	}
	if m.localSettingsDir == "" {
		return nil, fmt.Errorf("runtime credentials are unavailable: %w", domain.ErrInvalid)
	}
	settings, err := runtimesettings.Load(m.localSettingsDir)
	if err != nil {
		return nil, fmt.Errorf("load runtime credentials: %w", err)
	}
	value, err := runtimesettings.UnsealRuntimeCredential(m.localSettingsDir, settings, provider)
	if err != nil {
		return nil, fmt.Errorf("resolve %s runtime credential: %w", provider, domain.ErrInvalid)
	}
	return map[string]any{
		"provider": provider,
		"value":    value,
	}, nil
}

func taskRunCanReadRuntimeCredential(run *domain.TaskRun, provider string) bool {
	if run == nil {
		return false
	}
	if !isDaytonaTaskRunner(run) {
		return false
	}
	switch provider {
	case runtimesettings.RuntimeCredentialProviderDaytona, runtimesettings.RuntimeCredentialProviderGitHub:
		return true
	default:
		return false
	}
}

func isDaytonaTaskRunner(run *domain.TaskRun) bool {
	for _, value := range []string{
		run.Runner,
		run.RunnerEntrypoint,
		run.RuntimeMetadata["task_runner"],
		run.RuntimeMetadata["runner"],
	} {
		if strings.TrimSpace(value) == "daytona-task-runner" {
			return true
		}
	}
	return false
}
