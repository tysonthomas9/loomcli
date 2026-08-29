// Package skillpaths identifies Loom-managed skill materializations that the
// file browser must hide. Reads by an explicit path remain allowed; this
// policy applies only to directory listings, indexes, and searches.
package skillpaths

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/skillmat"
)

const policyVersion = "skill-materializations-v2"

// Policy hides skill materializations beneath configured checkout roots.
// Roots and candidate paths are relative to the file operation's scope root.
type Policy struct {
	checkoutRoots []string
	identity      string
}

// NewPolicy constructs a policy for the configured checkout roots within one
// file-browser scope. Use an empty checkout root when the scope root is itself
// a checkout (repo and agent scopes).
func NewPolicy(checkoutRoots ...string) Policy {
	roots := make([]string, 0, len(checkoutRoots))
	seen := make(map[string]struct{}, len(checkoutRoots))
	for _, root := range checkoutRoots {
		normalized, ok := normalizeRelative(root)
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		roots = append(roots, normalized)
	}
	sort.Strings(roots)
	return Policy{
		checkoutRoots: roots,
		identity:      policyVersion + "\x1f" + strings.Join(roots, "\x1e"),
	}
}

// Identity returns a stable discriminator for caches and in-flight builds.
// It changes when the policy version or configured checkout topology changes.
func (p Policy) Identity() string { return p.identity }

// Hidden reports whether rel is at or below .agents/skills or .claude/skills
// in one of the configured checkouts.
func (p Policy) Hidden(rel string) bool {
	normalized, ok := normalizeRelative(rel)
	if !ok || normalized == "" {
		return false
	}
	for _, root := range p.checkoutRoots {
		for _, materializedDir := range []string{skillmat.AgentsSkillsDir, skillmat.ClaudeSkillsDir} {
			if isAtOrBelowManagedDir(root, normalized, filepath.ToSlash(materializedDir)) {
				return true
			}
		}
	}
	return false
}

func isAtOrBelowManagedDir(root, candidate, managedDir string) bool {
	rootSegments := splitPath(root)
	candidateSegments := splitPath(candidate)
	managedSegments := splitPath(managedDir)
	if len(candidateSegments) < len(rootSegments)+len(managedSegments) {
		return false
	}
	for i, segment := range rootSegments {
		if candidateSegments[i] != segment {
			return false
		}
	}
	for i, segment := range managedSegments {
		if !strings.EqualFold(candidateSegments[len(rootSegments)+i], segment) {
			return false
		}
	}
	return true
}

func splitPath(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func normalizeRelative(value string) (string, bool) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" || value == "." {
		return "", true
	}
	if strings.HasPrefix(value, "/") {
		return "", false
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return strings.TrimPrefix(clean, "./"), true
}
