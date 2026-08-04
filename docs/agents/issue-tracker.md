# Issue tracker: loom data (fleet-db)

> **Status:** Current · *audited 2026-07-23*

Issues for this repo live in fleet-db, driven by the `loom data` CLI (see
`AGENTS.md`). There are two fleet-db backends and they are **not**
interchangeable:

- **Server backend** — the real workspaces (e.g. `SF-RC1`) on a running loom
  server or the aether-dev deployment (a host name, not the design system —
  see [../loom-glossary.md](../loom-glossary.md)). Reached with `--server <url>`
  or `LOOM_SERVER_URL`. This is where product work lives, and where daemon
  agents poll for claimable tasks.
- **Local backend** — an embedded fleet-db backed by
  `~/.loom/fleet-db/redis-snapshot.json` on this machine (local mode; see
  [../loom-glossary.md](../loom-glossary.md)). Reached by passing **no**
  `--server` and an explicit `--workspace`. A healthy embedded runtime is
  reused, not respawned: `openLocalStore` opens an HTTP client against the
  already-running embedded process rather than booting a new one
  (`internal/bootstrap/openstore.go:122-160`; `bootstrap.reuseEmbeddedRuntime`
  checks pid/redis-config/healthz, `internal/bootstrap/embedded.go:183-211`), so
  overlapping `loom data` invocations share one process instead of each
  rewriting the snapshot. A daemon *can* poll this backend — see the
  precondition under [Wayfinding operations](#wayfinding-operations).

Build or locate the CLI first: `go build -o loom ./cmd/loom` (or use an
installed `loom`). Commands below assume `loom` on `PATH`.

## Conventions

- **Create**: `loom data create --workspace <WS> --type <task|bug|feature|epic|chore> --title "..." --description "..." --priority <0-4>` (`internal/cli/data/create.go:90`)
- **Read**: `loom data show <ID> --workspace <WS> -o json`
- **List**: `loom data list --workspace <WS> [--parent <ID>] [--status <s>] -o json`
- **Comment**: `loom data comment <ID> --workspace <WS> "..."`
- **Close**: `loom data close <ID> --workspace <WS> --reason "..."`
- **Update fields**: `loom data update <ID> --workspace <WS> [--status ...] [--assignee ...] [--design ...] [--design-format <markdown|html>] [--title ...] [--priority ...] [--notes ...] [--description ... | --description-from-file <path|->] [--depends-on <ID>] [--remove-depends-on <ID>]`
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
*build* it. The local embedded fleet-db is **not** immune to this by nature: a
daemon defaults its issue backend to `fleetdb`
(`internal/cli/config/project.go:226-228`) and, with `LOOM_FLEET_DB_URL` unset,
opens the same local embedded store through `cmdstore.OpenStore`
(`internal/cli/daemon/daemon_cmd.go:320,330`; local mode selected by
`bootstrap.DetectMode`, `internal/bootstrap/mode.go:54-58`), then polls it for
ready tasks (`internal/cli/daemon/daemon.go:293-302`). So a daemon bound to
`LOOMCLI` *would* claim wayfinder tickets. The
"open + unassigned = unclaimed frontier" convention therefore holds only as an
**operational precondition: no daemon may be running against `LOOMCLI`.** Verify
before charting — `loom daemon status`
(`internal/cli/daemon/daemon_cmd.go:96`), or check for a live pid in the daemon
PID file (`.loom/daemon.pid` by default, `internal/cli/config/project.go:197`).
Always pass `--workspace LOOMCLI` and no `--server`.

- **Map**: `loom data create --workspace LOOMCLI --type epic --label wayfinder:map --title "... (wayfinder map)"`. The Destination / Notes / Decisions-so-far / Fog body is the issue description.
- **Child ticket**: `loom data create --workspace LOOMCLI --type task --parent <MAP-ID> --label wayfinder:<type> --title "..."`, where `<type>` is `research`/`prototype`/`grilling`/`task`. Wire blocking in a second pass with `loom data update <child> --workspace LOOMCLI --depends-on <blocker>` (issues need ids before they can reference each other). Labels, by contrast, must be set at create time — see Conventions.
- **Frontier query**: `loom data ready --workspace LOOMCLI --parent <MAP-ID>`. The `--parent` scope is **required** — a bare `loom data ready` excludes children of an open epic, so the map's tickets do not appear without it. `ready` already drops blocked and claimed (assignee-set) tickets; take the first in id order.
  - **Gotcha: `ready` does not return the `assignee` field.** Its rows carry `owner` (the creator — typically set on *every* ticket, so it is NOT the claim) but no `assignee`. You therefore cannot tell claimed-ness from `ready` output; use `loom data list --parent <MAP-ID>` or `show <ID>` to see `assignee`. Auditing a map's claims with `ready` alone will silently mislead. *Observed at runtime, not traced to source* — the CLI layer is not where the field is dropped (`ready` accepts an `--assignee` filter at `internal/cli/data/ready.go:44` and the shared formatter prints both Assignee and Owner at `internal/cli/data/format.go:39-43`), so the omission comes from the backend's ready-view projection. Treat as empirical until someone pins it to the fleet-db view.
  - **Watch for stale claims.** A ticket assigned during charting and never worked stays off the frontier indefinitely. Audit periodically with `list` and clear with `loom data update <ID> --workspace LOOMCLI --assignee ""`.
  - Overlapping `loom data` invocations do **not** each boot their own embedded fleet-db, and the earlier `lock … is held` non-zero-exit hazard no longer applies: a healthy runtime is reused over HTTP, and an invocation that arrives while another is mid-startup **waits** for that shared process to become healthy instead of failing. `openLocalStore` routes the `embedded fleet-db already running` lock error (`internal/bootstrap/embedded.go:46,89`) into `waitAndOpenLocalStore` (`internal/bootstrap/openstore.go:130-131,162-174`), which retries `reuseEmbeddedRuntime` until `/healthz` answers (`internal/bootstrap/embedded.go:214-237`). So there is no lock-collision exit or independent per-invocation snapshot rewrite to code around here.
  - The snapshot remains shared mutable state on a single process. Its durability limits — not committed, not backed up, plus recovery via the Redis event stream — are covered under **Durability caveat** below. If a batch of mutations matters, re-read afterward rather than trusting a printed `updated`.
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
