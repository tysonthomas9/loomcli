# Issue tracker: loom data (fleet-db)

Issues for this repo live in fleet-db, driven by the `loom data` CLI (see
`AGENTS.md`). There are two fleet-db backends and they are **not**
interchangeable:

- **Server backend** — the real workspaces (e.g. `SF-RC1`) on a running loom
  server or the aether-dev deployment. Reached with `--server <url>` or
  `LOOM_SERVER_URL`. This is where product work lives, and where daemon agents
  poll for claimable tasks.
- **Local backend** — an embedded fleet-db that spins up per invocation from
  `~/.loom/fleet-db/redis-snapshot.json` on this machine. Reached by passing
  **no** `--server` and an explicit `--workspace`. No daemon agents poll it.

Build or locate the CLI first: `go build -o loom ./cmd/loom` (or use an
installed `loom`). Commands below assume `loom` on `PATH`.

## Conventions

- **Create**: `loom data create --workspace <WS> --type <task|bug|feature|epic> --title "..." --description "..." --priority <0-4>`
- **Read**: `loom data show <ID> --workspace <WS> -o json`
- **List**: `loom data list --workspace <WS> [--parent <ID>] [--status <s>] -o json`
- **Comment**: `loom data comment <ID> --workspace <WS> "..."`
- **Close**: `loom data close <ID> --workspace <WS> --reason "..."`
- **Update fields**: `loom data update <ID> --workspace <WS> [--status ...] [--assignee ...] [--design ...] [--title ...] [--priority ...] [--notes ...] [--description ... | --description-from-file <path|->] [--depends-on <ID>] [--remove-depends-on <ID>]`
- **Labels**: `--label <name>` (repeatable) at create. The `update` CLI does **not** expose label editing today, but the capability is plumbed end-to-end — `backend.UpdateParams` carries `AddLabels`/`RemoveLabels`/`SetLabels` (`internal/backend/types.go:469-471`) and the API schema has `add_labels`/`remove_labels`/`set_labels` (`PatchIssueRequest`). Wiring `--add-label`/`--remove-label`/`--set-label` into `internal/cli/data/update.go` is a flags-only change; until then, post-hoc relabelling needs a direct API call.
- **Dependencies**: `--depends-on <ID>` at create or on `update` (`--remove-depends-on` to drop one). Edges are a separate resource from the issue PATCH — `update` composes `AddDependency`/`RemoveDependency` (`internal/cli/data/update.go:171-183`). A ticket is unblocked when every issue it depends on is closed.

> **Check your `loom` binary is current.** These flags are read from source at
> `internal/cli/data/update.go`. An older installed `loom` on `PATH` will reject
> them with `unknown flag`, which reads like a missing feature but is a stale
> build. Verify with `loom data update --help`, and rebuild with
> `go build -o loom ./cmd/loom` if a documented flag is absent.
- Priority scale: `0` critical, `1` high, `2` medium, `3` low, `4` backlog.

`--workspace` is **mandatory** on the local backend — there is no active-workspace
default, and a missing one fails with `no active workspace`.

## When a skill says "publish to the issue tracker"

Create an issue with `loom data create` in the relevant workspace.

## When a skill says "fetch the relevant ticket"

`loom data show <ID> --workspace <WS> -o json`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is an `epic` issue; its **tickets** are `task`
issues parented to it. Blocking is native (`--depends-on`).

**Wayfinder maps live on the LOCAL backend, workspace `LOOMCLI`** — not on any
server workspace. This is deliberate: server workspaces (SF-RC1, …) are polled
by daemon agents that would claim an open, unassigned planning ticket and try to
*build* it. The local backend has no such pollers, so wayfinder's
"open + unassigned = unclaimed frontier" convention holds. Always pass
`--workspace LOOMCLI` and no `--server`.

- **Map**: `loom data create --workspace LOOMCLI --type epic --label wayfinder:map --title "... (wayfinder map)"`. The Destination / Notes / Decisions-so-far / Fog body is the issue description.
- **Child ticket**: `loom data create --workspace LOOMCLI --type task --parent <MAP-ID> --label wayfinder:<type> --title "..."`, where `<type>` is `research`/`prototype`/`grilling`/`task`. Wire blocking in a second pass with `loom data update <child> --workspace LOOMCLI --depends-on <blocker>` (issues need ids before they can reference each other). Labels, by contrast, must be set at create time — see Conventions.
- **Frontier query**: `loom data ready --workspace LOOMCLI --parent <MAP-ID>`. The `--parent` scope is **required** — a bare `loom data ready` excludes children of an open epic, so the map's tickets do not appear without it. `ready` already drops blocked and claimed (assignee-set) tickets; take the first in id order.
  - **Gotcha: `ready` does not return the `assignee` field.** Its rows carry `owner` (the creator — typically set on *every* ticket, so it is NOT the claim) but no `assignee`. You therefore cannot tell claimed-ness from `ready` output; use `loom data list --parent <MAP-ID>` or `show <ID>` to see `assignee`. Auditing a map's claims with `ready` alone will silently mislead.
  - **Watch for stale claims.** A ticket assigned during charting and never worked stays off the frontier indefinitely. Audit periodically with `list` and clear with `loom data update <ID> --workspace LOOMCLI --assignee ""`.
  - The embedded local backend loads `~/.loom/fleet-db/redis-snapshot.json` per invocation and writes it back. **Overlapping invocations DO lose writes — verified 2026-07-22.** Two concurrent wayfinder sessions blanked the `LOOMCLI-169` map body twice; the losing writer's issue record came back with no `description` at all. Always re-read after a batch of mutations, and never assume a write landed because the command printed `updated`.
  - Overlapping invocations also collide on the lock outright: `embedded fleet-db already running: lock … is held`. The command exits non-zero having done nothing, so a `2>/dev/null` pipeline yields **empty output**, not an error you'd notice. Retry rather than treating an empty result as "no rows".
- **Blocked**: `loom data blocked --workspace LOOMCLI`.
- **Claim**: `loom data update <ID> --workspace LOOMCLI --assignee <you>` — the session's first write, before any work.
- **Resolve**: `loom data comment <ID> --workspace LOOMCLI "<answer>"`, then `loom data close <ID> --workspace LOOMCLI --reason "..."`, then append a one-line context pointer (gist + id) to the map's Decisions-so-far. Store any large artifact (research findings, a spec) in the ticket's `--design` field, not only in a scratchpad file — the scratchpad is per-session and does not persist.

### Durability caveat

The local backend persists to a single `~/.loom/fleet-db/redis-snapshot.json`
on this machine. It survives across sessions here but is **not** committed and
**not** backed up. A map that needs to outlive this machine should be migrated to
a synced tracker (the server fleet-db, or beads via git) — a migration, not a
rewrite.

**Recovering a clobbered issue body.** Every mutation is journalled to a Redis
stream in the same snapshot, so a lost `description` is recoverable without a
backup. Find the entry keyed
`fleet-db:<WS>:events:issue:<ID>` in `~/.loom/fleet-db/redis-snapshot.json`; its
`stream` is the ordered event list, and each event's `values.after` is a JSON
issue delta. Walk it backwards for the last non-empty `description` and write it
back with `loom data update <ID> --workspace <WS> --description-from-file <path>`.
The `.bak` sibling snapshot is only one write behind, so it is usually **already
post-clobber** — use the event stream, not the backup.

### Map registry

<!-- one row per wayfinder map, so a session finds it without discovery -->

| Map | Workspace | Frontier query |
|---|---|---|
| `LOOMCLI-169` — Resumable lead conversations across backends | `LOOMCLI` (local) | `loom data ready --workspace LOOMCLI --parent LOOMCLI-169` |
