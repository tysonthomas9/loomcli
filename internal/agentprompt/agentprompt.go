// Package agentprompt owns the canonical embedded prompt template sources used
// by Loom's Go agent runtimes. It deliberately has no dependency on the CLI or
// WebUI layers so both can inspect the same source without creating a cycle.
package agentprompt

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed prompts/*.md
var promptFS embed.FS

// TemplateSource returns the raw embedded template text for name. The source
// is not rendered and does not include per-worktree loom-prompts overrides.
func TemplateSource(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || path.Base(name) != name || name == "." || strings.HasSuffix(name, ".md") {
		return "", fmt.Errorf("agentprompt: invalid template name %q", name)
	}
	data, err := promptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("agentprompt: template %q not found: %w", name, err)
	}
	return string(data), nil
}
