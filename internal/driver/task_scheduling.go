package driver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Schedulability preflight for taskRuns.request: fail fast as unschedulable
// when no live node advertises the provider/capabilities the request needs,
// instead of queueing work that can never be claimed.
type taskRunSchedulingRequirements struct {
	Providers    []string
	Capabilities []string
}

func verifyTaskRunRequestSchedulable(ctx context.Context, s store.Store, opts TaskRunRequestOptions) error {
	profile, err := taskRunRequestSchedulingProfile(ctx, s, opts)
	if err != nil {
		return err
	}
	requirements := taskRunRequestSchedulingRequirements(opts, profile)
	nodes, err := s.Nodes().List(ctx, opts.WorkspaceKey)
	if err != nil {
		return fmt.Errorf("list nodes for task run scheduling: %w", err)
	}
	now := time.Now().UTC()
	for _, node := range nodes {
		if taskRunNodeSatisfiesScheduling(node, requirements, now) {
			return nil
		}
	}
	taskRunID := firstNonEmpty(opts.TaskRunID, opts.TaskID)
	return fmt.Errorf(
		"task run %q is unschedulable: no live active node advertises providers %s and capabilities %s: %w",
		taskRunID,
		formatSchedulingRequirementList(requirements.Providers),
		formatSchedulingRequirementList(requirements.Capabilities),
		domain.ErrUnschedulable,
	)
}

func taskRunRequestSchedulingProfile(ctx context.Context, s store.Store, opts TaskRunRequestOptions) (*domain.WorkerProfile, error) {
	profileID := strings.TrimSpace(opts.WorkerProfileID)
	if profileID == "" {
		return nil, nil
	}
	profile, err := s.WorkerProfiles().Get(ctx, opts.WorkspaceKey, profileID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("worker profile %q is not schedulable: %w", profileID, domain.ErrUnschedulable)
		}
		return nil, fmt.Errorf("get worker profile %q for scheduling: %w", profileID, err)
	}
	if !profile.Enabled {
		return nil, fmt.Errorf("worker profile %q is disabled: %w", profileID, domain.ErrUnschedulable)
	}
	return profile, nil
}

func taskRunRequestSchedulingRequirements(opts TaskRunRequestOptions, profile *domain.WorkerProfile) taskRunSchedulingRequirements {
	var providers []string
	if !taskRunHasNamedRunner(opts) {
		provider := strings.TrimSpace(opts.SandboxPlacement.Provider)
		if provider == "" {
			provider = strings.TrimSpace(opts.ProviderProfile)
		}
		providers = append(providers, provider)
	}
	if profile != nil {
		providers = append(providers, profile.Backend)
	}

	capabilities := append([]string(nil), opts.Capabilities...)
	if profile != nil {
		capabilities = append(capabilities, profile.Capabilities...)
	}
	return taskRunSchedulingRequirements{
		Providers:    normalizeStringList(providers),
		Capabilities: normalizeStringList(capabilities),
	}
}

func taskRunNodeSatisfiesScheduling(node *domain.Node, requirements taskRunSchedulingRequirements, now time.Time) bool {
	if node == nil {
		return false
	}
	if node.DrainState != domain.NodeDrainActive {
		return false
	}
	if !node.ExpiresAt.IsZero() && !node.ExpiresAt.After(now) {
		return false
	}
	for _, provider := range requirements.Providers {
		if !nodeAdvertisesProvider(node, provider) {
			return false
		}
	}
	return stringListContainsAll(node.Capabilities, requirements.Capabilities)
}

func nodeAdvertisesProvider(node *domain.Node, provider string) bool {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return true
	}
	if string(node.RuntimeProvider) == provider {
		return true
	}
	return stringListEmptyOrContains(node.Capabilities, provider)
}

func stringListEmptyOrContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func stringListContainsAll(have, required []string) bool {
	for _, want := range required {
		if !stringListEmptyOrContains(have, want) {
			return false
		}
	}
	return true
}

func formatSchedulingRequirementList(values []string) string {
	if len(values) == 0 {
		return "<any>"
	}
	return strings.Join(values, ",")
}

func normalizeArtifactIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func taskRunSupportedProviders(opts TaskRunRequestOptions) []string {
	values := append([]string(nil), opts.SupportedProviders...)
	if !taskRunHasNamedRunner(opts) {
		values = append(values, opts.SandboxPlacement.Provider)
	}
	return normalizeStringList(values)
}

func taskRunWorkerSupportedProviders(opts TaskRunWorkerOptions) []string {
	values := append([]string(nil), opts.SupportedProviders...)
	values = append(values, opts.SandboxPlacement.Provider)
	return normalizeStringList(values)
}

func taskRunHasNamedRunner(opts TaskRunRequestOptions) bool {
	return strings.TrimSpace(opts.Runner) != "" ||
		strings.TrimSpace(opts.RunnerKind) != "" ||
		strings.TrimSpace(opts.RunnerEntrypoint) != "" ||
		strings.TrimSpace(opts.RunnerRef) != ""
}

func resolveTaskProviderProfile(opts TaskRunRequestOptions, hostBridgeAvailable bool) (TaskRunRequestOptions, error) {
	profile := strings.TrimSpace(opts.ProviderProfile)
	opts.ProviderProfile = profile
	switch profile {
	case "local-noop", "noop":
		opts.ProviderProfile = "local-noop"
		if opts.SandboxPlacement.Provider == "" {
			opts.SandboxPlacement.Provider = "local-noop"
		}
		opts.SupportedProviders = append(opts.SupportedProviders, "local-noop", "noop")
		return opts, nil
	case "flue-daytona":
		if !hostBridgeAvailable {
			return opts, fmt.Errorf("provider profile %q requires a configured task runner command: %w", profile, domain.ErrInvalid)
		}
		if opts.RunnerPlacement.Provider == "" {
			opts.RunnerPlacement.Provider = "flue"
		}
		if opts.SandboxPlacement.Provider == "" {
			opts.SandboxPlacement.Provider = "daytona"
		}
		opts.SupportedProviders = append(opts.SupportedProviders, "daytona")
		return opts, nil
	case "":
		return opts, fmt.Errorf("provider profile required: %w", domain.ErrInvalid)
	default:
		if !hostBridgeAvailable {
			return opts, fmt.Errorf("provider profile %q is not supported by local exec-task: %w", profile, domain.ErrInvalid)
		}
		providers := normalizeStringList(opts.SupportedProviders)
		if opts.SandboxPlacement.Provider == "" {
			switch len(providers) {
			case 0:
				return opts, fmt.Errorf("provider profile %q requires --sandbox-provider or --supported-provider: %w", profile, domain.ErrInvalid)
			case 1:
				opts.SandboxPlacement.Provider = providers[0]
			default:
				return opts, fmt.Errorf("provider profile %q has multiple supported providers; --sandbox-provider is required: %w", profile, domain.ErrInvalid)
			}
		}
		return opts, nil
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func generatedTaskRunID(driverRunID, taskID string) string {
	runPart := slug(driverRunID)
	if runPart == "" {
		runPart = "run"
	}
	taskPart := slug(taskID)
	if taskPart == "" {
		taskPart = "task"
	}
	return fmt.Sprintf("task-run-%s-%s-%d", runPart, taskPart, time.Now().UTC().UnixNano())
}

func generatedTaskRunLeaseToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("task-run-token-%d", time.Now().UTC().UnixNano())
}

func generatedTaskRunLeaseID(nodeID string) string {
	nodePart := slug(nodeID)
	if nodePart == "" {
		nodePart = "worker"
	}
	return fmt.Sprintf("task-run-lease-%s-%d", nodePart, time.Now().UTC().UnixNano())
}
