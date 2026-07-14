# Workspace file browser architecture and security

The workspace file browser exposes a workspace checkout through
`/api/workspaces/{ws}/files`. It is an operator tool over local source code, not
a general-purpose file server.

## Filesystem boundary

- Every request resolves one registered workspace, repository, or agent scope.
  User paths are relative to that scope and reject absolute paths, NUL bytes,
  traversal components, and root mutation aliases.
- On Unix, traversal and mutations are descriptor-relative (`openat`,
  `fstatat`, `renameat`, and `unlinkat`) with no-follow checks. Directory file
  descriptors remain open across atomic writes and renames. Other platforms use
  Go's rooted filesystem API as the containment boundary.
- Symlinks are never followed by browser operations. A concurrent symlink swap
  cannot redirect an operation outside the selected root.
- A case-insensitive `.git` segment is always hidden and denied for read and
  write operations. Recursive move/delete also refuses an ancestor containing
  `.git`; this invariant is not configurable.
- `.loom/terminal-worktrees` is intentionally visible. It receives the same
  rooted/no-follow treatment as every other non-`.git` path.

## Authorization

Remote mode requires an authenticated identity and a workspace-scoped role
resolver. Viewers can read ordinary files; editors and administrators can write
and can read sensitive files. Sensitive paths include `.env*`, key and
certificate extensions, SSH private keys, and `.netrc`; they are omitted from
viewer tree, index, search, status, and checkout counts, and explicit access is
denied.

Open local mode is limited to explicitly configured loopback frontend origins.
The request host must be loopback and must match the configured frontend host;
an `Origin`, when browsers send one, must match that configured origin. Local
reverse proxies must preserve the browser-facing `Host`. Unauthenticated
network hosts, hostile origins, missing remote resolvers, and unknown roles fail
closed.

The capabilities endpoint returns the effective `{read, write, sensitive}`
permissions. The frontend removes mutation controls when `write` is false; the
server remains the enforcement boundary.

## Concurrency contracts

An ordinary editor Save is last-writer-wins by design. Create and Duplicate use
`If-None-Match: *`. Delete and move require the current source version; an
overwrite move also requires the destination version. Search/replace, conflict
Overwrite, and commit restore use `If-Match`. Missing preconditions return 428;
stale preconditions return 412. Per-scope, case-folded path locks serialize
overlapping browser mutations.

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
bounded by roots, bytes, and age; overlapping mutations invalidate cached
ancestors and singleflight suppresses duplicate builds.

Git decoration, diff, blame, and history run through the bounded Git inspector:
arguments are separated from revisions, the environment is isolated, output is
capped, commands have timeouts, and concurrent Git work is limited. Filenames
are parsed from NUL-delimited output. Browser save snapshots are disabled;
History contains Git commits only.

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
