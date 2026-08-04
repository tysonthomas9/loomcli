// Package data contains the `loom data ...` command subtree — thin CLI
// commands that interact with local and remote loom issue backends.
//
// Commands in this package are sdk-only: they must
// NOT import infra packages, cli root or cli sub-packages, or webui. They
// rely solely on:
//
//   - github.com/spf13/cobra — the command framework
//   - internal/backend, internal/backend/api, internal/backend/api/gen —
//     the generic backend types and the HTTP client implementation of
//     backend.IssueBackend
//   - internal/httpclient — the auth-aware HTTP client used as a transport
//     for api.APIBackend (supports OIDC device flow and token cache)
//
// This invariant is enforced by the depguard `data-isolation` rule in
// .golangci.yml. The rule's files filter matches `**/internal/cli/data/**`
// and denies every infra package plus `internal/cli`. The package uses a
// flat layout (no sub-directories) so prefix-matching deny rules do not
// collide with self-imports. See loomcli-rir82.24 for the rationale.
//
// Wiring: because cli/data cannot import internal/cli, it cannot call
// cli.RegisterCommand from an init() function like other sub-packages do.
// Instead, it exports Commands() which cmd/loom/main.go consumes and
// registers explicitly.
//
// Server selection: commands use --server or LOOM_SERVER_URL when present.
// Without a server, issue commands use an injected local backend provider.
// The --workspace flag / LOOM_WORKSPACE env var selects the target remote
// workspace. Remote commands require this selection explicitly.
package data
