# Plan: Give Leads Direct Access to the Shared Browser

## Goal

Let a Loom lead use the installed `agent-browser` CLI to operate a browser that
is already open in the Loom desktop view. The lead discovers the live connection
and open tabs with `loom browser state`; browser actions remain ordinary
`agent-browser` commands. The human can continue using the visible browser and
its existing annotation UI.

This replaces the earlier broker and MCP plan for the current POC. It is a
small connection and discovery feature, not a production browser security
model.

## Lead Workflow

1. The lead runs `loom browser state --json`.
2. Loom reports each open browser identity, its local CDP port, and its live
   page tabs. The result identifies the browser selected in the desktop view
   and the page currently shown there.
3. The lead chooses a tab by its CDP target ID and uses `agent-browser` with
   that browser's port and a dedicated session name.
4. Before acting after a tab switch or user intervention, the lead runs
   `loom browser state` again and takes a fresh `agent-browser` snapshot.

Example shape (illustrative IDs and ports):

```json
{
  "runtimeId": "poc-run-123",
  "browsers": [
    {
      "id": "app-a",
      "name": "Research",
      "selectedInLoom": true,
      "status": "ready",
      "cdpPort": 61222,
      "selectedTargetId": "A1B2",
      "tabs": [
        { "targetId": "A1B2", "title": "Example", "url": "https://example.com", "selected": true }
      ]
    }
  ]
}
```

The lead's instructions can show the command pattern without embedding a
particular port or target ID:

```sh
loom browser state --json
agent-browser --session <unique-tab-session> --cdp <cdpPort> tab <targetId>
agent-browser --session <unique-tab-session> --cdp <cdpPort> --pin-tab snapshot -i
agent-browser --session <unique-tab-session> --cdp <cdpPort> --pin-tab click @e1
```

The session name is derived from `runtimeId`, browser ID, and target ID, then
reused for subsequent commands on that tab. `--pin-tab` makes a closed tab
fail visibly instead of silently switching to another tab. `agent-browser`
0.37.1 accepts CDP target IDs as tab references and supports `--pin-tab`.

## Minimal Changes

### 1. Report live browser state

Extend the POC control server's browser discovery. It currently returns a
registry of browser apps at `GET /api/browsers` and one visible target at
`GET /api/status/{appId}`. Add one read-only state response that queries live
CDP page targets for each browser and includes runtime ID, URL, title, target
ID, and the page currently shown in Loom. A stopped browser appears as
`unavailable` with an error summary; it does not make the other browsers
disappear.

The POC UI currently keeps its selected browser only in `src/main.ts`; publish
that selection to the control server when it changes. Treat tab and connection
details as live state, not a cached startup list. Tabs opened by either the
human or `agent-browser` must appear on the next state request.

Verification: two browser identities and multiple tabs appear with distinct
CDP ports and target IDs; switching the visible browser updates
`selectedInLoom`; closing a tab removes it from the next response.

### 2. Add `loom browser state`

Register a read-only `browser state` subcommand in Loom's Cobra CLI. It queries
the local POC control origin supplied to the lead runtime and prints a concise
table by default or the structured response with `--json`. It must not invent
connection values from static defaults. If no browser runtime is attached,
return a clear error and a nonzero exit status.

For this POC, the lead runtime receives the control origin as an environment
value. The command uses the live control server as its source of truth. It does
not create browsers, select tabs, or invoke `agent-browser`.

Verification: the command reports the same identities, ports, and pages as the
desktop view; `--json` remains machine readable; unavailable browsers are
represented without hiding healthy ones.

### 3. Expose the workflow to the lead

Ensure `agent-browser` is available on the lead's `PATH` and the lead can
reach the returned loopback CDP port. Add a short lead instruction that says to
run `loom browser state` before using `agent-browser`, select a target ID, and
use a session unique to that tab. Do not put a copyable connection command in
the desktop UI; the lead can discover the current value itself.

Verification in a real lead terminal: run `loom browser state`, bind
`agent-browser` to the page the human sees, take a snapshot, click a harmless
fixture button, and see the result in the desktop view. Repeat with a second
browser identity and a newly opened tab to prove there is no accidental
cross-browser or cross-tab control.

## Current POC Boundary

Direct `agent-browser` commands bypass the POC's queued-action epoch check.
Human interaction remains enabled, but this simple path cannot guarantee that
a concurrent agent action will be cancelled or rejected after human input.
The lead should refresh state and snapshot after a user intervention. The
existing annotation UI continues to capture DOM context and screenshots;
automatic delivery of those annotations into a lead conversation is separate
work.

The CDP port grants broad control of the local browser. Keep this path in the
trusted local POC environment and expose only the task-owned loopback ports.
If the product later needs strong multi-user permissions or automatic human
preemption, revisit a controller then, based on the observed workflow.

## Done When

A lead can discover every open Loom browser and tab with one command, attach
`agent-browser` to the tab visible to the human, act on it, and repeat after a
tab or browser switch. The live desktop view shows those actions. The command
reports stale or unavailable connections clearly, and the known concurrency
limitation is documented in the lead instructions.
