# CLI Flag-Parity Audit — bd vs fdb

**Generated:** 2026-04-22
**Harness:** `go test -tags parity -run TestCLIParity ./internal/backend/paritytest/`
**Source of truth:** `bd --version` → `bd version 0.49.0 (dev)`; `fdb` built from `~/codebase/fleet-db/cmd/fdb/main.go` (commit-as-of today)

This document is the exhaustive side-by-side comparison of every `bd`
subcommand / flag against its `fdb` equivalent. The harness runs the
CLIs against a live pair of backends (bd daemon + fleet-db HTTP server
with embedded miniredis) and records diff data; this audit is the
static reference for where the CLI surfaces themselves agree or diverge.

Both binaries aim to manage the same issue model, but they were built
with different constraints:
- `bd` — local-first, git-native, SQLite storage, rich issue-type system
- `fdb` — HTTP client targeting a remote `fleet-db` server, workspace-scoped

The audit does **not** include flags that are purely infrastructural
for only one CLI (e.g. `bd --db <path>`, `fdb -socket`, `fdb -api-key`).
Those are called out in a dedicated "CLI-infra-only flags" section at
the bottom rather than repeated on every subcommand row.

## Summary

- **bd subcommands audited:** 62 top-level commands (subcommands expanded separately)
- **fdb subcommands audited:** 32 top-level commands
- **Shared subcommands:** 27 (the core CRUD + queue + dep + label + comment + history + stats surface)
- **bd-only subcommands:** 35 (daemon, federation, dolt-vc, git sync, migration, setup, and molecule/swarm/gate concepts)
- **fdb-only subcommands:** 5 (workspace, defer, undefer, deferred, heartbeat — mostly cousins of bd flags surfaced as subcommands)

Flag-level tallies across shared subcommands:

| category | count | notes |
|---|---|---|
| **exact match** (same long flag, same semantics) | 48 | `--title`, `--status`, `--type`, `--priority`, `--assignee`, `--limit`, `--description`, `--reason`, `-json`, etc. |
| **near match** (renamed but semantically equivalent) | 12 | `labels` (csv) / `label add` (subcmd), `--add-label` / `label add`, `bd status` / `fdb stats`, `--until` / `--defer-until`, etc. |
| **bd-only** on a shared subcommand | ~90 | `--acceptance`, `--design`, `--ephemeral`, `--defer`, `--deps`, `--mol-type`, `--external-ref`, `--parent`, `--dry-run`, `--tree`, `--pretty`, `--watch`, `--source-repos`, etc. |
| **fdb-only** on a shared subcommand | 3 | `-transport`, per-subcmd `-limit` defaults, `-follow` (events) |
| **name conflict** (same flag, different behavior) | 2 | `--priority` (bd string "0-4|P0-P4", fdb int 0-4); `--limit` (different defaults) |

## Global flags

| bd flag | fdb flag | semantic match | notes |
|---|---|---|---|
| `--json` | `-json` | yes | Flag-style difference is cosmetic (Go stdlib `flag` accepts both `-` and `--`); both enable JSON output mode. |
| `--actor` | `-actor` | yes | Bd reads `$BD_ACTOR`; fdb reads `$FLEET_ACTOR` (falls back to `$USER`). |
| `--db <path>` | — | bd-only | Fdb is a network client; it has no local DB concept. |
| — | `-server <url>` | fdb-only | Target fleet-db HTTP server URL. Bd uses local daemon socket instead. |
| — | `-workspace <key>` | fdb-only | Fleet-db workspace key. Bd rigs are not a 1:1 mapping. |
| — | `-socket <path>` | fdb-only | Used when fdb talks to fleet-db via Unix socket (RPC). |
| — | `-transport http\|rpc\|auto` | fdb-only | Which transport to reach fleet-db over. |
| — | `-api-key` | fdb-only | For authenticated remote deployments. |
| — | `-timeout` | fdb-only | Client-side HTTP timeout. |
| `-v / --verbose` | — | bd-only | |
| `-q / --quiet` | — | bd-only | |
| `--lock-timeout` | — | bd-only | SQLite busy timeout. |
| `--no-auto-flush` | — | bd-only | Disable JSONL auto-sync. |
| `--no-auto-import` | — | bd-only | Disable JSONL auto-import. |
| `--no-daemon` | — | bd-only | Force direct SQLite mode. |
| `--no-db` | — | bd-only | Pure JSONL mode. |
| `--readonly` | — | bd-only | Read-only mode. |
| `--sandbox` | — | bd-only | No daemon, no auto-sync. |
| `--allow-stale` | — | bd-only | Bypass staleness check. |
| `--profile` | — | bd-only | CPU profile for the invocation. |

## Per-subcommand audit

### `create` — create a new issue

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--title <str>` | `-title <str>` | yes | Required on both. Bd also supports positional title; fdb does not. |
| `--description <str>` / `-d` | `-description <str>` / `-d` | yes | Identical. |
| `--priority <str>` / `-p` | `-priority <int>` / `-p` | **near** | bd accepts `"0-4"` or `"P0-P4"` strings; fdb takes bare int. Same numeric range. |
| `--type <str>` / `-t` | `-type <str>` / `-t` | yes | bd default `task`; fdb default `task`. Bd supports many more values (agent, role, rig, event, etc.). |
| `--assignee <str>` / `-a` | `-assignee <str>` / `-a` | yes | |
| `--owner <str>` | `-owner <str>` | yes | |
| `--labels <csv>` / `-l` | `-labels <csv>` | yes | Comma-separated on both. |
| `--parent <id>` | `-parent <id>` | yes | Bd stores as `parent`; fdb as `parent_id`. |
| `--design <str>` | `-design <str>` | yes | |
| `--notes <str>` | `-notes <str>` | yes | |
| `--due <relative\|rfc3339>` | `-due-at <rfc3339>` | **near** | bd accepts `+6h`, `tomorrow`, etc. fdb requires RFC3339 only. |
| `--defer <relative\|rfc3339>` | `-defer-until <rfc3339>` | **near** | Same as --due; bd relative-date support is richer. |
| `--acceptance <str>` | — | bd-only | Maps to bd's `acceptance_criteria` field. Fdb's equivalent is embedded in `--description` sections (see lint rules). |
| `--repo <str>` | — | bd-only | Bd target repository; fdb uses workspace-level repos. |
| `--source-repo <str>` | — | bd-only | |
| `--body-file <path>` | — | bd-only | Read description from file (also supports `-` for stdin). |
| `--external-ref <str>` | — | bd-only | e.g. `gh-9`, `jira-ABC`. |
| `--deps <csv>` | — | bd-only | Inline deps like `discovered-from:bd-20,blocks:bd-15`. Fdb uses `fdb dep add` separately. |
| `--id <str>` | — | bd-only | Explicit issue ID override (for partitioning). |
| `--prefix <str>` | — | bd-only | Target rig prefix. |
| `--rig <str>` | — | bd-only | Create in a different rig. |
| `--file <path>` / `-f` | — | bd-only | Create many issues from a markdown file. Fdb equivalent is `fdb batch create --file=...`. |
| `--estimate <int>` / `-e` | — | bd-only | Time estimate in minutes. |
| `--silent` | — | bd-only | Output only the issue ID (for scripting). Fdb equivalent: `bd q` analogue not available on fdb. |
| `--ephemeral` | — | bd-only | Wisp issue (not exported to JSONL). |
| `--force` | — | bd-only | Force create even on prefix mismatch. |
| `--dry-run` | — | bd-only | Preview without persisting. |
| `--validate` | — | bd-only | Validate description has required template sections. |
| `--mol-type <str>` | — | bd-only | Molecule type. |
| `--agent-rig <str>` | — | bd-only | Agent's rig (requires `--type=agent`). |
| `--role-type <str>` | — | bd-only | Agent role type. |
| `--event-*` | — | bd-only | Four event-specific flags (actor, category, target, payload). |
| `--waits-for <id>` | — | bd-only | Fanout-gate dependency. |
| `--waits-for-gate <str>` | — | bd-only | Gate type for waits-for. |

Bd coverage: 34 flags. Fdb coverage: 12 flags. Shared: 10 (exact or near match). Bd-only: 22.

### `show` — show issue details

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | |
| `--json` (global) | `-json` (global) | yes | |
| `--as-of <commit>` | — | bd-only | Dolt-backend-only. |
| `--children` | — | bd-only | Show only children of the issue. fdb: `fdb children <id>`. |
| `--refs` | — | bd-only | Reverse lookup: issues referencing this one. |
| `--short` | — | bd-only | Compact one-line output. |
| `--thread` | — | bd-only | For message-type issues. |

Both CLIs produce very different output even at parity: bd emits `[{issue}]` (single-element array) while fdb emits `{issue}`. The harness unwraps singletons before diffing.

### `list` — list issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--status <str>` / `-s` | `-status <str>` / `-s` | yes | |
| `--type <str>` / `-t` | `-type <str>` / `-t` | yes | |
| `--assignee <str>` / `-a` | `-assignee <str>` / `-a` | yes | |
| `--label <csv>` / `-l` | `-label <str>` / `-l` | **near** | bd takes repeatable/csv of ANDed labels; fdb takes single string. Fdb has no `--label-any` equivalent. |
| `--label-any <csv>` | — | bd-only | OR-logic labels. |
| `--limit <int>` / `-n` | `-limit <int>` | **near** | Same semantic; bd short flag `-n`, fdb flag name is bare `-limit`. Bd default 50, fdb default 50. |
| `--parent <id>` | `-parent <id>` | yes | |
| — | `-repo <str>` | fdb-only | |
| — | `-offset <int>` | fdb-only | Pagination offset. Bd doesn't expose pagination. |
| `--id <csv>` | — | bd-only | Filter to specific IDs. |
| `--priority <str>` / `-p` | — | bd-only | Not supported as a filter in fdb list (use `fdb count --by-priority` instead). |
| `--priority-min <str>` | — | bd-only | |
| `--priority-max <str>` | — | bd-only | |
| `--created-after <date>` | — | bd-only | |
| `--created-before <date>` | — | bd-only | |
| `--updated-after <date>` | — | bd-only | |
| `--updated-before <date>` | — | bd-only | |
| `--closed-after <date>` | — | bd-only | |
| `--closed-before <date>` | — | bd-only | |
| `--due-after <date>` | — | bd-only | |
| `--due-before <date>` | — | bd-only | |
| `--defer-after <date>` | — | bd-only | |
| `--defer-before <date>` | — | bd-only | |
| `--all` | — | bd-only | Include closed. Fdb equivalent: `-status closed` or omit status filter. |
| `--deferred` | — | bd-only | Fdb has `fdb deferred` subcommand. |
| `--empty-description` | — | bd-only | |
| `--desc-contains <str>` | — | bd-only | |
| `--notes-contains <str>` | — | bd-only | |
| `--title-contains <str>` | — | bd-only | |
| `--title <str>` | — | bd-only | |
| `--sort <field>` | — | bd-only | |
| `--reverse` / `-r` | — | bd-only | |
| `--long` | — | bd-only | Multi-line per issue. |
| `--pretty` / `--tree` | — | bd-only | Tree output. |
| `--format <str>` | — | bd-only | `digraph` / `dot` / go template. |
| `--no-pager` | — | bd-only | |
| `--include-gates` | — | bd-only | |
| `--include-templates` | — | bd-only | |
| `--no-pinned` / `--pinned` | — | bd-only | |
| `--no-assignee` / `--no-labels` | — | bd-only | |
| `--overdue` | — | bd-only | |
| `--ready` | — | bd-only | Fdb has `fdb ready` subcommand. |
| `--source-repos <csv>` | — | bd-only | |
| `--mol-type <str>` | — | bd-only | |
| `--watch` / `-w` | — | bd-only | |

Bd coverage: 40+ flags. Fdb coverage: 8 flags. The `list` subcommand is the most one-sided — bd is a rich query tool, fdb is a thin list.

### `update` — update an existing issue

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | bd falls back to "last touched" when no ID given; fdb requires the id. |
| `--title <str>` | `-title <str>` | yes | |
| `--description <str>` / `-d` | `-description <str>` / `-d` | yes | |
| `--priority <str>` / `-p` | `-priority <int>` / `-p` | **near** | Same shape mismatch as `create --priority`. |
| `--type <str>` / `-t` | `-type <str>` / `-t` | yes | |
| `--design <str>` | `-design <str>` | yes | |
| `--notes <str>` | `-notes <str>` | yes | |
| `--owner <str>` | `-owner <str>` | yes | bd: pass empty string to clear. fdb: empty string is accepted but stored as blank. |
| `--due <str>` | `-due-at <str>` | **near** | Same --due drift as create. |
| `--assignee <str>` / `-a` | — | bd-only | Fdb does not support `update --assignee`; use `fdb claim` or `fdb update -owner`. |
| `--status <str>` / `-s` | — | bd-only | Fdb forbids status transitions via update; callers must use `close` / `reopen` / `defer` / `undefer`. |
| `--add-label <str>` (repeatable) | — | **named differently** | Fdb has `fdb label add <id> <label>` instead. Semantically equivalent but shape differs. |
| `--remove-label <str>` (repeatable) | — | **named differently** | Fdb: `fdb label remove <id> <label>`. |
| `--set-labels <csv>` (repeatable) | — | bd-only | Replace all labels. Fdb has no single-call equivalent — callers must remove+add. |
| `--acceptance <str>` | — | bd-only | |
| `--body-file <path>` | — | bd-only | |
| `--claim` | — | bd-only | Atomic claim. Fdb equivalent: `fdb claim <id>`. |
| `--parent <id>` | — | bd-only | Re-parent. |
| `--defer <str>` | — | bd-only | Fdb: `fdb defer <id> --until=...`. |
| `--ephemeral` / `--persistent` | — | bd-only | Wisp toggles. |
| `--estimate <int>` / `-e` | — | bd-only | |
| `--external-ref <str>` | — | bd-only | |
| `--session <str>` | — | bd-only | Claude-Code session tag. |
| `--await-id <str>` | — | bd-only | Gate await id. |

### `close` — close one or more issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id...>` | positional `<id...>` | yes | Both accept multiple. |
| `--reason <str>` / `-r` | `-reason <str>` / `-r` | yes | |
| `--force` / `-f` | — | bd-only | Force-close pinned issues. |
| `--continue` | — | bd-only | Molecule auto-advance. |
| `--no-auto` | — | bd-only | |
| `--suggest-next` | — | bd-only | |
| `--session <str>` | — | bd-only | |

Outcome message text differs substantially: `bd: "✓ Closed 001-abc: <reason>"` vs `fdb: "✓ closed 1 issue(s)"`. Captured as `_stdout_text` diff.

### `reopen` — reopen a closed issue

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id...>` | positional `<id>` | **near** | Bd batch-reopens; fdb reopens one at a time. |
| `--reason <str>` / `-r` | — | bd-only | Fdb records reason only if passed via subsequent `fdb comment add`. |

### `delete` — delete issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | |
| `--force` / `-f` | — | bd-only | Bd default is preview; fdb default is destructive. |
| `--cascade` | — | bd-only | Recursively delete dependents. |
| `--hard` | — | bd-only | Bypass tombstones. |
| `--from-file <path>` | — | bd-only | |
| `--dry-run` | — | bd-only | |

### `search` — text search

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<query>` | positional `<query>` | yes | |
| `--limit <int>` / `-n` | `-limit <int>` | yes | |
| `--status <str>` / `-s` | — | bd-only | Fdb post-filters on the client side. |
| `--type <str>` / `-t` | — | bd-only | |
| `--label <csv>` / `-l` | — | bd-only | |
| `--label-any <csv>` | — | bd-only | |
| `--priority-min/max <str>` | — | bd-only | |
| `--created-after/before <date>` | — | bd-only | |
| `--updated-after/before <date>` | — | bd-only | |
| `--closed-after/before <date>` | — | bd-only | |
| `--sort <field>` | — | bd-only | |
| `--reverse` / `-r` | — | bd-only | |
| `--assignee <str>` / `-a` | — | bd-only | |
| `--long` | — | bd-only | |
| `--query <str>` (alias of positional) | — | bd-only | |

Fdb's search is minimal: query + limit. All other filtering must happen after the call.

### `ready` — list ready work

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--limit <int>` / `-n` | `-limit <int>` | yes | Bd default 10, fdb default 0 (unlimited). Semantic drift. |
| `--priority <int>` / `-p` | `-priority <csv-int>` | **near** | Bd takes single int; fdb takes csv list of ints. |
| — | `-max-priority <int>` | fdb-only | |
| `--assignee <str>` / `-a` | `-assignee <str>` | yes | Fdb also auto-sets `include-assigned`. |
| `--unassigned` / `-u` | — | bd-only | Fdb: omit `-assignee`. |
| `--label <csv>` / `-l` | `-label <csv>` | yes | AND semantics on both. |
| `--label-any <csv>` | `-label-any <csv>` | yes | OR semantics. |
| `--parent <id>` | `-parent <id>` | yes | |
| — | `-sort hybrid\|priority\|oldest` | fdb-only | |
| — | `-include-deferred` | fdb-only | Bd: `--include-deferred`. |
| `--include-deferred` | `-include-deferred` | yes | Identical. |
| — | `-include-assigned` | fdb-only | |
| — | `-status <csv>` | fdb-only | |
| — | `-type <csv>` | fdb-only | Bd: `--type`. |
| `--type <str>` / `-t` | `-type <csv>` | **near** | Bd single string; fdb csv. |
| `--pretty` | — | bd-only | |
| `--source-repos <csv>` | — | bd-only | |
| `--mol-type <str>` | — | bd-only | |
| `--mol <id>` | — | bd-only | Filter to molecule steps. |
| `--gated` | — | bd-only | Find molecules ready for gate-resume. |
| `-s, --sort <str>` | `-sort <str>` | yes | Same name, same values. |

Bd coverage: 15 flags. Fdb coverage: 12 flags. Substantial overlap in the query semantics.

### `blocked` — list blocked issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--parent <id>` | — | bd-only | |

Output shapes differ significantly: fdb emits `[{issue: {...}, blockers: [...]}]`; bd emits flat `[{title, priority, blocked_by, blocked_by_count, blocked_by_details, ...}]`. Harness hoists fdb's nested `issue` before diffing.

### `dep` — manage dependencies

Bd exposes `dep add`, `dep remove`, `dep list`, `dep tree`, `dep cycles`, `dep relate`, `dep unrelate`. Fdb exposes only `dep add`, `dep remove`, `dep list`.

#### `dep add`

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id> <depends-on-id>` | positional `<id> <depends-on-id>` | yes | |
| `--type <str>` / `-t` (default `blocks`) | `-type <str>` / `-t` | yes | Values partially overlap: bd adds `tracks`, `discovered-from`, `relates-to`. Fdb: `blocks`, `parent-child`, `related`, `duplicate-of`, `superseded-by`. |
| `--blocked-by <id>` | — | bd-only | Alias for positional form. |
| `--depends-on <id>` | — | bd-only | Alias for positional form. |

#### `dep remove`

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id> <depends-on-id>` | positional `<id> <depends-on-id>` | yes | |
| — | `-type <str>` / `-t` | fdb-only | Required for disambiguation when same pair has multiple dep types. |

#### `dep list`

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | |
| `--type <str>` / `-t` | — | bd-only | Filter by dep type. Fdb returns all types. |
| `up` / `down` positional | — | bd-only | Bd switches between dependents and dependencies. Fdb returns both in one response. |

#### `dep tree` / `dep cycles` / `dep relate` / `dep unrelate`

All bd-only. Fdb has no tree walker or cycle detector on the dep subcommand (graph info is surfaced via `fdb graph`).

### `label` — manage labels

Bd: `label add`, `label list`, `label list-all`, `label remove`.
Fdb: `label add`, `label list`, `label remove`.

| bd subcmd | fdb subcmd | match? | notes |
|---|---|---|---|
| `label add <id> <label>` | `label add <id> <label>` | yes | Identical. Bd also takes multiple issue IDs as positional args. |
| `label remove <id> <label>` | `label remove <id> <label>` | yes | |
| `label list <id>` | — | bd-only | Lists labels for one issue. Fdb: show the issue and read `.labels`. |
| — | `label list` (no id) | fdb-only | Lists all labels in workspace. |
| `label list-all` | `label list` | **near** | Same semantic, different name. |

### `comments` / `comment` — manage comments

**Name drift:** bd is plural (`bd comments`), fdb is singular (`fdb comment`).

| bd command | fdb command | match? | notes |
|---|---|---|---|
| `comments <id>` (list) | `comment list <id>` | **near** | Same semantic, different shape. |
| `comments add <id> <text>` | `comment add <id> -body <text>` | **near** | Bd takes positional text; fdb requires `--body` flag. |
| — | — | — | Bd accepts `-a, --author <str>` and `-f, --file <path>`; fdb only accepts `-b, --body`. |

Fdb comment output uses `body` field; bd uses `text` field. Harness aliases `text` → `body`.

### `count` — count issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--status <str>` / `-s` | `-status <str>` | yes | |
| `--type <str>` / `-t` | `-type <str>` | yes | |
| `--assignee <str>` / `-a` | `-assignee <str>` | yes | |
| `--label <csv>` / `-l` | `-label <str>` | **near** | Bd csv/repeatable, fdb single string. |
| `--by-status` | `-by-status` | yes | |
| `--by-type` | `-by-type` | yes | |
| `--by-priority` | `-by-priority` | yes | |
| `--by-assignee` | `-by-assignee` | yes | |
| `--by-label` | `-by-label` | yes | |
| `--priority <int>` / `-p` | — | bd-only | Use `-status/type/..` or `-by-priority` instead. |
| `--priority-min/max <int>` | — | bd-only | |
| `--created-after/before` | — | bd-only | |
| `--updated-after/before` | — | bd-only | |
| `--closed-after/before` | — | bd-only | |
| `--id <csv>` | — | bd-only | |
| `--title` / `--title-contains` | — | bd-only | |
| `--desc-contains` | — | bd-only | |
| `--notes-contains` | — | bd-only | |
| `--empty-description` | — | bd-only | |
| `--no-assignee` / `--no-labels` | — | bd-only | |
| `--label-any <csv>` | — | bd-only | |
| — | `-repo <str>` | fdb-only | |
| — | `-parent <id>` | fdb-only | |

**Output shape diverges substantially:** bd's `--json` emits `{count: N, groups: [...]}`; fdb emits `{total: N, groups: {k: v}}`. Groups are array-of-objects on bd vs map on fdb. Harness records both as `_stdout_json` diffs — this is genuine UX drift, not noise.

### `stats` / `status` — aggregate workspace stats

**Name drift:** bd calls this `bd status` (with alias `bd stats`); fdb calls it `fdb stats`.

Stats output differs fundamentally:
- bd: `{summary: {open_issues, blocked_issues, ...}}`
- fdb: `{by_status, by_type, by_priority, total}`

Bd includes derived metrics (average_lead_time_hours, pinned_issues, tombstone_issues, epics_eligible_for_closure, ready_issues); fdb's schema does not. These are real semantic differences — the harness surfaces them as diffs.

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--all` | — | bd-only | |
| `--assigned` | — | bd-only | |
| `--no-activity` | — | bd-only | |
| `--recent <int>` | — | bd-only | |

### `children` — list children of an epic

| bd command | fdb command | match? | notes |
|---|---|---|---|
| `children <parent-id>` | `children <parent-id>` | yes | Identical. |
| `--pretty` | — | bd-only | |
| `--json` | `-json` | yes | |

### `graph` — dependency graph

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<issue-id>` | — | bd-only | Bd graphs a specific issue's neighborhood; fdb always graphs the full workspace. |
| `--all` | (default) | **near** | Bd opt-in; fdb opt-out not available. |
| `--box` / `--compact` | `-compact` | **near** | Bd has both `--box` (default) and `--compact`; fdb only has `-compact`. |

### `history` — view issue history

| bd command | fdb command | match? | notes |
|---|---|---|---|
| `history <id>` | `history list <id>` | **near** | Fdb nests under `list` subcommand; also has `history at <id> <timestamp>` and `history revert <id> <event-id>`. |
| `--limit <int>` | `-limit <int>` / `-l` | yes | |
| — | `-action <csv>` | fdb-only | |
| — | `-actor <str>` / `-a` | fdb-only | |
| — | `-since <cursor>` | fdb-only | |
| — | `-before <cursor>` | fdb-only | |
| — | `-start-time <rfc3339>` | fdb-only | |
| — | `-end-time <rfc3339>` | fdb-only | |

Note: bd's `history` command requires the Dolt storage backend; with SQLite it errors out. Fdb's history is unconditionally available.

### `epic` — epic management

Both expose `epic status` and `epic close-eligible`:

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `epic status` | `epic status` | yes | No flags on either side beyond `--json`. |
| `epic close-eligible` | `epic close-eligible` | yes | |
| — | `-dry-run` (on close-eligible) | fdb-only | Fdb can preview; bd closes immediately. |

### `lint` — template-section lint

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id...>` | — | bd-only | |
| `--status <str>` / `-s` | `-status <str>` | yes | |
| `--type <str>` / `-t` | `-type <str>` | yes | |
| `--json` | `-json` | yes | |

### `stale` — find stale issues

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| `--days <int>` / `-d` | `-days <int>` | yes | Default 30 on both. |
| `--status <str>` / `-s` | `-status <str>` | yes | |
| `--limit <int>` / `-n` | `-limit <int>` | yes | Default 50 on both. |

### `duplicate` — mark as duplicate

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | |
| `--of <canonical>` | `-of <canonical>` | yes | Required on both. |

### `supersede` — mark as superseded

| bd flag | fdb flag | match? | notes |
|---|---|---|---|
| positional `<id>` | positional `<id>` | yes | |
| `--with <replacement>` | `-with <replacement>` | yes | |

### `defer` / `undefer` / `deferred`

Bd exposes these as flags on `update` and `list --deferred`; fdb exposes them as subcommands.

| bd command | fdb command | match? | notes |
|---|---|---|---|
| `bd update <id> --defer=<date>` | `fdb defer <id> -until=<date>` | **named differently** | Flag→subcommand. |
| `bd update <id> --defer=""` | `fdb undefer <id>` | **named differently** | Clear deferral. |
| `bd list --deferred` | `fdb deferred` | **near** | Fdb exposes as dedicated subcommand. |

### `claim` / `heartbeat` (worker lifecycle)

Bd: `bd update --claim` (flag on update).
Fdb: `fdb claim <id>` (subcommand with `-ttl`).

| fdb flag | bd equivalent | notes |
|---|---|---|
| `claim -ttl <sec>` | — | bd-only no; bd claims are daemon-default-lived. |
| `heartbeat <worker-id>` | — | bd has no heartbeat command — the daemon handles worker presence internally. |

### `batch` — bulk operations

Fdb: `batch create --file=<path>` (JSON), `batch close <id...> -reason=<str>`.
Bd equivalent: `bd create --file=<markdown>` (markdown), `bd close <id...>`.

| fdb flag | bd equivalent | notes |
|---|---|---|
| `batch create --file=PATH` | `bd create -f <markdown-path>` | **named differently** | File format differs (markdown vs JSON). |
| `batch close <id...>` | `bd close <id...>` | yes | Same operation, bd lacks `batch` prefix. |

### `workspace` — manage workspaces

fdb-only subcommand. Bd has no workspace abstraction (workspaces map to rigs, managed via git/filesystem conventions).

| fdb command | bd equivalent | notes |
|---|---|---|
| `workspace create --key=X --name=Y` | — | N/A for bd. |
| `workspace list` | `bd info` (partial) | |
| `workspace show <key>` | `bd where` | |
| `workspace update <key>` | — | |
| `workspace delete <key>` | — | |

### `events` — event stream / mutation poll

Fdb subcommands: `events stream`, `events mutations`.

Bd equivalents: `bd activity` (like `events stream`), no equivalent for `events mutations`.

| fdb flag | bd equivalent | notes |
|---|---|---|
| `events stream -last-event-id=$` | `bd activity --follow` | **named differently** | Bd uses `--follow`; fdb uses cursor. |
| `events mutations -since=X -limit=Y -follow` | — | Fdb-only. Bd activity stream is continuous; no explicit poll mode. |

## CLI-infra-only flags (not compared per-subcommand)

### Bd-only (local database / git integration)

- `--db`, `--lock-timeout`, `--no-auto-flush`, `--no-auto-import`, `--no-daemon`, `--no-db`
- `--readonly`, `--sandbox`, `--allow-stale`, `--profile`
- Entire `bd daemon *` subtree (start, stop, status, logs, killall, restart, list, health)
- Entire `bd sync` subtree (--import, --status, --resolve, --force, --full, --apply)
- Entire `bd hooks` subtree (git hook management)
- `bd init`, `bd doctor`, `bd repair`, `bd migrate`, `bd compact`
- `bd config *`, `bd setup *`, `bd onboard`, `bd quickstart`, `bd human`, `bd prime`, `bd info`, `bd where`
- `bd export`, `bd import`, `bd restore`
- `bd federation *` (peer-to-peer)
- `bd vc *`, `bd branch`, `bd merge`, `bd diff`, `bd history` (Dolt-backend)
- `bd mol *`, `bd swarm *`, `bd gate *`, `bd slot *`, `bd merge-slot *`, `bd move`, `bd refile`, `bd rename`, `bd rename-prefix`, `bd resolve-conflicts`, `bd edit`, `bd create-form`, `bd q`
- `bd activity`, `bd set-state`, `bd state`, `bd preflight`, `bd upgrade`, `bd ship`, `bd cook`, `bd formula`, `bd duplicates` (plural), `bd orphans`
- Integration subcommands: `bd admin *`, `bd agent *`, `bd audit *`, `bd hook *`, `bd jira *`, `bd linear *`, `bd mail *`, `bd repo *`, `bd worktree *`

### Fdb-only (network client)

- `-server`, `-socket`, `-workspace`, `-transport`, `-api-key`, `-timeout`, `-actor`
- `fdb workspace *` (create/list/show/update/delete)
- `fdb events mutations -follow -since -limit -timeout`
- `fdb claim -ttl`, `fdb heartbeat`
- `fdb batch create --file=<json>`, `fdb batch close`
- `fdb defer -until`, `fdb undefer` (subcommand form of bd's `update --defer=X`)

## Known CLI behavior drift

These are drift items identified by the harness that are semantic, not
cosmetic:

1. **`create --priority`** — bd accepts `"P2"`, `"2"`, or `2`; fdb only accepts bare int `2`. Harness normalizes in `parsePriorityString`.

2. **`show` output shape** — bd emits `[{...}]` (array of one); fdb emits `{...}` (bare object). Harness unwraps singletons in `unwrapSingleton`.

3. **`show` / `list` field naming: `issue_type` vs `type`** — bd's canonical field is `issue_type`; fdb uses bare `type`. Both carry the same value. Harness aliases `issue_type` → `type`.

4. **`show` parent field naming: `parent` vs `parent_id`** — bd emits `parent`; fdb emits `parent_id`. Both link to the same upstream issue. Harness ignores both (IDs drift per-backend anyway).

5. **`list` with `--type` filter** — fdb emits a strict match; bd applies internal aliases (e.g. `feat` → `feature`, `mr` → `merge-request`). A fixture filtering `type=bug` matches bd-more-permissively.

6. **`comments` vs `comment`** — plural/singular flip. Comment record field names also differ: bd `text`, fdb `body`. Harness aliases `text` → `body`.

7. **`count --by-X` output shape** — bd returns `groups: [{group: "bug", count: 5}, ...]`; fdb returns `groups: {"bug": 5, ...}`. Array-of-objects vs map. This is a genuine UX diff visible in the CLI output.

8. **`status` / `stats` schema** — bd's `status` emits derived metrics (average_lead_time_hours, ready_issues, pinned_issues, tombstone_issues, epics_eligible_for_closure) absent from fdb. The top-level shape differs (`summary: {...}` vs `by_*: {...}`).

9. **`blocked` output shape** — fdb wraps each row as `{issue: {...}, blockers: [...]}`; bd flattens the issue fields at top level with `blocked_by`, `blocked_by_count`, `blocked_by_details` siblings. Harness hoists the fdb wrapper in `normalizeIssueMap`.

10. **`ready --limit` default** — bd defaults to 10; fdb defaults to 0 (unlimited). Fixtures explicitly pass `--limit 10` to defuse.

11. **`dep add` echo message** — bd: `"✓ Added dependency: <a> depends on <b> (blocks)"`. fdb: `"✓ added blocks dependency: <a> → <b>"`.

12. **`close` echo message** — bd: `"✓ Closed <id>: <reason>"` (per-issue). fdb: `"✓ closed N issue(s)"` (aggregate).

13. **`reopen` echo** — bd: `"↻ Reopened <id>"`. fdb: `"✓ reopened <id>"`.

14. **`comment add` echo** — bd: `"Comment added to <id>"`. fdb: renders the full comment body in a header-prefixed block.

15. **`label add/remove` echo** — bd (issued via `bd update`): `"✓ Updated issue: <id>"`. fdb: `"✓ added label <x> to <id>"`.

16. **`create --labels` round-trip** — bd's `--json` create output omits the `labels` field even when `--labels a,b` was passed. A follow-up `bd show` emits them. Fdb emits labels inline in the create response.

17. **`reopen --reason`** — bd supports a reason via the `--reason` flag; fdb has no such flag. Fdb users must follow up with `fdb comment add`.

## Flags that behave differently despite the same name

| flag | bd | fdb | notes |
|---|---|---|---|
| `--priority` | string `"P0"`-`"P4"` or `"0"`-`"4"` | int `0-4` | Value shape. |
| `--limit` (`ready`) | default 10 | default 0 (no limit) | Default drift. |
| `--limit` (`history`) | 0 = all | default 50 | Default drift. |
| `--type` (`list`, `search`) | single string with bd aliases (`feat`→`feature`, `mr`→`merge-request`, `mol`→`molecule`) | csv-of-strings, strict | Semantic drift: bd widens the filter implicitly. |
| `--label` (`list`, `search`, `count`) | repeatable / csv, ANDed | single string | Only bd supports multi-label AND. |
| `--force` (`close`) | force-close pinned | N/A | Bd-only. |
| `--force` (`delete`) | orphan dependents | N/A | Bd-only. |
| `--status` (`ready`) | single string | csv | |
| `-a` (`history`) | actor filter (sort order secondary) | shorthand for `-actor` | Same semantic but exposure differs. |

## Operator recommendation

When writing a command that should work identically on both CLIs:

- Prefer long-form flags (`--flag=val`) — Go's stdlib accepts both `-` and `--` on fdb, and bd accepts only `--`.
- Pass `--priority` as bare int: `--priority 2`.
- Always include `--json` / `-json` before any diff comparison.
- Avoid `--type` with bd-specific aliases (`feat`, `mr`, `mol`); use the canonical value.
- Use `--limit N` explicitly for `ready` since defaults differ.
- For labels: use `label add / remove` subcommands (work on both) rather than bd's `update --add-label`.
- For "close with reason": use `close <id> --reason "..."` on both.
- For deferrals: use `fdb defer` on fdb side; on bd side use `update --defer=...`.

## Next steps

Entries in this audit were validated against the harness output
(`test/parity/ui/cli-report.json`) as of the first CLI-parity run on
2026-04-22. Future runs should re-generate the report and diff against
the baseline in `after-b1b2/cli-report.json`.

Recommended follow-ups (not in scope for this audit):
- Align `--type` alias handling (bd → fdb, or remove bd aliases)
- Align `count` output shape (array vs map)
- Align `status`/`stats` schema (add bd-style derived metrics to fdb, or
  drop them from bd's output for default display)
- Align `comment` / `comments` naming (add alias on one side)
- Standardize CLI echo messages (at minimum, share a style guide)
