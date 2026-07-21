//go:build loom_packaged_builtins

package workflows

import "testing"

func TestEmbeddedPackagedPromptAgentMatchesSource(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinPromptAgentWorkflowName)
	if !ok {
		t.Fatal("prompt-agent builtin missing")
	}
	const distPath = "builtin-dist/prompt-agent/dist"
	matches, err := packagedBuiltinDigestMatches(packagedBuiltinFS, distPath, SourceDigest(spec.Files))
	if err != nil {
		t.Fatalf("read embedded prompt-agent digest: %v", err)
	}
	if !matches {
		t.Fatal("embedded prompt-agent bundle is missing or stale; rebuild it before packaging")
	}
}
