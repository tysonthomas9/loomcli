# Workspace file browser architecture and security

> **Status:** Current · CANONICAL for shipped file-browser behaviour · *audited 2026-08-03*
>
> This doc owns the security, containment, concurrency and RBAC contract of the
> file browser. `2026-07-02-file-browser-v2-scoped-explorer.md` and
> `2026-07-07-file-explorer-v3-unified-tree.md` are historical design proposals
> written *before* this contract existed; v2's "no policy guards" stance was
> reversed before ship. For component-level architecture see
> [docs/arch/file-explorer.md](../arch/file-explorer.md).

The workspace file browser exposes a workspace checkout through
`/api/workspaces/{ws}/files`. It is an operator tool over local source code, not
a general-purpose file server.

## Filesystem boundary

- Every request resolves one registered workspace, repository, or agent scope
  (`internal/webui/svcimpl/file_service.go:145`
  `resolveScopeRoot(wsID, scope, target, repo)`; the `repo` qualifier is
  accepted only for `scope=agent`). User paths are relative to that scope and
  reject absolute paths, NUL bytes, traversal components, and root mutation
  aliases (`internal/webui/svcimpl/rooted_file_store.go:137`).
- On Unix, traversal and mutations are descriptor-relative (`openat`,
  `fstatat`, `renameat`, and `unlinkat`) with no-follow checks —
  `internal/webui/svcimpl/rooted_file_store_unix.go:26,55,100,110,243` all carry
  `unix.O_NOFOLLOW` / `unix.AT_SYMLINK_NOFOLLOW`. Directory file descriptors
  remain open across atomic writes and renames. Other platforms use Go's rooted
  filesystem API as the containment boundary
  (`internal/webui/svcimpl/rooted_file_store_other.go:1` `//go:build !unix`).
- Symlinks are never followed by browser operations. A concurrent symlink swap
  cannot redirect an operation outside the selected root.
- A case-insensitive `.git` segment is always hidden and denied for read and
  write operations — `rooted_file_store.go:141` returns
  `ErrForbidden(".git paths are not available")` for every operation, with the
  case-folded segment scan at `rooted_file_store.go:146-151`; the listing filter
  is `file_service.go:141` `hiddenScopeSegments = map[string]bool{".git": true}`.
  Recursive move/delete also refuses an ancestor containing `.git`
  (`rooted_file_store.go:558,598-599,689,720,760`); this invariant is not
  configurable.
- `.loom/terminal-worktrees` is intentionally visible. It receives the same
  rooted/no-follow treatment as every other non-`.git` path
  (`internal/webui/svcimpl/file_walk_hardening_test.go:25,50-51`).

## Authorization

Remote mode requires an authenticated identity and a workspace-scoped role
resolver (`internal/webui/server/middleware/file_access.go`). Viewers can read
ordinary files; editors and administrators can write and can read sensitive
files — the role→capability table is
`server/middleware/file_access.go:92-102`. Sensitive paths include `.env*`, key
and certificate extensions, SSH private keys, and `.netrc`
(`internal/webui/fileaccess/access.go:42-77`, reached via
`internal/webui/service/file_access.go:30` `IsSensitiveFilePath`, a thin alias
for `fileaccess.IsSensitivePath`); they are
omitted from viewer tree, index, search, status, and checkout counts
(`internal/webui/svcimpl/file_walk.go:348,376`, capability plumbed in at
`file_walk.go:271-273`), and explicit access is denied.

Open local mode is limited to explicitly configured loopback frontend origins.
The request host must be loopback and must match the configured frontend host;
an `Origin`, when browsers send one, must match that configured origin. Local
reverse proxies must preserve the browser-facing `Host`. Unauthenticated
network hosts, hostile origins, missing remote resolvers, and unknown roles fail
closed.

`GET /api/workspaces/{ws}/files/capabilities`
(`internal/webui/handlers/misc/module.go:35`) returns the effective
`{read, write, sensitive}` permissions
(`internal/webui/server/middleware/file_access.go:95-99`). The frontend removes
mutation controls when `write` is false; the server remains the enforcement
boundary.

## Concurrency contracts

An ordinary editor Save is last-writer-wins by design. Create and Duplicate use
`If-None-Match: *` (only `*` is accepted —
`internal/webui/handlers/misc/files.go:352-354`). Delete and move require the
current source version; an overwrite move also requires the destination version.
Search/replace, conflict Overwrite, and commit restore use `If-Match`
(`handlers/misc/files.go:347,375`, enforced at
`internal/webui/svcimpl/file_service.go:324-345`). Missing preconditions return
428; stale preconditions return 412. Per-scope, case-folded path locks serialize
overlapping browser mutations (`internal/webui/svcimpl/file_versions.go:23-36`,
case folding at `file_versions.go:90-98`).

Versions are SHA-256 content hashes for files and bounded manifests for
directories. They detect changes observed through either the browser or the
terminal, but they are not a filesystem transaction with arbitrary terminal
writers: a terminal process can still change a file immediately after the
browser's final version check. Conditional writes prevent stale browser intent;
they do not provide cross-process locking.

## Search and Git bounds

Tree, index, and search share the rooted path policy. `.git` is never returned;
default search excludes such as `node_modules` can only be changed where the API
explicitly permits it. Text search skips non-UTF-8 and oversized files, reports
truncation reasons, and enforces result and byte limits. The index cache is
bounded by roots, bytes, and age (`internal/webui/svcimpl/file_index_cache.go:14-16`
= 32 entries / 64 MiB, TTL at `file_index_cache.go:40,59,86`); overlapping
mutations invalidate cached ancestors
(`internal/webui/svcimpl/file_walk.go:266-267`) and singleflight suppresses
duplicate builds (`internal/webui/svcimpl/file_service.go:30`
`indexBuilds singleflight.Group`, used at `file_walk.go:79-103`).

Git decoration, diff, blame, and history run through the bounded Git inspector
(`internal/webui/svcimpl/file_history.go`, `file_git_status.go`): arguments are
separated from revisions, the environment is isolated, output is capped
(`file_history.go:17-19` — 100 commits, 5000 blame lines), commands have
timeouts, and concurrent Git work is limited. Filenames are parsed from
NUL-delimited output. Browser save snapshots are disabled; History contains Git
commits only — `file_history.go:236` emits `Kind: "commit"` and nothing else,
and `file_history.go:382-390` `cleanupLegacySaveHistory` deletes the legacy
`file-history` snapshot root on startup.

## Checkout enumeration

`GET /api/workspaces/{ws}/files/checkouts`
(`internal/webui/handlers/misc/module.go:40`, handler
`handlers/misc/files.go:268`) enumerates the workspace's checkouts with
per-checkout existence, branch and change count. A checkout that is not present
on this machine reports `exists:false` rather than erroring, and a checkout
whose git metadata is unreadable reports `status_error` — file browsing stays
available, only the git overlays degrade.

`POST /api/workspaces/{ws}/files/checkouts/repair`
(`handlers/misc/module.go:41`, handler `handlers/misc/files.go:282`, body
`internal/webui/service/file.go:226` `{scope, target, repo?, force?}`) repairs or
provisions a known workspace checkout (`internal/ops/fileops.go:108-112`). The
target must already be a checkout the workspace knows about — an unknown target
or a repo outside the agent's allowed set is rejected as a validation error
(`internal/webui/svcimpl/file_git_status.go:269-271`).

## Document sessions

Open documents live in an in-memory registry keyed by workspace, checkout ref,
and path, so split panes share one draft. Tabs may persist, but draft contents do
not survive a page reload. Focus/reconnect refreshes clean documents. If an
external edit arrives while a draft is dirty, the editor preserves the draft
and offers Reload, Compare, and conditional Overwrite. A second external change
before Overwrite produces another conflict instead of silently replacing it.

## Verification matrix

| Evidence | Coverage |
| --- | --- |
| Deterministic Go tests | containment, symlink races, hidden/sensitive policy, RBAC, Host/Origin checks, versions, locks, cache bounds, Git limits |
| Frontend unit tests | capabilities, create/duplicate contracts, versioned move/delete, shared drafts, refresh conflicts, conditional replace/restore |
| Real self-contained Playwright | real `loom serve`, FleetDB workspace registration, real Git checkout, file API contracts, editor/search/conflict/history workflow |
| Agent-browser | independent desktop and compact viewport inspection against the real local API/frontend |

Remote viewer/OIDC behavior requires a deployment with a real identity provider
and workspace-role resolver. Local-mode browser runs do not constitute remote
RBAC evidence; deterministic middleware tests cover that boundary when such a
deployment is unavailable.

## Related

- [docs/arch/file-explorer.md](../arch/file-explorer.md) — component and data-flow
  architecture of the same subsystem (frontend modules, backend services, routes).
- [2026-07-07-file-explorer-v3-unified-tree.md](2026-07-07-file-explorer-v3-unified-tree.md)
  — the shipped information architecture (historical design doc).
- [2026-07-02-file-browser-v2-scoped-explorer.md](2026-07-02-file-browser-v2-scoped-explorer.md)
  — superseded proposal; its §5 "accepted risks" were reversed before ship.
