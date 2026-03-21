# Multi-Workspace Frontend Architecture (Epics 3wsub + rml7g)

## Overview

The multi-workspace frontend enables users to manage multiple workspaces, each containing multiple repos, from a single web UI. It provides workspace CRUD, per-repo agent grouping, scoped search, context preservation across workspace switches, and graceful degradation to single-repo mode.

The architecture is built on React context providers, URL-synced hooks, and a module-level workspace cache.

---

## 1. Core Context: WorkspaceProvider

**File:** `frontend/src/hooks/useWorkspaceContext.tsx`

The canonical source of workspace state for the entire component tree.

```typescript
interface WorkspaceContextValue {
  workspace: WorkspaceData | null;
  activeWorkspaceName: string | null;
  isMultiRepo: boolean;
  sourceReposFilter: string[] | undefined;
  selectedRepoNames: Set<string>;
  selectAll: () => void;
  selectRepos: (repos: string[]) => void;
  setActiveWorkspace: (name: string) => void;
  agents: WorkspaceAgentInfo[];
  repos: RepoInfo[];
  workspaces: WorkspaceSummary[];
  defaultWorkspaceName: string | null;
  setDefaultWorkspace: (name: string | null) => void;
}
```

### Key Derivations

- `isMultiRepo` = `repos.length >= 1` (gates workspace-specific UI)
- `sourceReposFilter` = derived from `selectedRepoNames`; `undefined` when all repos selected (no filter)
- `activeWorkspaceName` = from `localStorage["loom-active-workspace"]` or URL `?_ws=` param

### State Flow

```
WorkspaceProvider (context root)
  +-- useWorkspace()          -> polls GET /api/workspace every 60s
  +-- useWorkspaceRepos()     -> resilient fetch with backoff retry
  +-- selectedRepoNames       -> Set<string>, drives sourceReposFilter
  +-- activeWorkspaceName     -> localStorage + URL sync
```

All workspace-aware components consume `useWorkspaceContext()` rather than calling hooks directly, preventing duplicate polling.

---

## 2. Hook Architecture

| Hook | File | Purpose |
|------|------|---------|
| `useWorkspaceContext` | `hooks/useWorkspaceContext.tsx` | Canonical context provider + consumer |
| `useWorkspace` | `hooks/useWorkspace.ts` | 60s polling for GET /api/workspace |
| `useWorkspaceRepos` | `hooks/useWorkspaceRepos.ts` | Resilient fetch with exponential backoff and connection state |
| `useWorkspaceState` | `hooks/useWorkspaceState.ts` | Per-workspace UI snapshot save/restore on switch |
| `useWorkspaceTree` | `hooks/useWorkspaceTree.ts` | Epic/task tree builder (calls useIssues with sourceRepos) |
| `useWorkspaceParam` | `hooks/useWorkspaceParam.ts` | URL `?workspace=` sync |
| `useRepoFilter` | `hooks/useRepoFilter.ts` | URL `?repos=` sync (standalone) |
| `useViewState` | `hooks/useViewState.ts` | URL `?view=` sync with push/replace state |
| `useSearchScope` | `hooks/useSearchScope.ts` | Derives scope name from workspace context |

### Connection State Machine

`useWorkspaceRepos` tracks:

```typescript
type WorkspaceConnectionState =
  | "loading"
  | "connected"
  | "error_never_connected"
  | "error_lost_connection";
```

Backoff: base 5s, max 60s, max 10 attempts, 0.5 jitter factor.

---

## 3. Module-Level Workspace Cache

**File:** `frontend/src/api/workspace.ts`

```typescript
let workspaceCache: WorkspaceData | null = null;
let fetchPromise: Promise<WorkspaceData> | null = null;
let cacheGeneration = 0;
```

- `fetchWorkspace()` returns cached data if available, deduplicates concurrent requests
- `refreshWorkspace()` increments `cacheGeneration`, invalidating stale in-flight responses
- All mutating APIs (`renameWorkspace`, `deleteWorkspace`, etc.) invalidate cache and update with server response

---

## 4. App.tsx Orchestration

**File:** `frontend/src/App.tsx`

Wires all hooks together and passes props to layout components:

```
useWorkspaceContext() -> workspace, isMultiRepo, sourceReposFilter, ...
useViewState()        -> view, setView, navigateToView, urlIssueId
useWorkspaceParam()   -> [workspaceParam, setWorkspaceParam]
useWorkspaceState()   -> switchWorkspace (snapshot save/restore)
useIssues({mode, sourceRepos, workspaceName})
```

`AppLayout` receives four named slots:
- `navRail` — NavRail icon rail
- `sidebar` — WorkspaceTree (always rendered)
- `title` — WorkspaceBreadcrumb
- `navigation` — search + filter bar

Multi-repo guards:
- `activeView === "workspace" && !isMultiRepo` → redirect to kanban
- `WorkspaceSwitcher` only rendered when `isMultiRepo`
- `WorkspaceBreadcrumb` shows workspace name only when `isMultiRepo`

---

## 5. WorkspaceTree Sidebar

**File:** `frontend/src/components/WorkspaceTree/WorkspaceTree.tsx`

The primary navigation surface. Always rendered but adapts to workspace data.

```
WorkspaceTree (aside)
+-- Toggle button (header + star + count + "+ Add" + ActiveAllToggle)
+-- [Loading skeletons / ErrorDisplay / Stale banner]
+-- Agent section (all workspace agents)
+-- Workspaces section (DnD sortable, only when 2+ workspaces)
+-- Repo list (role="radiogroup")
|   +-- "All Workspaces" radio button
|   +-- RepoGroupList (per-repo collapsible agent groups)
+-- EpicTaskTree (hierarchical issue tree)
+-- WorkQueueSection (issue counts by status)
+-- "+ New Workspace" button
+-- SidebarStatusBar (N working / N reviewing / N idle)
```

### Key Sub-Components

| Component | File | Purpose |
|-----------|------|---------|
| `RepoGroupList` | `WorkspaceTree/RepoGroupList.tsx` | Per-repo collapsible groups with agents and health dots |
| `EpicTaskTree` | `WorkspaceTree/EpicTaskTree.tsx` | Hierarchical epic/task tree with context menus |
| `ActiveAllToggle` | `WorkspaceTree/ActiveAllToggle.tsx` | Filter: active issues vs all issues |
| `SortableWorkspaceEntry` | `WorkspaceTree/SortableWorkspaceEntry.tsx` | Draggable workspace entry (`@dnd-kit`) |
| `WorkspaceContextMenu` | `WorkspaceTree/WorkspaceContextMenu.tsx` | Right-click: rename, delete, set default |
| `SidebarStatusBar` | `WorkspaceTree/SidebarStatusBar.tsx` | Agent activity counts |
| `WorkQueueSection` | `WorkspaceTree/WorkQueueSection.tsx` | Issue counts by status category |

### Collapsed State

When collapsed, shows total agent count badge and worst health color. Error badge on connection failure.

### Repo Radio-Select

Uses `role="radiogroup"` / `role="radio"` ARIA semantics. Clicking a repo calls `onWorkspaceSelect(repoName)`, clicking "All" calls `onWorkspaceSelect(null)`.

### Per-Repo Health

`computeRepoHealth()` in `utils/workspaceHealth.ts` computes `"green" | "yellow" | "red"` from agents in each repo group.

---

## 6. NavRail and View Plumbing

### ViewMode

```typescript
type ViewMode =
  | "kanban" | "table" | "graph" | "monitor" | "observability"
  | "terminal" | "workspace" | "settings" | "files" | "issue-detail";
```

### NavRail Component

**File:** `frontend/src/components/NavRail/NavRail.tsx`

Icon-only vertical navigation rail. Pure presentation component driven by App.tsx:
- TOP: kanban, table, terminal
- BOTTOM: settings
- Props: `activeView`, `onChange`, `sessionCount`, `badges`

### useViewState Hook

**File:** `frontend/src/hooks/useViewState.ts`

Bidirectional URL sync for `?view=`:
- `setView()` uses `replaceState` (no history entry)
- `navigateToView()` uses `pushState` (history entry for back/forward)
- `urlIssueId` extracted from `?issue=` param
- `popstate` listener for browser back/forward

---

## 7. Workspace CRUD

### Create

- **Frontend:** `CreateWorkspaceModal` (React portal)
- **Backend:** `POST /api/workspace/create`
- Types: `"clone"` (git URLs) or `"empty"` (local paths)
- On success: hard navigation via `window.location.replace("/?_ws=" + name)` to ensure daemon pool registration

### Rename

- **Frontend:** inline edit in `SortableWorkspaceEntry`
- **Backend:** `PATCH /api/workspace/rename` with `{ old_name, new_name }`
- Atomic write via temp-file-then-rename on `~/.loom/config.yaml`

### Delete

- **Frontend:** `ConfirmDialog` → 5-second delayed deletion with undo toast
- **Backend:** `DELETE /api/workspace/{name}`
- Undo: `clearTimeout` prevents API call if triggered before timer fires
- Error: 409 Conflict if workspace has running agents

### Drag-to-Reorder

- Uses `@dnd-kit/core` + `@dnd-kit/sortable`
- Optimistic local reorder → `PUT /api/workspace/order { order: string[] }`
- Keyboard: Alt+Up/Down for accessible reordering

### Set Default (Star)

- `PUT /api/workspace/default { name }` / `DELETE /api/workspace/default`
- Star glyph renders on default workspace in sidebar

---

## 8. Scoped Operations

### Workspace-Scoped Issue Fetch

All fetches include `Workspace` HTTP header set by `setActiveWorkspaceClient()`. Backend routes to correct workspace's beads store.

```
useIssues({ sourceRepos: sourceReposFilter, workspaceName })
  -> GET /api/issues?source_repos=repo-a,repo-b  (+ Workspace: myws header)
```

### Search Scope Indicator

`useSearchScope` derives human-readable scope from repo selection:
- 1 repo → repo name
- All repos in a group → group name
- Multiple → "N repos"
- All selected → hidden

### Workspace-Scoped Session Isolation

`useWorkspaceState` closes open panels synchronously before state restoration on workspace switch.

---

## 9. Cross-Workspace Behavior

### All Workspaces Combined View

Selecting "All Workspaces" clears `selectedRepoNames` → `sourceReposFilter` becomes `undefined` → no `source_repos` filter applied. Shows all repos within the current workspace (not a true cross-workspace query).

### Context Preservation on Switch

**File:** `frontend/src/hooks/useWorkspaceState.ts`

Maintains `Map<workspaceId, WorkspaceSnapshot>` in a ref:

```
captureSnapshot()  -> save {view, filters, searchValue, selectedIssueId, scrollTop}
closeAllPanels()   -> synchronously close panels
restoreSnapshot()  -> apply saved state (or kanban defaults)
updateWorkspaceUrl(newId) -> replaceState ?workspace=
```

Snapshots are in-memory only (reset on page reload).

### WorkspaceSwitcher Overlay

**File:** `frontend/src/components/WorkspaceSwitcher/WorkspaceSwitcher.tsx`

Command-palette overlay triggered by Cmd+K (multi-repo only):
- Substring search, arrow key navigation, Enter to select
- Cmd+Shift+1-9 positional shortcuts
- Active workspace checkmark

---

## 10. Connection State Handling

### Sidebar Rendering by State

| State | Sidebar Behavior |
|-------|-----------------|
| `loading` | Skeleton rows |
| `connected` | Normal content |
| `error_never_connected` | ErrorDisplay with retry button |
| `error_lost_connection` | Yellow stale banner, dimmed repo list, stale data preserved |

### Agent-Fleet Merge

WorkspaceTree merges two agent sources:
1. **Fleet agents** from `useAgentContext()` — live running agents
2. **Configured agents** from workspace config — shown as placeholders when not started

Fleet agents take precedence by name.

---

## 11. Single-Repo Fallback

When `isMultiRepo` is false or workspace data not loaded:

| Guard | Behavior |
|-------|----------|
| `workspace` view | Redirected to kanban |
| WorkspaceSwitcher | Not rendered |
| WorkspaceBreadcrumb | Name hidden |
| Workspace list section | Hidden when < 2 workspaces |
| Repo selector in filter bar | Hidden with 1 repo |

---

## 12. Backend API Surface

| Route | Method | Handler | Description |
|-------|--------|---------|-------------|
| `/api/workspace` | GET | `handleWorkspace` | Workspace topology (repos, agents, summaries) |
| `/api/workspace/create` | POST | `handleWorkspaceCreate` | Create workspace (clone/empty) |
| `/api/workspace/rename` | PATCH | `handleWorkspaceRename` | Rename workspace in config |
| `/api/workspace/{name}` | DELETE | `handleWorkspaceDelete` | Remove workspace config entry |
| `/api/workspace/order` | PUT | `handleWorkspaceReorder` | Persist display order |
| `/api/workspace/default` | PUT | `handleSetDefaultWorkspace` | Set default workspace |
| `/api/workspace/default` | DELETE | `handleClearDefaultWorkspace` | Clear default |
| `/api/workspace/{name}/config/backend` | PATCH | `handleWorkspaceBackendPatch` | Per-workspace backend config |
| `/api/workspaces` | GET | `handleListWorkspaces` | List all workspaces with pool stats |

### Workspace Header Routing

`GET /api/workspace` reads the `Workspace` HTTP header to determine which workspace's repos/agents to return.

---

## 13. Key Design Decisions

- **Workspace switch = full-page reload.** Changing the `Workspace` HTTP header requires resetting all in-flight requests, SSE connections, and cached data.
- **Repo selection = client-side only.** No reload needed; `sourceReposFilter` drives `source_repos` query param.
- **WorkspaceProvider as single source of truth.** All workspace-aware components consume `useWorkspaceContext()`.
- **Module-level workspace cache.** Deduplicates concurrent requests, generation tracking prevents stale responses.
- **Optimistic UI for CRUD.** Default, reorder, rename use optimistic updates with rollback. Delete uses delayed execution with undo toast.

---

## 14. File Map

### Frontend

| File | Role |
|------|------|
| `App.tsx` | Orchestration hub |
| `hooks/useWorkspaceContext.tsx` | Canonical workspace context |
| `hooks/useWorkspace.ts` | 60s polling hook |
| `hooks/useWorkspaceRepos.ts` | Resilient fetcher with backoff |
| `hooks/useWorkspaceTree.ts` | Epic/task tree builder |
| `hooks/useWorkspaceState.ts` | Per-workspace UI snapshot |
| `hooks/useWorkspaceParam.ts` | URL `?workspace=` sync |
| `hooks/useRepoFilter.ts` | URL `?repos=` sync |
| `hooks/useViewState.ts` | URL `?view=` sync |
| `hooks/useSearchScope.ts` | Scope name derivation |
| `api/workspace.ts` | API client with module-level cache |
| `components/WorkspaceTree/WorkspaceTree.tsx` | Sidebar root |
| `components/WorkspaceTree/RepoGroupList.tsx` | Per-repo groups |
| `components/WorkspaceTree/EpicTaskTree.tsx` | Epic/task tree |
| `components/WorkspaceTree/SortableWorkspaceEntry.tsx` | Draggable workspace |
| `components/WorkspaceTree/WorkspaceContextMenu.tsx` | Right-click menu |
| `components/WorkspaceTree/SidebarStatusBar.tsx` | Agent counts |
| `components/NavRail/NavRail.tsx` | Icon nav rail |
| `components/WorkspaceBreadcrumb/WorkspaceBreadcrumb.tsx` | Header breadcrumb |
| `components/WorkspaceSwitcher/WorkspaceSwitcher.tsx` | Cmd+K overlay |
| `components/CreateWorkspaceModal/CreateWorkspaceModal.tsx` | Create modal |

### Backend

| File | Role |
|------|------|
| `internal/webui/handlers_workspace.go` | GET /api/workspace |
| `internal/webui/handlers_workspace_create.go` | POST create |
| `internal/webui/handlers_workspace_rename.go` | PATCH rename |
| `internal/webui/handlers_workspace_delete.go` | DELETE |
| `internal/webui/handlers_workspace_reorder.go` | PUT order |
| `internal/webui/handlers_workspace_default.go` | PUT/DELETE default |
| `internal/webui/routes.go` | Route registration |
