package workflowcatalog

import (
	"path"
	"strings"
)

// Builtin workflow identities are Catalog-owned names. Their embedded source,
// build toolchain, and local bundle materialization live in the distribution
// adapter; consumers that need only durable identity must not import that
// adapter.
const (
	BuiltinEpicRunnerWorkflowName        = "epic-runner"
	BuiltinGitHubReviewAgentWorkflowName = "github-review-agent"
	BuiltinGitHubReviewTaskRunnerName    = "github-review-task-runner"
	BuiltinBugFixAgentWorkflowName       = "bug-fix-agent"
	BuiltinReviewLoopAgentWorkflowName   = "review-loop-agent"
	BuiltinLocalReviewAgentWorkflowName  = "local-review-agent"
	BuiltinPromptAgentWorkflowName       = "prompt-agent"
	ManagedBuiltinProvenance             = "loom-managed-builtin"
)

// builtinWorkflowRegistry is the one canonical, sorted catalog of reserved
// managed identities. Distribution coverage is tested against this list so a
// new constant cannot silently become authorable without embedded source (or
// vice versa).
var builtinWorkflowRegistry = [...]string{
	BuiltinBugFixAgentWorkflowName,
	BuiltinEpicRunnerWorkflowName,
	BuiltinGitHubReviewAgentWorkflowName,
	BuiltinLocalReviewAgentWorkflowName,
	BuiltinPromptAgentWorkflowName,
	BuiltinReviewLoopAgentWorkflowName,
}

// BuiltinWorkflowNames returns a defensive copy of the canonical sorted
// managed-builtin identity registry.
func BuiltinWorkflowNames() []string {
	return append([]string(nil), builtinWorkflowRegistry[:]...)
}

// IsBuiltinWorkflowName reports whether name is a catalog-reserved embedded
// workflow identity. It performs an exact lookup; inbound adapters normalize
// their own transport values before calling it.
func IsBuiltinWorkflowName(name string) bool {
	for _, candidate := range builtinWorkflowRegistry {
		if name == candidate {
			return true
		}
	}
	return false
}

// BuiltinSourceRef returns the exact source provenance admitted by managed
// authoring for one embedded source digest.
func BuiltinSourceRef(name, sourceDigest string) string {
	return "builtin://workflows/" + name + "/versions/" + sourceDigest
}

// BuiltinVersionID returns the content-derived immutable version identity used
// by native Flue staging.
func BuiltinVersionID(name, bundleDigest string) string {
	short := strings.TrimPrefix(bundleDigest, "sha256:")
	if len(short) > 12 {
		short = short[:12]
	}
	return name + "-v-" + short
}

// BuiltinBundleRef returns the only local bundle reference admitted for a
// managed builtin version.
func BuiltinBundleRef(name, versionID string) string {
	return path.Join(".loom", "drivers", name, versionID)
}
