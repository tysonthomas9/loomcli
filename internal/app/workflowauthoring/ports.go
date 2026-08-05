// Package workflowauthoring owns the application-level workflow-authoring
// contract. Filesystem builds and native bundle staging implement these ports
// from internal/infra/workflowdistribution.
package workflowauthoring

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type RunnerSpec struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

type BuildOptions struct {
	WorkspaceKey     string
	Name             string
	DriverID         string
	Entrypoint       string
	Files            map[string]string
	Activate         bool
	SourceRef        string
	SourceDigest     string
	WorkDir          string
	Runners          []RunnerSpec
	Manifest         map[string]string
	DeriveRunners    bool
	Trust            workflowcatalog.DriverTrustLevel
	RequestID        string
	ExpectedRevision uint64
}

type Bundle struct {
	Root         string            `json:"root,omitempty"`
	BundleRef    string            `json:"bundle_ref"`
	SourceRef    string            `json:"source_ref"`
	SourceDigest string            `json:"source_digest"`
	BundleDigest string            `json:"bundle_digest"`
	Manifest     map[string]string `json:"manifest"`
	Diagnostics  string            `json:"diagnostics,omitempty"`
}

type StagedMetadata struct {
	DriverName      string
	DriverID        string
	VersionID       string
	SourceRef       string
	SourceDigest    string
	BundleRef       string
	BundleDigest    string
	Runtime         string
	CatalogManifest map[string]string
	Diagnostics     string
}

type StagedBundle interface {
	Metadata() StagedMetadata
	Bundle() *Bundle
	Promote() error
	Cleanup()
}

// BundleStager is the outbound infrastructure port. Implementations may build
// source, extract archives, and write content-addressed bundle trees, but they
// return only owned metadata to the application coordinator.
type BundleStager interface {
	BuildAndStage(context.Context, BuildOptions) (StagedBundle, string, error)
}

type NativeOptions struct {
	WorkspaceKey string
	WorkDir      string
	DistPath     string
	ManifestPath string
	DriverName   string
	DriverID     string
	WorkflowName string
	SourceRef    string
	SourceDigest string
	Activate     bool
	Runners      []RunnerSpec
	Manifest     map[string]string
	Trust        workflowcatalog.DriverTrustLevel
}

type NativeBundleStager interface {
	StageNative(context.Context, NativeOptions) (StagedBundle, error)
}

// ManagedBuiltinAuthorityProvider is the exact-purpose authority seam used by
// the startup and dispatch-time self-healing paths. Implementations issue only
// ActionAuthorManagedVersion for the requested canonical workspace.
type ManagedBuiltinAuthorityProvider interface {
	AuthorityForManagedBuiltin(context.Context, string, string) (authority.SystemAuthority, error)
}

type BuiltinSpec struct {
	Entrypoint string
	Files      map[string]string
	Runners    []RunnerSpec
}

type BuiltinVersionAssessment struct {
	BundleAvailable bool
	RunnerListStale bool
	MissingRunners  []string
}

// BuiltinSupport is the outbound distribution/read-model port. Embedded
// source lookup, filesystem bundle checks, runner-manifest decoding, and
// process working-directory resolution stay behind this infrastructure seam.
type BuiltinSupport interface {
	BuiltinNames() []string
	Builtin(string) (BuiltinSpec, bool)
	SourceDigest(map[string]string) (string, error)
	AssessVersion(*workflowcatalog.DriverVersion, []RunnerSpec) BuiltinVersionAssessment
	DeclaredRunner(*workflowcatalog.DriverVersion, string) (RunnerSpec, error)
	WorkDir() string
}

// BoundPromptAgentIndex exposes only the two startup discovery reads needed by
// the application coordinator. It deliberately does not expose a composite
// store or legacy workspace/trigger-binding models.
type BoundPromptAgentIndex interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
	HasEnabledPromptAgentBinding(context.Context, string) (bool, error)
}

type GlobalRunnerResolution struct {
	Driver  *workflowcatalog.Driver
	Version *workflowcatalog.DriverVersion
	Spec    RunnerSpec
}

type Result struct {
	Driver         *workflowcatalog.Driver        `json:"driver"`
	Version        *workflowcatalog.DriverVersion `json:"version"`
	Bundle         *Bundle                        `json:"bundle,omitempty"`
	CreatedDriver  bool                           `json:"created_driver"`
	CreatedVersion bool                           `json:"created_version"`
	ReusedVersion  bool                           `json:"reused_version"`
	Activated      bool                           `json:"activated"`
}
