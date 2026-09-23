# Plan: Give Leads Direct Access to the Shared Browser

## Goal

Let a Loom lead create and manage browser identities with `loom browser`, then
use the installed `agent-browser` CLI to operate their tabs and pages. The lead
discovers live connections and open tabs with `loom browser state`. The human
can continue using the visible browser and its existing annotation UI.

This replaces the earlier broker and MCP plan for the current POC. It is a
small browser lifecycle and connection feature, not a production browser
security model.

## Operations and Ownership

Loom owns isolated browser identities. `agent-browser` owns tab and page
actions after the lead connects to one of those identities.

| Object | Create | Read | Update | Delete |
|---|---|---|---|---|
| Browser identity | `loom browser create --name Research` | `loom browser state [id] --json` | `loom browser update <id> --name Research --select` | `loom browser delete <id>` |
| Tab/page | `agent-browser tab new [url]` | `loom browser state` or `agent-browser tab list` | `agent-browser tab <targetId>` to select; `agent-browser open <url>` to navigate | `agent-browser tab close <targetId>` |

`loom browser update` accepts either `--name`, `--select`, or both. `--select`
changes the browser shown in Loom; renaming changes its label in the UI and
state output. `loom browser delete` closes exactly that browser identity and
its tabs and releases its task-owned container and ports, including that
browser's temporary profile and sign-in state. It does not stop the other
browser identities or the shared VM. Deleting the selected browser moves the
UI to another available browser, or to an empty state if none remain.

`loom browser create` opens a new browser tab under the owning lead in the real
Loom desktop UI. When that lead is on screen, Loom activates the Browser panel
and selects the new tab automatically. The tab appears as **Starting…** when
provisioning begins; when ready, it shows the browser's first page and accepts
human input and annotations. If another lead is on screen, the new tab waits
under its owner without changing the user's current view. This is the outer
Loom browser tab for an isolated browser identity. Page tabs inside it remain
visible through browser state and `agent-browser`.

The browser commands use the same runtime identity and ownership checks as the
POC. They return the browser ID and current state so the lead can immediately
read the connection details. A request for an unknown or already deleted ID
returns a clear error; deletion never guesses a container from a port alone.

## Lead Workflow

1. The lead runs `loom browser state --json`, or first runs
   `loom browser create --name Research` when a new isolated browser is needed.
   Creation immediately opens its tab under that lead and selects it when the
   user is viewing the lead.
2. Loom reports each open browser identity, its local CDP port, and its live
   page tabs. The result identifies the browser selected in the desktop view
   and the page currently shown there.
3. The lead chooses a tab by its CDP target ID and uses `agent-browser` with
   that browser's port and a dedicated session name.
4. The lead can rename, select, or delete browser identities with
   `loom browser update` and `loom browser delete`; it uses `agent-browser tab`
   for tab creation and closure.
5. Before acting after a tab switch or user intervention, the lead runs
   `loom browser state` again and takes a fresh `agent-browser` snapshot.

Example shape (illustrative IDs and ports):

```json
{
  "runtimeId": "poc-run-123",
  "browsers": [
    {
      "id": "app-a",
      "name": "Research",
      "workspace": "CRITICAL-BUGS",
      "leadName": "Claude",
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
loom browser create --name Research
loom browser state --json
agent-browser --session <unique-tab-session> --cdp <cdpPort> tab <targetId>
agent-browser --session <unique-tab-session> --cdp <cdpPort> --pin-tab snapshot -i
agent-browser --session <unique-tab-session> --cdp <cdpPort> --pin-tab click @e1
loom browser update <browserId> --name "Research notes" --select
loom browser delete <browserId>
```

The session name is derived from `runtimeId`, browser ID, and target ID, then
reused for subsequent commands on that tab. `--pin-tab` makes a closed tab
fail visibly instead of silently switching to another tab. `agent-browser`
0.37.1 accepts CDP target IDs as tab references and supports `--pin-tab`.

## Minimal Changes

### 1. Complete browser lifecycle operations

The POC already creates browsers through `POST /api/browsers`. Extend the
control server with a single-browser update and delete operation. Add a
`runtime.sh remove <app-id>` path that first verifies the prototype label,
runtime ID, and browser ID, then removes only that owned container. Keep the
browser registry, names, and selection in task-owned runtime metadata so a
control-server restart does not resurrect a deleted browser or lose a rename.
Creation should reuse a free browser slot after deletion, and a failed create
should clean up only the container it attempted to start.

Associate every browser with the workspace and lead that created it. The
controlled lead runtime supplies that identity to `loom browser create` and
`state`; UI queries use the selected lead. A browser created by Claude must
not appear in Codex's browser list.

Record a newly requested browser as `starting` before waiting for its container
to become ready. Mark it `ready` when CDP responds, or `failed` with a visible
reason if provisioning fails. Creating a browser also sets it as the selected
browser. A later readiness update must not override a newer user selection.
A failed entry can be deleted without affecting any other browser.

Verification: create two browsers, rename and select one, then delete only that
one. The other stays usable; its CDP endpoint and tabs remain unchanged. After
a control-server restart, the deleted browser stays absent and the renamed
browser retains its name. Invalid IDs and ownership mismatches fail without
removing anything.

### 2. Report live browser state

The control server currently returns a browser registry at `GET /api/browsers`
and one visible target at `GET /api/status/{appId}`. Add one read-only state
response that queries live CDP page targets for each browser and includes
runtime ID, URL, title, target ID, and the page currently shown in Loom. A
stopped browser appears as `unavailable` with an error summary; it does not
make the other browsers disappear.

The POC UI currently keeps its selected browser only in `src/main.ts`; keep
that selection synchronized with the control server and the `--select` update
operation. Treat tab and connection details as live state, not a cached startup
list. Tabs opened with `agent-browser` must appear on the next state request.

### 3. Open the browser in the real lead view

The UI currently discovers browser apps only at startup or after its own
**New browser** click in the standalone prototype. The real lead view uses
`AgentEditorGroups` for its Terminal/Info/Git/Diff/Files tabs and `AgentsPage`
to render each pane. Add a Browser pane there for leads, with a tab for each
browser identity owned by that lead. Poll the lightweight browser registry
once per second while that view is open so a create issued from a lead terminal
adds and selects the new browser tab without a reload. Reconcile by browser ID
to avoid duplicates. Let `AgentsPage` request activation of the existing
Browser editor tab in `AgentEditorGroups`, including when that tab is in a split
group. Render the POC's proven live frame, human input, and annotation surface
inside the selected browser tab.
Show `starting`, `ready`, and `failed` states in the tab; once ready, display
the first live frame. On lead-created browser discovery, activate the Browser
pane if the owning lead is selected. Rename and delete operations must update
and remove the same tab. If the human selects another browser while creation
is underway, keep that newer choice when the new browser becomes ready.

Verification: two browser identities and multiple tabs appear with distinct
CDP ports and target IDs; switching the visible browser updates
`selectedInLoom`; closing a page tab removes it from the next response. A lead
create opens a selected **Starting…** tab in that lead's already open Loom
view, then shows the live page when ready. A failed create stays visible with
an error and does not leave a false ready tab. Another lead's view remains
unchanged.

### 4. Add `loom browser` commands

Register `browser create`, `browser state`, `browser update`, and
`browser delete` in Loom's Cobra CLI. They call the POC control server;
`state` prints a concise table by default or a structured response with
`--json`. All four commands accept `--json` for machine-readable results.
They must not invent connection values from static defaults. If no browser
runtime is attached, return a clear error and a nonzero exit status.

For this POC, the lead runtime receives the control origin as an environment
value. The commands use the live control server as their source of truth. Loom
does not duplicate tab navigation or page actions from `agent-browser`.

Verification: the commands create, read, update, and delete the same browser
identities shown in the desktop view; `--json` remains machine readable;
unavailable browsers are represented without hiding healthy ones.

### 5. Expose the workflow to the lead

Ensure `agent-browser` is available on the lead's `PATH` and the lead can
reach the returned loopback CDP port. Add a short lead instruction that says to
run `loom browser state` before using `agent-browser`, select a target ID, and
use a session unique to that tab. Do not put a copyable connection command in
the desktop UI; the lead can discover the current value itself.

Verification in a real lead terminal: create a browser, run
`loom browser state`, bind `agent-browser` to the page the human sees, take a
snapshot, click a harmless fixture button, and see the result in the desktop
view. Rename the browser, open a second tab, and delete the browser while
another identity stays usable. Prove there is no accidental cross-browser or
cross-tab control. Keep the desktop view open throughout, confirming that
creation, loading, rename, selection, and deletion appear without a reload.

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

A lead can create, inspect, rename/select, and delete an isolated Loom browser
from its terminal. Create immediately opens a browser tab under that lead and
selects it when the lead is on screen; the live page appears when ready.
`loom browser state` discovers every
open browser and tab; `agent-browser` attaches to the tab visible to the human
and performs page and tab actions. The desktop view reflects those operations.
Deleting one browser never affects another, stale or unavailable connections
report clearly, and the known concurrency limitation is documented in the lead
instructions.
