package taskrunapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const (
	maxDaytonaRepositoryURLBytes = 2 << 10
	maxDaytonaTaskPromptBytes    = 512 << 10
	maxDaytonaGitRefBytes        = 512
	maxDaytonaModeBytes          = 64
	maxDaytonaBackendBytes       = 64
	maxDaytonaModelBytes         = 256
)

func (m *Module) daytonaExecute(
	ctx context.Context,
	workspace string,
	identity leaseIdentity,
	body []byte,
) (any, error) {
	intent, err := decodeStrictParams[execution.DaytonaProviderIntent](body)
	if err != nil {
		return nil, err
	}
	if err := validateDaytonaProviderIntent(intent); err != nil {
		return nil, err
	}
	run, err := m.verifyLease(ctx, workspace, identity)
	if err != nil {
		return nil, err
	}
	if !isDaytonaTaskRun(run) {
		return nil, fmt.Errorf("daytona provider execution is restricted to %s: %w", driver.DaytonaTaskRunnerEntrypoint, persistence.ErrNotOwner)
	}
	if m.daytonaProvider == nil {
		return nil, fmt.Errorf("daytona provider broker is not configured: %w", execution.ErrUnavailable)
	}
	result, err := m.daytonaProvider.ExecuteDaytona(ctx, execution.DaytonaProviderCommand{
		WorkspaceKey: workspace,
		TaskRunID:    run.TaskRunID,
		WorkItemID:   run.WorkItemID,
		DriverRunID:  run.DriverRunID,
		Intent:       intent,
	})
	if err != nil {
		return nil, err
	}
	if err := validateDaytonaProviderResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

func isDaytonaTaskRun(run *execution.TaskRun) bool {
	if run == nil {
		return false
	}
	runner := strings.TrimSpace(run.RunnerEntrypoint)
	if runner == "" {
		runner = strings.TrimSpace(run.Runner)
	}
	return runner == driver.DaytonaTaskRunnerEntrypoint
}

func validateDaytonaProviderIntent(intent execution.DaytonaProviderIntent) error {
	if intent.SchemaVersion != execution.DaytonaProviderSchemaV1 {
		return fmt.Errorf("schemaVersion must be %q: %w", execution.DaytonaProviderSchemaV1, persistence.ErrInvalid)
	}
	if err := validateBoundedUTF8("repositoryUrl", intent.RepositoryURL, 1, maxDaytonaRepositoryURLBytes); err != nil {
		return err
	}
	if err := validateDaytonaRepositoryURL(intent.RepositoryURL); err != nil {
		return err
	}
	if err := validateBoundedUTF8("taskPrompt", intent.TaskPrompt, 1, maxDaytonaTaskPromptBytes); err != nil {
		return err
	}
	if err := validateBoundedUTF8("backend", intent.Backend, 1, maxDaytonaBackendBytes); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(intent.Backend), "codex") {
		return fmt.Errorf("backend must be %q: %w", "codex", persistence.ErrInvalid)
	}
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{name: "baseRef", value: intent.BaseRef, max: maxDaytonaGitRefBytes},
		{name: "model", value: intent.Model, max: maxDaytonaModelBytes},
		{name: "mode", value: intent.Mode, max: maxDaytonaModeBytes},
		{name: "delivery.baseBranch", value: intent.Delivery.BaseBranch, max: maxDaytonaGitRefBytes},
		{name: "delivery.outputBranch", value: intent.Delivery.OutputBranch, max: maxDaytonaGitRefBytes},
	} {
		if err := validateBoundedUTF8(field.name, field.value, 0, field.max); err != nil {
			return err
		}
	}
	if intent.Delivery.OpenPullRequest {
		if strings.TrimSpace(intent.Delivery.BaseBranch) == "" || strings.TrimSpace(intent.Delivery.OutputBranch) == "" {
			return fmt.Errorf("delivery baseBranch and outputBranch are required for pull-request delivery: %w", persistence.ErrInvalid)
		}
	}
	return nil
}

func validateDaytonaRepositoryURL(value string) error {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "git@github.com:") {
		repo := strings.TrimSuffix(strings.TrimPrefix(value, "git@github.com:"), ".git")
		parts := strings.Split(repo, "/")
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" &&
			!strings.ContainsAny(repo, "@?#\\\r\n\t ") {
			return nil
		}
		return fmt.Errorf("repositoryUrl must be a credential-free HTTPS or git@github.com repository URL: %w", persistence.ErrInvalid)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(value, "\r\n\t") {
		return fmt.Errorf("repositoryUrl must be a credential-free HTTPS or git@github.com repository URL: %w", persistence.ErrInvalid)
	}
	return nil
}

func validateBoundedUTF8(name, value string, minBytes, maxBytes int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8: %w", name, persistence.ErrInvalid)
	}
	size := len(value)
	if size < minBytes || size > maxBytes {
		return fmt.Errorf("%s must be between %d and %d bytes: %w", name, minBytes, maxBytes, persistence.ErrInvalid)
	}
	return nil
}

func validateDaytonaProviderResult(result execution.DaytonaProviderResult) error {
	if result.SchemaVersion != execution.DaytonaProviderSchemaV1 {
		return fmt.Errorf("daytona provider returned unsupported schema version: %w", execution.ErrUnavailable)
	}
	switch result.Status {
	case "completed":
		if result.ExitCode != 0 {
			return fmt.Errorf("daytona provider returned completed with a non-zero exit code: %w", execution.ErrUnavailable)
		}
		if strings.TrimSpace(result.Sandbox.ID) == "" ||
			strings.TrimSpace(result.Sandbox.CWD) == "" ||
			strings.TrimSpace(result.Sandbox.RepoRef) == "" {
			return fmt.Errorf("daytona provider returned incomplete materialization evidence: %w", execution.ErrUnavailable)
		}
	case "failed", "cancelled":
	default:
		return fmt.Errorf("daytona provider returned non-terminal status %q: %w", result.Status, execution.ErrUnavailable)
	}
	if result.Sandbox.Provider != "daytona" {
		return fmt.Errorf("daytona provider returned invalid sandbox receipt: %w", execution.ErrUnavailable)
	}
	return nil
}
