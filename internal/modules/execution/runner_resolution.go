package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const openShellRunnerName = "openshell-task-runner"

type TrustedRunnerResolver interface {
	ResolveTrustedRunner(context.Context, authority.SystemAuthority, ResolveTrustedRunnerCommand) (*TrustedRunner, error)
}

type ResolveTrustedRunnerCommand struct {
	WorkspaceKey string
	RunnerName   string
}

type RunnerSpec struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

type TrustedRunner struct {
	WorkspaceKey string
	DriverID     string
	VersionID    string
	Spec         RunnerSpec
}

// RunnerCatalogCandidate is the catalog adapter's projection of one active
// version that may declare a requested runner.
type RunnerCatalogCandidate struct {
	WorkspaceKey   string
	DriverID       string
	VersionID      string
	ManagedBuiltin bool
	Trusted        bool
	Manifest       map[string]string
}

// RunnerCatalog is a read-only projection port. Managed builtin preparation
// must complete before this query runs.
type RunnerCatalog interface {
	ActiveBuiltinCandidates(context.Context, string, string) ([]RunnerCatalogCandidate, error)
}

type RunnerResolutionService struct {
	catalog   RunnerCatalog
	admission *authority.Admission
}

var _ TrustedRunnerResolver = (*RunnerResolutionService)(nil)

func NewRunnerResolutionService(
	catalog RunnerCatalog,
	admission *authority.Admission,
) (*RunnerResolutionService, error) {
	if catalog == nil || admission == nil {
		return nil, ErrUnavailable
	}
	return &RunnerResolutionService{catalog: catalog, admission: admission}, nil
}

func (service *RunnerResolutionService) ResolveTrustedRunner(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ResolveTrustedRunnerCommand,
) (*TrustedRunner, error) {
	if !canonicalRunnerResolutionCommand(command) {
		return nil, ErrInvalid
	}
	if service == nil || service.catalog == nil || service.admission == nil {
		return nil, ErrUnavailable
	}
	if err := service.admission.RequireSystem(ActionResolveTrustedRunner, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if command.RunnerName == openShellRunnerName {
		return nil, ErrNotFound
	}
	candidates, err := service.catalog.ActiveBuiltinCandidates(ctx, command.WorkspaceKey, command.RunnerName)
	if err != nil {
		return nil, fmt.Errorf("resolve active builtin runner candidates: %w", err)
	}
	candidates = append([]RunnerCatalogCandidate(nil), candidates...)
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].DriverID != candidates[right].DriverID {
			return candidates[left].DriverID < candidates[right].DriverID
		}
		return candidates[left].VersionID < candidates[right].VersionID
	})
	for _, candidate := range candidates {
		if resolved, ok := resolveRunnerCandidate(command.WorkspaceKey, command.RunnerName, candidate); ok {
			return resolved, nil
		}
	}
	return nil, ErrNotFound
}

func canonicalRunnerResolutionCommand(command ResolveTrustedRunnerCommand) bool {
	workspace := strings.TrimSpace(command.WorkspaceKey)
	runnerName := strings.TrimSpace(command.RunnerName)
	return workspace != "" && runnerName != "" &&
		workspace == command.WorkspaceKey && runnerName == command.RunnerName
}

func resolveRunnerCandidate(
	workspace string,
	runnerName string,
	candidate RunnerCatalogCandidate,
) (*TrustedRunner, bool) {
	if candidate.WorkspaceKey != workspace ||
		strings.TrimSpace(candidate.DriverID) == "" || candidate.DriverID != strings.TrimSpace(candidate.DriverID) ||
		strings.TrimSpace(candidate.VersionID) == "" || candidate.VersionID != strings.TrimSpace(candidate.VersionID) ||
		!candidate.ManagedBuiltin || !candidate.Trusted {
		return nil, false
	}
	raw := strings.TrimSpace(candidate.Manifest[runnerManifestKey])
	if raw == "" {
		return nil, false
	}
	var specs []RunnerSpec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, false
	}
	for _, spec := range specs {
		if spec.Name != runnerName || !validRunnerSpec(spec) {
			continue
		}
		return &TrustedRunner{
			WorkspaceKey: workspace,
			DriverID:     candidate.DriverID,
			VersionID:    candidate.VersionID,
			Spec:         spec,
		}, true
	}
	return nil, false
}

func validRunnerSpec(spec RunnerSpec) bool {
	if spec.Name == "" || spec.Name != strings.TrimSpace(spec.Name) ||
		spec.Kind == "" || spec.Kind != strings.TrimSpace(spec.Kind) ||
		spec.Entrypoint == "" || spec.Entrypoint != strings.TrimSpace(spec.Entrypoint) {
		return false
	}
	switch spec.Kind {
	case RunnerKindFlueWorkflow, RunnerKindNodeModule:
	default:
		return false
	}
	if path.IsAbs(spec.Entrypoint) || strings.Contains(spec.Entrypoint, `\`) {
		return false
	}
	clean := path.Clean(spec.Entrypoint)
	return clean != "." && clean == spec.Entrypoint && clean != ".." && !strings.HasPrefix(clean, "../")
}
