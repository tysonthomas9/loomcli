# Workspace Provider Refactor — Router Loader + Layered Providers

## Problem

Switching workspaces from the terminal view leaves the UI frozen on the old
workspace: URL updates to `/ws/{new-id}/` but the sidebar, terminal tabs,
agents, and session list all still reflect the previous workspace. Kanban view
does not have this bug. Confirmed on aether-dev via agent-browser.

## Root Cause

`WorkspaceProvider` (`internal/webui/frontend/src/hooks/useWorkspaceContext.tsx`)
derives state from its `workspaceId` prop by stuffing the derived values into
`useState` and syncing them via `useEffect`. When the URL changes, the prop
updates but the effect-synced state is stale for one render cycle — longer when
the underlying `useWorkspace` SWR-style hook continues returning the
previously-fetched data while refetching.

This directly violates the Vercel React rule
[`rerender-derived-state-no-effect`](../../.claude/skills/react-best-practices/rules/rerender-derived-state-no-effect.md):

> Do not set state in effects solely in response to prop changes; prefer
> derived values or keyed resets instead.

Compounding that, `WorkspaceLayout.tsx:30-80` runs its own async
`fetchWorkspace(workspaceId)` validation inside a `useEffect` and returns `null`
while in flight — giving the component two failure modes for the same URL
("new params with stale data" and "new params with no data"). The right place
for pre-render data loading is a React Router loader, not a component effect.

Finally, `WorkspaceProvider` mixes four concerns with four distinct lifetimes:

1. **Loader data** (workspace name, id, repos, agents) — should be derived,
   never stateful
2. **Per-workspace preferences** (`selectedRepoNames`) — must reset on switch
3. **Cross-workspace preferences** (`defaultWorkspaceName`) — must persist across
   switches
4. **Data fetching / polling** — should be router-driven, not hand-rolled

Stuffing all four into one provider means you cannot key any one of them
without breaking the others. The composition-patterns skill rules
(`state-decouple-implementation`, `state-lift-state`,
`state-context-interface`) all converge on the same answer: **split by
lifetime.**

## Target Architecture

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

## Why Not the Alternatives

| Approach | Fixes root cause | Fast switches | Preserves tabs | Survives new state |
|---|---|---|---|---|
| `flushSync: true` everywhere | ❌ symptom only | ✅ | ✅ | ❌ |
| `key={workspaceId}` on layout | ⚠️ by nuking | ❌ full remount | ❌ | ✅ (by nuking) |
| External store via `useSyncExternalStore` | ❌ wrong layer | ✅ | ✅ | ❌ |
| **Loader + layered providers** | ✅ | ✅ | ✅ | ✅ |

- `flushSync` is the symptom-layer escape hatch the codebase already uses in
  `useRouteView.ts`. It works but does not prevent the next developer from
  hitting the same class of bug when they add workspace-scoped state.
- `key={workspaceId}` on `<WorkspaceLayout>` unmounts the entire tree
  including TerminalView, destroying terminal tab memory and re-establishing
  every WebSocket on every switch. Unacceptable UX cost.
- External store rewrites the wrong layer — the bug is provider staleness,
  not WebSocket render pressure. The `setTabUnread`/`setShowNewOutputPill`
  bailouts already limit re-render frequency to once per tab per session
  transition.
- Router loaders + layered providers is the only option that respects
  React's data model, the skill rules, and the terminal's UX constraints
  simultaneously.

## Rules Applied (from `~/.claude/skills/`)

- **`rerender-derived-state-no-effect`** (react-best-practices) — the primary
  rule. Derive, don't effect-sync.
- **`rerender-split-combined-hooks`** — split `useWorkspace`'s combined
  "initial fetch + polling + visibility refetch" effect.
- **`rerender-lazy-state-init`** — `selectedRepoNames` initializer reads
  localStorage; use the function form of `useState`.
- **`rerender-move-effect-to-event`** — `invalidateWorkspaceCache` in
  navigation events (already correct), not in effects.
- **`async-suspense-boundaries`** — router loader + Suspense instead of
  `useEffect(fetchWorkspace)` + `null` return.
- **`state-decouple-implementation`** (composition-patterns) — provider is
  the only place that knows how state is managed; UI consumes an interface
  unchanged across the refactor.
- **`state-lift-state`** — lift per-workspace state into its own provider.
- **`state-context-interface`** — keep the public `useWorkspaceContext()` API
  stable so no UI consumer needs to change.

## Ordered Implementation Plan

### PR #1 — Architectural fix (Steps 1–4)

**Step 1. Add the workspace loader.**
- `internal/webui/frontend/src/router.tsx:57` — attach
  `loader: workspaceLoader` to the `/ws/:workspaceId` route.
- Loader calls `fetchWorkspace(params.workspaceId!)` and returns `{ workspace }`.
- `throw redirect("/")` on 404; `throw redirect("/")` with
  `clearLastWorkspaceId` call on stale-localStorage 404 (mirror current
  behavior in `WorkspaceLayout.tsx:56-66`).

**Step 2. Rewrite `WorkspaceLayout.tsx`.**
- Delete `useEffect(fetchWorkspace)`, `validating`, `valid` state, and the
  `null`-return gate (lines 33-80).
- Use `const { workspace } = useLoaderData<typeof workspaceLoader>()`.
- Optionally render a subtle loading indicator driven by
  `useNavigation().state === "loading"` — React Router keeps the old UI
  visible during loader transitions, which is the intended UX.

**Step 3. Split `WorkspaceProvider` into three providers.**
- New `WorkspaceDataProvider`: takes `workspace` prop, exposes
  `workspace`, `workspaceId`, `activeWorkspaceName`, `repos`, `groups`,
  `agents`, `getRepoByName`, `getReposByGroup`, `getAgentByName`. All derived.
  Zero `useState`, zero effects.
- New `PerWorkspacePrefsProvider`: takes `workspaceId`, mounted with
  `key={workspaceId}`. Holds `selectedRepoNames` (lazy `useState` initialized
  from `wsGet(workspaceId, SK_SELECTED_REPOS)`). Exposes `selectRepos`,
  `selectAll`, `toggleRepo`, `isAllSelected`, `activeRepos`,
  `activeRepoNames`, `sourceReposFilter`, `isMultiRepo`.
- New `GlobalPrefsProvider`: holds `defaultWorkspaceName`,
  `setDefaultWorkspace`. Never keyed.
- Public `useWorkspaceContext()` hook stays. It reads from all three
  providers internally and returns the same `WorkspaceContextValue` shape —
  no UI consumer needs to change.

**Step 4. Split `useWorkspace` polling.**
- Delete the initial-fetch branch — the loader owns it.
- Extract `useWorkspacePolling(workspaceId, pollInterval)`: one effect, calls
  `useRevalidator().revalidate()` on interval.
- Extract `useVisibilityRevalidate()`: one effect, calls `revalidate()` on
  `visibilitychange`.
- Both hooks mount inside `WorkspaceLayout` post-loader.

### PR #2 — Cleanup and consolidation (Steps 5–8)

**Step 5. Bridge non-route callers via `useRevalidator`.**
- `OtherWorkspacesSection.tsx:80`, `CreateWorkspaceModal` flow, and the
  provider's `setDefaultWorkspace` currently call `refreshWorkspace()`
  directly. Replace with `useRevalidator().revalidate()` so the loader is
  the single source of truth.

**Step 6. Preserve `view=` on workspace switch.**
- Build a tiny helper `buildWorkspaceSwitchUrl(id, currentSearch)` that
  constructs `/ws/${id}/` and conditionally re-attaches `view=` from current
  params. Whitelist `view=` only — do not preserve `issue=`, `repo=`, or
  filter params (they leak stale IDs into the new workspace).
- Apply at the 5 call sites:
  - `useWorkspaceContext.tsx:236` (setActiveWorkspace)
  - `App.tsx:641, 888, 900, 1350`
  - `RedirectToWorkspace.tsx` (4 sites)

**Step 7. Reassess `flushSync` in `useRouteView.ts`.**
- With the loader pattern, route transitions commit atomically regardless of
  terminal render pressure. The `flushSync: true` may become dead defense.
- Try deleting it. If the bug returns, leave it with a comment:
  "belt-and-suspenders after loader refactor, not load-bearing."

**Step 8. Update tests.**
- Every test that mounts `WorkspaceProvider` directly (≈10 files) must now
  provide mock loader data.
- Export a `TestWorkspaceProviders` helper that wraps children in all three
  providers with literal mock data — one place to update if the shape
  changes.

## What Gets Deleted

- `WorkspaceLayout.tsx:33-80` — validating/valid gate
- `useWorkspaceContext.tsx:166-186` — `activeWorkspaceName` useState + sync effect
- `useWorkspaceContext.tsx:218-223` — `defaultWorkspaceName` sync effect
- `useWorkspace.ts` — initial-fetch branch + visibility handler (loader + revalidator replace them)
- `useRouteView.ts`'s `flushSync: true` (after Step 7 verification)

## What Stays Unchanged

- `useWorkspaceTabState.ts` — already correct; watches workspace name for
  changes and saves/restores tab state
- `invalidateWorkspaceCache()` — still called from navigation events
- `WorkspaceContextValue` shape — public API unchanged
- All UI consumers of `useWorkspaceContext()`

## Tradeoffs

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

After PR #1:
1. Reproduce the bug on a local dev build via agent-browser:
   `/ws/A/?view=terminal` → click switcher → select ws B → confirm sidebar,
   tabs, agents all update to ws B. URL preserves `?view=terminal`.
2. Confirm terminal WebSockets do NOT reconnect on switch (no WS close+open
   pair in network tab).
3. Confirm `useWorkspaceTabState` restores tab state when switching back to
   a workspace visited earlier in the session.
4. Run the existing frontend test suite; update test fixtures per Step 8
   in PR #2.

After PR #2:
1. Re-verify Step 7: if `flushSync` was removed from `useRouteView`, confirm
   view switching in terminal view still works under heavy WebSocket load.
2. Confirm non-route refreshers (create-workspace flow) still end up with
   fresh data after completion.

## References

- Rule files under `~/.claude/skills/react-best-practices/rules/`:
  `rerender-derived-state-no-effect.md`, `rerender-split-combined-hooks.md`,
  `rerender-lazy-state-init.md`, `rerender-move-effect-to-event.md`,
  `async-suspense-boundaries.md`
- Rule files under `~/.claude/skills/composition-patterns/rules/`:
  `state-decouple-implementation.md`, `state-lift-state.md`,
  `state-context-interface.md`
- React Router v7 loader docs: https://reactrouter.com/en/main/route/loader
- React "You Might Not Need an Effect": https://react.dev/learn/you-might-not-need-an-effect
