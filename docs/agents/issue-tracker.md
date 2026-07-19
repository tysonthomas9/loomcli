# Issue tracker: loom data (fleet-db)

Issues and PRDs for this repo live in the fleet-db issue backend, accessed through the `loom data` CLI — not GitHub Issues. With `--server` / `LOOM_SERVER_URL` set, commands talk to a loom server over HTTP; otherwise they use the local backend selected by the workspace configuration. Add `-o json` to any command for machine-readable output.

## Conventions

- **Create an issue**: `loom data create --title "..." --description "..." --type <task|bug|feature|epic|chore> --priority <0-4>` (0 critical → 4 backlog). Useful extras: `--acceptance-criteria`, `--design`, `--parent <id>`, `--depends-on <id>` (repeatable), `--label <label>` (repeatable — create time only, see below).
- **Read an issue**: `loom data show <id>`
- **List issues**: `loom data list --status <open|in_progress|review|closed|...> --type <type> --limit <n> -o json`
- **Find available work**: `loom data ready --limit 10` — open, unblocked, unclaimed issues.
- **List blocked issues**: `loom data blocked`
- **Comment on an issue**: `loom data comment <id> "text"`
- **Claim work**: `loom data claim <id>` — atomic.
- **Update fields**: `loom data update <id> --status ... --priority ... --title ... --notes ... --description-from-file <path|-> --depends-on <id> --remove-depends-on <id>`. Setting `--status blocked` requires a reason in `--notes` (`BLOCKED: <why + what unblocks it>`).
- **Close**: `loom data close <id> --reason "done"` (`--force` to close despite open dependencies).

**Label caveat**: labels attach only at creation (`create --label`); `loom data update` cannot add or remove labels on an existing issue. See `docs/agents/triage-labels.md` for how triage-state changes are recorded instead.

**Long bodies**: `create --description` takes an inline string only. For multi-line bodies, create the issue first, then `loom data update <id> --description-from-file -` with a heredoc on stdin.

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo starts treating external GitHub PRs as feature requests; `/triage` reads this flag.)_

## When a skill says "publish to the issue tracker"

Run `loom data create --title "..." --description "..."`.

## When a skill says "fetch the relevant ticket"

Run `loom data show <id>`.

## Wayfinding operations

Used by `/wayfinder`. The tracker supports all of these natively.

- **Map**: an epic issue holding the Notes / Decisions-so-far / Fog body in its description — `loom data create --type epic --title "..." --label wayfinder:map`.
- **Child ticket**: `loom data create --parent <map-id> --label "wayfinder:<type>"` (`research`/`prototype`/`grilling`/`task`). Once claimed, the ticket carries the driving dev as assignee.
- **Blocking**: native dependency edges — `--depends-on <blocker-id>` at create, or `loom data update <id> --depends-on <blocker-id>` / `--remove-depends-on <blocker-id>` later. A ticket is unblocked when every blocker is closed.
- **Frontier query**: `loom data ready --parent <map-id>` — already excludes blocked and claimed issues; first in map order wins.
- **Claim**: `loom data claim <id>` — the session's first write.
- **Resolve**: `loom data comment <id> "<answer>"`, then `loom data close <id> --reason "..."`, then append a context pointer to the map's Decisions-so-far (`loom data update <map-id> --description-from-file -`).
