// Package names is the single source of truth for provider-credential
// environment-variable names that loom runtime paths share.
//
// Consumers derive their provider-credential vocabulary from here so a new
// credential is added in exactly one place:
//   - internal/driver/env.go for trusted local task-runner inheritance
//   - internal/cli/envfilter for lead/interactive subprocess allowlisting
//   - the Daytona sandbox leak probe, kept a superset of this list by a Go test
//
// This package is intentionally standard-library-only so every consumer can
// import it without creating cycles.
package names

// ProviderCredentialNames is the authoritative, deduplicated list of provider
// credential env vars a trusted local agent subprocess may inherit so the
// backend CLI authenticates exactly as local tooling does.
//
// GITHUB_TOKEN_FILE is intentionally not here: it is an envfilter-local file
// path layered on top of this base, not a secret value inherited by the local
// task-runner path.
var ProviderCredentialNames = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	// claude-code's config-dir override. claudeAuthFilePath() honors it when
	// locating ~/.claude/.credentials.json for health checks, so dropping it
	// makes preflight and the spawned CLI disagree about where auth lives.
	"CLAUDE_CONFIG_DIR",
	"CODEX_API_KEY",
	"CODEX_HOME",
	"CURSOR_API_KEY",
	"GEMINI_API_KEY",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GOOGLE_API_KEY",
	"GOOGLE_APPLICATION_CREDENTIALS",
	"OPENAI_API_KEY",
}

// ProviderCredentialSet returns a fresh set built from ProviderCredentialNames.
// A new map is allocated every call so callers may store or mutate it without
// corrupting the shared source list.
func ProviderCredentialSet() map[string]struct{} {
	set := make(map[string]struct{}, len(ProviderCredentialNames))
	for _, n := range ProviderCredentialNames {
		set[n] = struct{}{}
	}
	return set
}
