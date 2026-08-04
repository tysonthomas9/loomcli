# LOOM environment variables

Inventory of every `LOOM_*` environment variable the Go source reads, derived
from `os.Getenv` / `os.LookupEnv` / `os.Setenv` call sites and from `LOOM_*`
string constants, with the variable name resolved through `go/types` so a read
by a named constant (`os.Getenv(bootstrap.EnvFleetDBURL)`) is counted the same
as a read by a string literal (`os.Getenv("LOOM_FLEET_DB_URL")`). The table
below is generated; this preamble is the hand-maintained rationale.

**What is counted.** A variable appears when its name is the resolved argument
of an `os` read/set call, or when it is declared as a `LOOM_*` string constant
(the "indirect" rows: names read only through a wrapper such as `boundedIntEnv`
or `baseURLOverride`, where the constant is passed in as a parameter).

**What is deliberately excluded.** Names that are only *written into a child
process's* environment via `fmt.Sprintf("LOOM_X=%s", …)` (see
`internal/cli/daemon/supervisor/spawn.go`) are not reads and do not appear
unless some process also reads them back. Names that occur only as entries in an
allow/deny filter list (`internal/driver/env.go`, `internal/cli/envfilter`) or a
redaction list (`internal/stackpublish/scrub.go`) are policy data, not reads.
Reads in `_test.go` files are out of scope.

**Known blind spot: reads through a non-constant argument.** The name has to
resolve to a constant *at the call site*, so a fallback chain that loops over a
slice literal is missed even though it is a genuine read — `executorOwnerActor`
(`internal/driver/executor.go:512-519`) ranges over
`[]string{"LOOM_FLEET_DB_ACTOR", "LOOM_DRIVER_FLEET_DB_ACTOR", "USER"}` and calls
`os.Getenv(key)`, so `LOOM_DRIVER_FLEET_DB_ACTOR` does not appear below.
`LOOM_FLEET_DB_ACTOR` does, only because it is read by a constant elsewhere.
Prefer a named constant over an inline slice literal when adding such a chain,
and treat this table as a floor rather than a closed set.

**Provenance is package-level, not `file:line`.** This document is meant to be
staleness-gated; a line number would make any edit above a read fail the gate
with no change to the environment contract. The same discipline the sibling
`scripts/openapi-to-md` generator follows.

**No default-value column.** The code's fallbacks are dynamic chains
(`firstNonEmpty(os.Getenv(a), os.Getenv(b), …)`, wrapper parameters, computed
values), not static string literals, so a mechanically derived default would
misinform more often than help. Document a specific default here in prose if it
matters.

**The "Sensitive" flag is name-based only.** It marks names containing
`KEY`, `TOKEN`, `SECRET`, or `PASSWORD`. It is a lint hint, not a data-flow
judgement about whether the value is actually a credential.

**What "git-tracked source" excludes.** The filter is *gitignored*, not
*untracked* — a brand-new uncommitted file (a fresh `doc.go`, say) is untracked
but not ignored, so it is still counted, and a dirty working tree does not
silently drop packages. What the filter does remove is any package containing a
gitignored file: the ~200 session directories under `internal/cli/*/sessions`
and `internal/cli/*/automode/sessions`, and anything else `.gitignore` names.
See `scripts/loomdoc/common.go:59-67`.
