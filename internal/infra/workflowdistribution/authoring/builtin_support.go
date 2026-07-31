package authoring

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	workflowdistribution "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type legacyBuiltinSupport struct{}

func NewBuiltinSupport() appworkflowauthoring.BuiltinSupport {
	return legacyBuiltinSupport{}
}

func (legacyBuiltinSupport) BuiltinNames() []string {
	return workflowdistribution.BuiltinWorkflowNames()
}

func (legacyBuiltinSupport) Builtin(name string) (appworkflowauthoring.BuiltinSpec, bool) {
	spec, ok := workflowdistribution.BuiltinWorkflow(name)
	if !ok {
		return appworkflowauthoring.BuiltinSpec{}, false
	}
	return appworkflowauthoring.BuiltinSpec{
		Entrypoint: spec.Entrypoint,
		Files:      cloneWorkflowManifest(spec.Files),
		Runners: applicationRunnerSpecs(
			workflowdistribution.DeriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files),
		),
	}, true
}

func (legacyBuiltinSupport) SourceDigest(files map[string]string) (string, error) {
	return workflowdistribution.SourceDigest(files)
}

func (legacyBuiltinSupport) AssessVersion(
	version *workflowcatalog.DriverVersion,
	fresh []appworkflowauthoring.RunnerSpec,
) appworkflowauthoring.BuiltinVersionAssessment {
	freshNames := make(map[string]struct{}, len(fresh))
	for _, runner := range fresh {
		if name := strings.TrimSpace(runner.Name); name != "" {
			freshNames[name] = struct{}{}
		}
	}
	return appworkflowauthoring.BuiltinVersionAssessment{
		BundleAvailable: builtInWorkflowBundleAvailable(version),
		RunnerListStale: workflowdistribution.ActiveManifestRunnersAreStale(
			versionManifest(version),
			freshNames,
		),
		MissingRunners: workflowdistribution.ManifestMissingFreshRunners(
			versionManifest(version),
			freshNames,
		),
	}
}

func (legacyBuiltinSupport) DeclaredRunner(
	version *workflowcatalog.DriverVersion,
	runnerName string,
) (appworkflowauthoring.RunnerSpec, error) {
	spec, err := driver.DeclaredRunnerSpec(version, runnerName)
	if err != nil {
		return appworkflowauthoring.RunnerSpec{}, err
	}
	return appworkflowauthoring.RunnerSpec{
		Name: spec.Name, Kind: spec.Kind, Entrypoint: spec.Entrypoint,
	}, nil
}

func (legacyBuiltinSupport) WorkDir() string {
	return builtinWorkflowWorkDir()
}

func versionManifest(version *workflowcatalog.DriverVersion) map[string]string {
	if version == nil {
		return nil
	}
	return version.Manifest
}

func builtInWorkflowBundleAvailable(version *workflowcatalog.DriverVersion) bool {
	if version == nil || strings.TrimSpace(version.BundleRef) == "" ||
		filepath.IsAbs(version.BundleRef) {
		return false
	}
	workDir := builtinWorkflowWorkDir()
	root := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(version.BundleRef)))
	rel, err := filepath.Rel(workDir, root)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, relFile := range []string{"manifest.json", filepath.Join("dist", "server.mjs")} {
		info, err := os.Stat(filepath.Join(root, relFile))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func builtinWorkflowWorkDir() string {
	if dir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")); dir != "" {
		return dir
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workDir
}

type legacyBoundPromptAgentIndex struct {
	index BoundPromptAgentIndex
}

func NewBoundPromptAgentIndex(
	index BoundPromptAgentIndex,
) appworkflowauthoring.BoundPromptAgentIndex {
	if index == nil {
		return nil
	}
	return legacyBoundPromptAgentIndex{index: index}
}

func (adapter legacyBoundPromptAgentIndex) ListWorkspaceKeys(
	ctx context.Context,
) ([]string, error) {
	workspaces, err := adapter.index.Workspaces().List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace != nil {
			keys = append(keys, workspace.Key)
		}
	}
	return keys, nil
}

func (adapter legacyBoundPromptAgentIndex) HasEnabledPromptAgentBinding(
	ctx context.Context,
	workspace string,
) (bool, error) {
	enabled := true
	bindings, err := adapter.index.TriggerBindings().List(
		ctx,
		workspace,
		store.TriggerBindingFilter{
			DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
			Enabled:  &enabled,
			Limit:    1,
		},
	)
	return len(bindings) > 0, err
}
