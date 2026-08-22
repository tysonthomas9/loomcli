package workflows

import (
	_ "embed"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

//go:embed FLUE_COMMIT
var pinnedFlueCommitRaw string

//go:embed NODE_VERSION
var pinnedNodeVersionRaw string

// PinnedFlueCommit is the Flue commit the built-in workflows are built and
// verified against (internal/workflows/FLUE_COMMIT).
var PinnedFlueCommit = strings.TrimSpace(pinnedFlueCommitRaw)

// PinnedNodeVersion is the Node release the packaged built-in artifacts run
// on (internal/workflows/NODE_VERSION; floor 22.18 per the Flue CLI engines).
var PinnedNodeVersion = strings.TrimSpace(pinnedNodeVersionRaw)

// BuiltinArtifactExpectation is what this binary expects a packaged artifact
// for the named built-in to carry: the embedded source digest and the runner
// set derived from the embedded source tree. ok is false for unknown names.
func BuiltinArtifactExpectation(name string) (sourceDigest string, runners []driver.DriverRunnerSpec, ok bool) {
	spec, found := BuiltinWorkflow(name)
	if !found {
		return "", nil, false
	}
	return SourceDigest(spec.Files), deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files), true
}

// packagedProvenance is the manifest overlay stamped on a registration from
// the packaged lane. provenance overrides nativeFlueManifest's
// operator_registered default.
func packagedProvenance(art *packaged.Artifact) map[string]string {
	return map[string]string{
		"provenance":            packaged.ProvenancePackagedBuiltin,
		"flue_commit":           art.FlueCommit,
		"node_version":          art.NodeVersion,
		"packaged_index_digest": art.IndexDigest,
		"packaged_target":       art.Target,
	}
}
