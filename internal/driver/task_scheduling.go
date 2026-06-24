package driver

import (
	"context"
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
	targetNodeID := strings.TrimSpace(opts.NodeID)
	for _, node := range nodes {
		if targetNodeID != "" && (node == nil || node.NodeID != targetNodeID) {
			continue
		}
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
	provider := strings.TrimSpace(opts.SandboxPlacement.Provider)
	if provider == "" {
		provider = strings.TrimSpace(opts.ProviderProfile)
	}
	providers = append(providers, provider)
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
