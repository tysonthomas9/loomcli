# Workspace Provider Refactor — Router Loader + Layered Providers

> **Status:** Implemented differently — shipped in `fe290c01b` (2026-04-12)
> as a provider split + `flushSync`, **not** as the router-loader design
> below. Read Problem / Root Cause / Why Not the Alternatives for context;
> the Ordered Implementation Plan is historical and is annotated step by
> step with what actually landed. *audited 2026-07-23*
>
> `internal/webui/frontend/src/hooks/workspace/useWorkspaceContext.tsx:5`
> cites this file by path, which is why it survives.

## Problem

Switching workspaces from the terminal view leaves the UI frozen on the old
workspace: URL updates to `/ws/{new-id}/` but the sidebar, terminal tabs,
agents, and session list all still reflect the previous workspace. Kanban view
does not have this bug. Confirmed on aether-dev via agent-browser.

## Root Cause

**Historical.** At the time of writing, `WorkspaceProvider` derived state from
its `workspaceId` prop by stuffing the derived values into `useState` and
syncing them via `useEffect`. When the URL changed, the prop updated but the
effect-synced state was stale for one render cycle — longer when the
underlying `useWorkspace` SWR-style hook kept returning previously-fetched
data while refetching.

That implementation no longer exists. The provider now lives at
`internal/webui/frontend/src/hooks/workspace/useWorkspaceContext.tsx` and its
header comment (`:7-12`) records the current design: workspace data is owned
by a per-provider zustand store created at `useWorkspaceContext.tsx:149`
(`createWorkspaceStore()`), and all workspace metadata is derived from store
state via `useStore` selectors — no `useState`/`useEffect` mirroring.

The rule this violated is the Vercel React best-practice rule
`rerender-derived-state-no-effect`:

> Do not set state in effects solely in response to prop changes; prefer
> derived values or keyed resets instead.

(The rule lives in a machine-local skill directory, not in this repo, so the
quote above is the durable part. There is no `react-best-practices` skill
under the repo's `.claude/skills/`.)

Compounding that, `WorkspaceLayout.tsx` runs its own async
`fetchWorkspace(workspaceId)` validation inside a `useEffect` and returns `null`
while in flight — giving the component two failure modes for the same URL
("new params with stale data" and "new params with no data"). The right place
for pre-render data loading is a React Router loader, not a component effect.

**Still true.** That gate was never removed:
`internal/webui/frontend/src/components/WorkspaceLayout/WorkspaceLayout.tsx:30-31`
declares `validating` / `valid`, `:33` opens the validating `useEffect`, and
`:83` is the `if (validating || !valid || !workspaceId) return null;` gate.

Finally, `WorkspaceProvider` mixes four concerns with four distinct lifetimes:

1. **Loader data** (workspace name, id, repos, agents) — should be derived,
   never stateful
2. **Per-workspace preferences** (`selectedRepoNames`) — must reset on switch
3. **Cross-workspace preferences** (`defaultWorkspaceName`) — must persist across
   switches
4. **Data fetching / polling** — should be router-driven, not hand-rolled

Stuffing all four into one provider means you cannot key any one of them
without breaking the others. The composition-patterns rules
(`state-decouple-implementation`, `state-lift-state`,
`state-context-interface`) all converge on the same answer: **split by
lifetime.**

Concern 3 is now moot: `setDefaultWorkspace` throws
("Default workspace selection has been removed",
`useWorkspaceContext.tsx:185-187`).

## Target Architecture (proposed — not what shipped)

```
router loader (fetches workspace atomically with route param change)
  └─ <WorkspaceDataProvider value={useLoaderData().workspace}>
       └─ <PerWorkspacePrefsProvider key={workspace.id}>
            └─ <GlobalPrefsProvider>
                 └─ <Outlet />   (App, TerminalView, etc.)
```

Each provider has the correct lifetime:

- **`WorkspaceDataProvider`** — zero `useState`, zero effects. All derived from
  loader data. Satisfies `rerender-derived-state-no-effect` by construction.
  No staleness possible.

- **`PerWorkspacePrefsProvider`** — the *only* provider keyed by
  `workspace.id`. Holds exactly the state that must reset on workspace change.
  Does NOT contain TerminalView, WebSocket tree, or any expensive subtree —
  so the keyed remount is cheap (resets a `Set`, re-reads localStorage).

- **`GlobalPrefsProvider`** — cross-workspace state. Never resets.

- **Router loader** — React Router guarantees the route component never
  renders with a new `workspaceId` until `fetchWorkspace` has resolved. The
  "new id, old data" window stops existing.

TerminalView lives outside the keyed boundary, so WebSockets and the
`useWorkspaceTabState` ref-map survive across workspace switches as intended.

**What shipped instead:** only the middle layer. `PerWorkspacePrefsProvider`
exists (`hooks/workspace/PerWorkspacePrefsProvider.tsx:71`) and is mounted
keyed on `workspaceId` at `useWorkspaceContext.tsx:288-294`. There is no
loader, no `WorkspaceDataProvider`, and no `GlobalPrefsProvider`; the
derived-data role stayed inside `WorkspaceProvider`, backed by the zustand
store instead of the loader. The staleness that the loader was meant to
eliminate is instead handled by `flushSync` — see Step 7.

## Why Not the Alternatives

| Approach | Fixes root cause | Fast switches | Preserves tabs | Survives new state |
|---|---|---|---|---|
| `flushSync: true` everywhere | ❌ symptom only *(verdict overturned — see below)* | ✅ | ✅ | ❌ |
| `key={workspaceId}` on layout | ⚠️ by nuking | ❌ full remount | ❌ | ✅ (by nuking) |
| External store via `useSyncExternalStore` | ❌ wrong layer *(verdict overturned — a zustand store is what shipped)* | ✅ | ✅ | ❌ |
| **Loader + layered providers** | ✅ | ✅ | ✅ | ✅ *(never built)* |

- `flushSync` was assessed here as "the symptom-layer escape hatch". The
  implementation reached the opposite conclusion. `useWorkspaceContext.tsx:193`
  now states "flushSync: true is REQUIRED here, not optional", and the
  surrounding comment (`:193-202`, mirrored at `:21-26`) gives the mechanism:
  React Router v7 wraps `navigate()` in `startTransition` by default; on the
  terminal view, renderer + WebSocket re-renders keep React's urgent-work
  queue saturated, the transition is deferred indefinitely, `history.pushState`
  updates the address bar but `useParams()` never commits, and
  `WorkspaceLayout` keeps reading the old `workspaceId`. `flushSync` forces a
  synchronous route commit. It is load-bearing, not belt-and-suspenders.
- `key={workspaceId}` on `<WorkspaceLayout>` unmounts the entire tree
  including TerminalView, destroying terminal tab memory and re-establishing
  every WebSocket on every switch. Unacceptable UX cost. (This assessment
  still holds; the keyed remount was narrowed to `PerWorkspacePrefsProvider`
  precisely to avoid it.)
- External store was rejected as "the wrong layer". A per-provider zustand
  store is nonetheless what the codebase settled on for workspace data
  (`useWorkspaceContext.tsx:52,149`).
- No evidence was found in the repo for *why* the loader approach was
  abandoned. Do not assume; ask.

## Rules Applied

These are rule names from machine-local skill packs (`react-best-practices`,
`composition-patterns`). They are not checked into this repo, so the names are
recorded without links.

- **`rerender-derived-state-no-effect`** (react-best-practices) — the primary
  rule. Derive, don't effect-sync. *Satisfied, via the zustand store rather
  than a loader; cited in `useWorkspaceContext.tsx:12`.*
- **`rerender-split-combined-hooks`** — split `useWorkspace`'s combined
  "initial fetch + polling + visibility refetch" effect. *Not done.*
- **`rerender-lazy-state-init`** — `selectedRepoNames` initializer reads
  localStorage; use the function form of `useState`.
- **`rerender-move-effect-to-event`** — `invalidateWorkspaceCache` in
  navigation events (already correct), not in effects.
- **`async-suspense-boundaries`** — router loader + Suspense instead of
  `useEffect(fetchWorkspace)` + `null` return. *Not done.*
- **`state-decouple-implementation`** (composition-patterns) — provider is
  the only place that knows how state is managed; UI consumes an interface
  unchanged across the refactor. *Satisfied; cited in
  `useWorkspaceContext.tsx:28-30`.*
- **`state-lift-state`** — lift per-workspace state into its own provider.
  *Satisfied.*
- **`state-context-interface`** — keep the public `useWorkspaceContext()` API
  stable so no UI consumer needs to change. *Satisfied.*

## Ordered Implementation Plan (historical)

Every path below has been corrected to its current location. The hooks moved
from `src/hooks/` to `src/hooks/workspace/`. Original line numbers that no
longer resolve have been dropped rather than guessed at.

### PR #1 — Architectural fix (Steps 1–4)

**Step 1. Add the workspace loader.** — **NOT DONE.**
- Proposed: attach `loader: workspaceLoader` to the `/ws/:workspaceId` route
  in `internal/webui/frontend/src/router.tsx`.
- Loader calls `fetchWorkspace(params.workspaceId!)` and returns `{ workspace }`.
- `throw redirect("/")` on 404, including the stale-localStorage case.
- Reality: `grep -rn 'workspaceLoader' internal/webui/frontend/src` returns
  nothing, and the `/ws/:workspaceId` route (`router.tsx:175-176`) has no
  `loader` key.

**Step 2. Rewrite `WorkspaceLayout.tsx`.** — **NOT DONE.**
- Proposed: delete `useEffect(fetchWorkspace)`, `validating`, `valid` state,
  and the `null`-return gate; read `useLoaderData()` instead.
- Reality: all of it is still there —
  `components/WorkspaceLayout/WorkspaceLayout.tsx:30-31`, `:33`, `:83`.

**Step 3. Split `WorkspaceProvider` into three providers.** — **PARTIALLY DONE.**
- `WorkspaceDataProvider` — **not created.** Its role lives on inside
  `WorkspaceProvider`, which derives everything from the zustand store
  (`useWorkspaceContext.tsx:149` creates it; `:213+` are the derived
  read-only values, explicitly "Zero useState").
- `PerWorkspacePrefsProvider` — **created**, at
  `hooks/workspace/PerWorkspacePrefsProvider.tsx:71`, taking
  `{ workspaceId, repos, children }` and mounted with `key={workspaceId}` at
  `useWorkspaceContext.tsx:288-294`.
- `GlobalPrefsProvider` — **not created**, and no longer needed:
  `defaultWorkspaceName` was removed from the product
  (`useWorkspaceContext.tsx:185-187` throws).
- Public `useWorkspaceContext()` hook — **kept**, and merges the two inner
  contexts into the unchanged `WorkspaceContextValue` shape.

**Step 4. Split `useWorkspace` polling.** — **NOT DONE.**
- Proposed: delete the initial-fetch branch (loader owns it), extract
  `useWorkspacePolling` and `useVisibilityRevalidate`.
- Reality: `hooks/workspace/useWorkspace.ts:87` is still one combined effect
  doing initial fetch, `setInterval` polling (`:166-172`), and the
  `visibilitychange` listener (`:182`, removed at `:190`). Neither extracted
  hook exists.

### PR #2 — Cleanup and consolidation (Steps 5–8)

**Step 5. Bridge non-route callers via `useRevalidator`.** — **VOID.**
- The premise was that `OtherWorkspacesSection`, the create-workspace flow,
  and `setDefaultWorkspace` call `refreshWorkspace()` directly.
  `refreshWorkspace` no longer exists anywhere under
  `internal/webui/frontend/src` (zero non-test hits), so there is nothing to
  bridge.

**Step 6. Preserve `view=` on workspace switch.** — **DONE.**
- `buildWorkspaceSwitchUrl(targetId, currentSearch)` shipped at
  `utils/workspaceUrl.ts:22`. It copies only whitelisted params
  (`PRESERVED_PARAMS`) from the current search into the new URL, dropping
  `issue=`, `repo=`, and filter params so stale IDs do not leak into the new
  workspace.
- Call sites: `hooks/workspace/useWorkspaceContext.tsx:206` (`setActiveWorkspace`)
  and `App.tsx:1112`, `:1125`, `:1503`.

**Step 7. Reassess `flushSync` in `useRouteView.ts`.** — **INVERTED.**
- Proposed: try deleting `flushSync: true` from `useRouteView.ts`; if the bug
  returns, keep it as non-load-bearing defense.
- Reality: `hooks/common/useRouteView.ts` has no `flushSync` at all — its
  `navigate()` calls at `:94` and `:104` pass `{ replace: true }` / nothing.
  `flushSync` moved *onto* the workspace-switch navigations and is documented
  as mandatory (`useWorkspaceContext.tsx:193`, `:206`; `App.tsx:1112,1125,1503`).

**Step 8. Update tests.** — **N/A as written.** There is no
`TestWorkspaceProviders` helper and no loader mock data, because there is no
loader. Tests live under `hooks/workspace/__tests__/`.

## What Gets Deleted (proposed — none of it was)

- `WorkspaceLayout.tsx` validating/valid gate — **still present** (`:30-31`, `:83`).
- `useWorkspaceContext.tsx` `activeWorkspaceName` useState + sync effect —
  gone, but by the zustand-store rewrite, not by this plan.
- `useWorkspaceContext.tsx` `defaultWorkspaceName` sync effect — gone, because
  the feature was removed.
- `useWorkspace.ts` initial-fetch branch + visibility handler — **still present**.
- `useRouteView.ts`'s `flushSync: true` — never existed there; see Step 7.

## What Stays Unchanged

- `useWorkspaceTabState.ts` — already correct; watches workspace name for
  changes and saves/restores tab state
- `invalidateWorkspaceCache()` — still called from navigation events
- `WorkspaceContextValue` shape — public API unchanged
- All UI consumers of `useWorkspaceContext()`

## Tradeoffs (of the proposed design)

1. **Refactor size**: PR #1 touches ~6-8 files. PR #2 touches ~8-10 files
   (mostly tests). Call it a focused day of work.
2. **Loader UX**: React Router keeps the *old* workspace visible during the
   loader fetch (usually <200ms) instead of showing a blank screen. Arguably
   better UX.
3. **Polling-as-revalidation**: `useRevalidator` re-runs the loader,
   re-fetching workspace metadata on every 60s tick. For loomcli's workload,
   identical cost to the current setup.
4. **`selectedRepoNames` across switches**: observable behavior unchanged —
   re-read from scoped localStorage on every switch, same as today.

## Validation

The manual repro that gated the shipped fix:

1. Reproduce the bug on a local dev build via agent-browser:
   `/ws/A/?view=terminal` → click switcher → select ws B → confirm sidebar,
   tabs, agents all update to ws B. URL preserves `?view=terminal`.
2. Confirm terminal WebSockets do NOT reconnect on switch (no WS close+open
   pair in network tab).
3. Confirm `useWorkspaceTabState` restores tab state when switching back to
   a workspace visited earlier in the session.
4. Run the existing frontend test suite.

## References

- Rule names from the machine-local `react-best-practices` skill:
  `rerender-derived-state-no-effect`, `rerender-split-combined-hooks`,
  `rerender-lazy-state-init`, `rerender-move-effect-to-event`,
  `async-suspense-boundaries`. Not checked into this repo.
- Rule names from the machine-local `composition-patterns` skill:
  `state-decouple-implementation`, `state-lift-state`,
  `state-context-interface`. Not checked into this repo.
- React Router v7 loader docs: https://reactrouter.com/en/main/route/loader
- React "You Might Not Need an Effect": https://react.dev/learn/you-might-not-need-an-effect

## Related

- `internal/webui/frontend/src/hooks/workspace/useWorkspaceContext.tsx` —
  the as-built provider; its header comment is the authoritative description.
- `docs/design/aether-wireframe-mapping.md` — web UI surface map.
