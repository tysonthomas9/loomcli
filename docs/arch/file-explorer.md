# File Explorer Architecture

> **Status:** Current · *audited 2026-07-23*
>
> Component and data-flow map of the workspace file explorer. Policy — what the
> browser is *allowed* to touch — lives in
> [../design/workspace-file-browser-security.md](../design/workspace-file-browser-security.md)
> and is not restated here. Information architecture and the rationale for the
> checkout-addressed tree live in
> [../design/2026-07-07-file-explorer-v3-unified-tree.md](../design/2026-07-07-file-explorer-v3-unified-tree.md).

## Overview

One React component tree (`components/FileExplorer/`) renders every file surface
in the web UI: the Files page, the Agents page "files" tab, and the agent detail
panel. It talks to one Go service (`internal/webui/svcimpl/file_*.go`) through
one route family (`/api/workspaces/{ws}/files/*`,
`internal/webui/handlers/misc/module.go:30-51`).

The unit of address is a **checkout**, not a directory: `{scope, target, repo?}`
plus a path. Three scopes exist —
`workspace` | `repo` | `agent` (`internal/webui/frontend/src/api/workspace/files.ts:145`),
resolved server-side by
`internal/webui/svcimpl/file_service.go:145`
`resolveScopeRoot(wsID, scope, target, repo)`. `repo` is honored only for
`scope=agent`, where it selects `worktrees/<repo>/<agent>`.

---

## 1. Component Hierarchy

```
FilesPage (views/FilesPage.tsx:19)            <- mode="workspace"
AgentsPage (views/AgentsPage.tsx:517-518)     <- mode="agent"
AgentDetailPanel (…/AgentDetailPanel.tsx:472-473) <- mode="agent"
  |
  +-- WorkspaceFileBrowser                    <- orchestrator (~1765 lines)
       +-- FileExplorerTreePanel              <- sidebar: sections, lens toggle
       |    +-- FileTree                      <- one lazy-loading tree per root
       +-- FileExplorerEditorGroup            <- one per split group
       |    +-- FileTabBar                    <- open tabs for that group
       |    +-- WorkspaceFilePane             <- CodeMirror editor / binary / diff
       |    |    +-- FileRevisionPane         <- read-only file-at-revision view
       |    +-- FileHistoryPanel              <- per-file commit timeline
       +-- FileSearchPanel                    <- workspace-wide search + replace
       +-- QuickOpenPalette                   <- Cmd+P, checkout-labelled results
       +-- SymbolPalette                      <- Cmd+Shift+O, lezer-derived symbols
       +-- FileExplorerDialogs                <- create / rename / delete / Move to…
```

`index.ts` exports only `WorkspaceFileBrowser`; everything else is internal to
the folder. The embedding mode is a single prop —
`FileBrowserMode = "workspace" | "agent"`
(`components/FileExplorer/treeRoots.ts:11`), consumed via
`FileBrowserProps` (`components/FileExplorer/workspaceFileBrowserTypes.ts:9-12`).

### Pure modules (no React)

| Module | Responsibility |
|---|---|
| `treeRoots.ts` | Builds the three tree sections from workspace context + checkouts (`treeRoots.ts:141` `buildFileTreeSections`) |
| `checkoutAvailability.ts` | `exists`/`status_error` → is this checkout usable, and its change count |
| `changesLens.ts` | Groups git-status entries by checkout and maps porcelain XY → friendly chips |
| `gitDecorations.ts` | File/folder status decoration kinds; also `resolveTreeDropMove` |
| `gitGutter.ts` | Buffer-vs-HEAD gutter marks; bounded by `GIT_GUTTER_SCAN_LIMIT` / `GIT_GUTTER_TIMEOUT_MS` (`gitGutter.ts:11-12`) |
| `quickOpen.ts` | Fuzzy ranking over the index, MRU-weighted (`rankQuickOpenItems`) |
| `searchReplace.ts` | Replacement preview computation for the search panel |
| `fileExplorerLocalUtils.ts` | localStorage keys, tree width bounds, path helpers |
| `workspaceFileBrowserTypes.ts` | `ExplorerLens = "files" \| "changes"`, `CompareMode`, props |

---

## 2. Tree Information Architecture

The sidebar renders exactly three sections
(`treeRoots.ts:42` `id: "agents" | "repos" | "workspace"`, assembled at
`treeRoots.ts:218-227`; in `mode="agent"` only the AGENTS section is built):

```
AGENTS      one root per agent; single-repo agents are FLATTENED
            (AgentTreeRoot.flattenedRef), cross-repo agents get one
            child per (agent, repo) checkout
REPOS       shared repo checkouts
WORKSPACE   the workspace root — collapsed and dimmed by default
```

Each root lazy-loads through `useScopedFileTree`
(`hooks/common/useScopedFileTree.ts:178`) with its own `(scope, target, repo)`
triple. Expansion, selection and tabs are keyed by the full checkout ref
(`utils/fileExplorerRefs.ts` `checkoutRefKey` / `tabIdentityKey`), so "switching
scope" is scrolling, not remounting. There is no scope `<select>`.

Checkouts reported `exists: false` by the checkouts endpoint render disabled.
`hasAvailableCheckoutStatus` (`checkoutAvailability.ts:3`) is the single
predicate for "this checkout can show git overlays".

---

## 3. State: three stores, three lifetimes

| State | Owner | Lifetime |
|---|---|---|
| Open tabs + split groups | `stores/fileBrowserStore.tsx` (zustand vanilla, one store per workspace) | localStorage `file-browser-tabs:v3`, agent mode suffixes `:agent:<name>` (`fileBrowserStore.tsx:77-83`) |
| Open document contents / dirty drafts | `stores/fileDocumentRegistry.ts` (`FileDocumentRegistry`, `:123`) | In-memory only — drafts do **not** survive reload |
| Tree width, lens, compare mode | `fileExplorerLocalUtils.ts:7-10` localStorage keys | Browser profile |

`FileDocumentRegistry` is keyed by `(workspaceId, scope, target, repo, path)`
(`fileDocumentKey`, `fileDocumentRegistry.ts:67`), which is why two split panes
showing the same file share one draft. It also owns the external-change
conflict object (`ExternalFileConflict`, `:18`) that drives the
Reload / Compare / Overwrite choice.

---

## 4. Server routes and services

### Route family

All 16 routes are registered by one module,
`internal/webui/handlers/misc/module.go:30-51`, each wrapped in the file-access
middleware (`module.go:31-33`):

| Method | Path (under `/api/workspaces/{ws}`) | Handler |
|---|---|---|
| GET | `/files/capabilities` | `HandleFileCapabilities` |
| GET | `/files/tree` | `HandleScopedFileTree` |
| GET | `/files/index` | `HandleScopedFileIndex` |
| POST | `/files/search` | `HandleScopedFileSearch` |
| GET | `/files/git-status` | `HandleScopedGitStatus` |
| GET | `/files/checkouts` | `HandleFileCheckouts` |
| POST | `/files/checkouts/repair` | `HandleFileCheckoutRepair` |
| GET | `/files/diff` | `HandleScopedFileDiff` |
| GET | `/files/history` | `HandleScopedFileHistory` |
| GET | `/files/blame` | `HandleScopedFileBlame` |
| GET | `/files` | `HandleScopedFileRead` |
| GET | `/files/stat` | `HandleScopedFileStat` |
| PUT | `/files` | `HandleScopedFileWrite` |
| DELETE | `/files` | `HandleScopedFileDelete` |
| POST | `/files/mkdir` | `HandleScopedFileMkdir` |
| PATCH | `/files/move` | `HandleScopedFileMove` |

There is no `/api/v1` prefix and no agent-name-keyed family: the former
`/api/agents/{name}/files*` routes were deleted, not delegated
(`internal/webui/app/routes_test.go:1864-1866` asserts 404).

### Service decomposition

| File | Responsibility |
|---|---|
| `internal/webui/svcimpl/file_service.go` | `fileServiceImpl`; scope resolution (`:145`), read/write/CRUD, precondition enforcement (`:324-345`) |
| `internal/webui/svcimpl/rooted_file_store.go` | The containment boundary — every user path goes through `scopedRoot`/`rootedFileStore` |
| `internal/webui/svcimpl/rooted_file_store_unix.go` | Descriptor-relative syscalls (`openat`/`fstatat`/`renameat`/`unlinkat`, all `O_NOFOLLOW`) |
| `internal/webui/svcimpl/rooted_file_store_other.go` | `//go:build !unix` fallback on Go's rooted-FS API |
| `internal/webui/svcimpl/file_walk.go` | Bounded walk shared by tree, index and search; caps at `:21-24` (50k entries, 2s walk budget, 10s cache TTL) |
| `internal/webui/svcimpl/file_index_cache.go` | LRU index cache bounded by entries/bytes/TTL; overlap invalidation (`:108`) |
| `internal/webui/svcimpl/file_versions.go` | Content/manifest versions + `pathLockSet` mutation serialization (`:23-36`) |
| `internal/webui/svcimpl/file_git_status.go` | Checkout enumeration (`:133 ListFileCheckouts`), repair (`:254 RepairCheckout`), status fan-out |
| `internal/webui/svcimpl/file_history.go` | `git log`/blame/diff/file-at-rev through a bounded inspector; legacy save-history cleanup (`:384`) |
| `internal/webui/fileaccess/access.go` | Sensitive-path name/extension tables (`IsSensitivePath`, `:42-77`) — what `service.IsSensitiveFilePath` resolves to |
| `internal/webui/service/pathsec/pathsec.go` | Diff-path denial tables (`IsDeniedPath`, `ValidateDiffPath`); used only by the two diff services |
| `internal/webui/server/middleware/file_access.go` | Role → `{read, write, sensitive}` capability resolution |

The index build is deduplicated by `singleflight`
(`file_service.go:30` `indexBuilds singleflight.Group`, used at
`file_walk.go:79-103`), so N tabs opening Quick Open at once produce one walk.

---

## 5. Read / write flow

```
FileTree row click
  -> WorkspaceFileBrowser opens a tab in the active group (fileBrowserStore)
  -> FileDocumentRegistry.read(ref)      GET  /api/workspaces/{ws}/files?scope=…&path=…
  -> WorkspaceFilePane renders CodeMirror with the returned content + version

Cmd+S
  -> FileDocumentRegistry.write(ref, content)   PUT /api/workspaces/{ws}/files
     (ordinary Save is last-writer-wins by design; create/duplicate/delete/move/
      replace/restore carry If-Match / If-None-Match — see the security doc)
  -> on 412/428 the registry raises ExternalFileConflict -> Reload | Compare | Overwrite
```

Git overlays are independent of browsing: a checkout whose working directory
exists stays readable and editable even when its git metadata is unreadable —
`ListFileCheckouts` reports `status_error` rather than failing the request
(`file_git_status.go:242` `checkoutError`).

---

## 6. Search, Quick Open, symbols

- **Global search** (`FileSearchPanel.tsx`) posts to `/files/search`; the server
  walks with the same bounded walker as the tree, skips non-UTF-8 and oversized
  files, and reports truncation reasons. Replace-with-preview is computed
  client-side in `searchReplace.ts` and applied as ordinary writes.
- **Quick Open** (`QuickOpenPalette.tsx` + `quickOpen.ts`) ranks over the
  `/files/index` result. In workspace mode the index spans every checkout, so
  results are labelled with their checkout.
- **Symbols** (`SymbolPalette.tsx`) are derived in-browser from the CodeMirror
  Lezer tree (`utils/lezerSymbols.ts`). There is no LSP and no file watcher.

---

## 7. File Map

### Frontend (`internal/webui/frontend/src/`)

| Path | Responsibility |
|---|---|
| `components/FileExplorer/WorkspaceFileBrowser.tsx` | Orchestrator: tree + groups + palettes + dialogs |
| `components/FileExplorer/FileExplorerTreePanel.tsx` | Sidebar shell, sections, lens toggle, resize |
| `components/FileExplorer/FileTree.tsx` | One tree root: lazy expand, inline edit, context menu |
| `components/FileExplorer/FileExplorerEditorGroup.tsx` | One split group: tab bar + pane + history |
| `components/FileExplorer/FileTabBar.tsx` | Tab strip for a group |
| `components/FileExplorer/WorkspaceFilePane.tsx` | Editor pane: CodeMirror, binary/read-only, blame |
| `components/FileExplorer/FileRevisionPane.tsx` | File-at-revision read-only view |
| `components/FileExplorer/FileHistoryPanel.tsx` | Per-file commit timeline (commits only) |
| `components/FileExplorer/FileSearchPanel.tsx` | Search + replace overlay |
| `components/FileExplorer/QuickOpenPalette.tsx` | Cmd+P palette |
| `components/FileExplorer/SymbolPalette.tsx` | Cmd+Shift+O palette |
| `components/FileExplorer/FileExplorerDialogs.tsx` | Create/rename/delete/Move-to dialogs |
| `components/FileExplorer/treeRoots.ts` | Section + root construction |
| `components/FileExplorer/changesLens.ts` | Changes lens grouping and status chips |
| `components/FileExplorer/checkoutAvailability.ts` | Checkout usability + change counts |
| `components/FileExplorer/gitDecorations.ts` | Tree status decorations |
| `components/FileExplorer/gitGutter.ts` | Editor gutter marks vs HEAD |
| `components/FileExplorer/quickOpen.ts` | Quick Open ranking |
| `components/FileExplorer/searchReplace.ts` | Replace preview |
| `components/FileExplorer/fileExplorerLocalUtils.ts` | Storage keys, widths, path helpers |
| `components/FileExplorer/workspaceFileBrowserTypes.ts` | Props, lens and compare-mode types |
| `stores/fileBrowserStore.tsx` | Per-workspace tab/group store (v3 schema) |
| `stores/fileDocumentRegistry.ts` | Shared open-document registry and conflicts |
| `utils/fileExplorerRefs.ts` | Checkout-ref normalization, keys, labels |
| `hooks/common/useScopedFileTree.ts` | Directory listing per tree root |
| `hooks/common/useFileDocument.tsx` | React binding to `FileDocumentRegistry` |
| `api/workspace/files.ts` | Every file API call and its DTOs |

### Server (`internal/webui/`)

| Path | Responsibility |
|---|---|
| `handlers/misc/module.go` | Route registration (all 16 routes) |
| `handlers/misc/files.go` | Request parsing, precondition headers, responses |
| `svcimpl/file_service.go` | Service core, scope resolution, CRUD |
| `svcimpl/rooted_file_store.go` (+`_unix.go`, `_other.go`, `_platform.go`) | Containment boundary |
| `svcimpl/file_walk.go` | Bounded walk, index build, search |
| `svcimpl/file_index_cache.go` | Index cache |
| `svcimpl/file_versions.go` | Versions + mutation locks |
| `svcimpl/file_git_status.go` | Checkouts, repair, git status |
| `svcimpl/file_history.go` | History, blame, diff, file-at-rev |
| `fileaccess/access.go` | Sensitive-path tables |
| `service/pathsec/pathsec.go` | Diff-path denial tables (diff services only) |
| `server/middleware/file_access.go` | RBAC → capabilities |

One dependency sits outside this prefix: `internal/ops/fileops.go` (repo root),
which owns workspace-level checkout repair/provisioning.

---

## Related

- [../design/workspace-file-browser-security.md](../design/workspace-file-browser-security.md)
  — the security, containment, concurrency and RBAC contract (authoritative for policy).
- [../design/2026-07-07-file-explorer-v3-unified-tree.md](../design/2026-07-07-file-explorer-v3-unified-tree.md)
  — information architecture and the decisions behind the unified tree.
- [../design/2026-07-02-file-browser-v2-scoped-explorer.md](../design/2026-07-02-file-browser-v2-scoped-explorer.md)
  — superseded proposal; kept for the scoped-root rationale only.
- [terminal-system.md](terminal-system.md), [issue-detail-view.md](issue-detail-view.md)
  — the other two web-UI subsystem maps.
