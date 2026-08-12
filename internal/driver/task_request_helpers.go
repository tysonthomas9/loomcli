package driver

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func normalizeArtifactIDs(ids []string) []string {
	out := normalizeStringList(ids)
	if len(out) == 0 {
		return nil
	}
	return out
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

func generatedTaskRunLeaseToken() string {
	var value [24]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
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
