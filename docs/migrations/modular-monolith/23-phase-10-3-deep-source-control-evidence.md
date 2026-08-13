# Phase 10.3 Deep Source Control Evidence

- **Status:** Complete; owner cutover, legacy deletion, full gate, and exact-source product proof pass
- **Date:** 2026-08-13
- **Parent decision:**
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Stack:** 10.3 of 10.12
- **Loom branch:** `modular-monolith-phase10-03-deep-source-control`
- **Loom parent:** `392c74f1a26e76533c616e3889ba069bb68e279b`
- **Loom implementation commit:** `ff64f81a8`
- **FleetDB companion:** `aec752566821fa029714cef62d4421cbf8238f3d`

## Outcome

Source Control is now the single owner of file and Git product semantics. Its
public workspace surface is exactly three consumer capabilities:

- `Browse` for repository topology, file reads, search, history, blame, status,
  diff, and pull-request listing;
- `Mutate` for conditional file writes, deletion, directory creation, move,
  branch synchronization, reset, and publication commands; and
- `Checkout` for checkout status, repair, branch placement, and publication
  operations.

`WorkspacePorts` composes those three capabilities over seal-bound access
grants. A zero-value or foreign grant fails before a filesystem adapter is
called. Trusted HTTP composition is the only grant issuer; handlers receive
only the owner capability they need and map generated OpenAPI transport types
explicitly to transport-neutral Source Control commands and results.

Bounded filesystem execution remains an earned private adapter at
`internal/modules/sourcecontrol/filesystem`. It owns rooted filesystem access,
symlink and containment defense, bounded traversal and content, versioning,
cache invalidation, mutation locking, and machine-local Git inspection. It is
not a fourth product port. The only production importer is the private local
Source Control runtime composition adapter.

Local Git mechanics are composed behind `SourceControlAdapter`. They consume a
two-method Workspace projection rather than `store.Store`. Stack persistence,
forge publication, and credential brokerage remain separate private adapters
because they cross durable-store, remote-provider, and secret boundaries.

The following public or horizontal authorities are deleted without forwarding
packages, compatibility aliases, fallbacks, or dual paths:

- `internal/webui/filecoord`;
- `internal/webui/sourcecontrolcoord`;
- `internal/ops/fileops.go` and `internal/ops/gitops.go`;
- the public `GitOpsImpl`, `NewGitOps`, `SourceControlBrowseAdapter`,
  `FileMechanics`, and `WorkspaceFileAdapter` surfaces; and
- broad Source Control `API`, `Materializer`, task-outcome, stack-lifecycle,
  and stack-binding authority interfaces from the capability root.

Issue diff projection is folded into the existing WebUI read-projection
package rather than introducing a one-file application package. Canonical Git
and file HTTP schemas live in OpenAPI and generated Go/TypeScript types.

## Requirement-to-proof matrix

| Required proof | Result | Representative coverage |
|---|---|---|
| Three public ports | Consumers receive only `Browse`, `Mutate`, or `Checkout`; the private filesystem adapter cannot be recovered from those interfaces. | `TestNewSourceControlExposesOnlyApprovedPorts`, `TestNewSourceControlRejectsMissingPrivateAdapters`, and `TestPhase10SourceControlBroadPublicAuthoritiesCannotReturn` |
| Authorization | Read/write grants are seal-bound, action-scoped, and sensitive-path scoped. Zero, foreign, and insufficient grants fail closed before adapter execution. | `files_access_test.go`, `workspace_operations_test.go`, `TestPhase10SourceControlGrantIssuerIsCompositionOwned`, and middleware file-access tests |
| Filesystem containment | Rooted operations reject traversal, symlink escape, `.git`, sensitive files without grant, invalid scopes, oversize input, and stale preconditions. Concurrent mutation is serialized and bounded. | `files_rooted_store_unix_test.go`, `files_walk_hardening_test.go`, `files_service_test.go`, `files_versions_test.go`, and scoped-file handler tests |
| Git browse and mutation | Opaque checkout placement, validated refs, merge-base resolution, bounded patches, branch operations, reset, status, and pull-request publication map through Source Control. | `workspace_operations_test.go`, `checkout_operations_test.go`, `local_source_control_mechanics_test.go`, Git handler tests, and CLI Git parity tests |
| Workspace locality | Local mechanics receive only `WorkspaceData` and `WorkspacePath`; no composite Store crosses the Source Control adapter seam. | `workspace_projection_test.go`, `source_control_workspace_layout_test.go`, and the exact composite-Store architecture inventory |
| Transport neutrality | Owner models carry no JSON tags. OpenAPI-generated types are the only shared server/frontend HTTP contract. | `transport_neutral_test.go`, `phase10_http_contract_test.go`, generated API staleness, and frontend production build |
| Legacy deletion | File/Git Ops and WebUI coordinators cannot return; HTTP adapters cannot bypass the owner; the filesystem implementation cannot return to the public root. | all six tests in `phase10_source_control_test.go` and `source_control_reachability_test.go` |
| Product UI | Files read/save/diff/restore and Pull Requests list/detail/diff run through the exact built image. | exact-source local-mode proof below |

## Architecture and shrink evidence

The exact production topology after Stack 10.3 is:

- 155 total production packages, down from 156 after Stack 10.2;
- 18 packages under `internal/modules`, including the earned private filesystem adapter;
- 137 packages outside `internal/modules`, down from 139;
- 38 one-file packages, unchanged; and
- 57 one-or-two-file packages, down from 60.

The package count is lower even though the private adapter is now explicit:
two horizontal coordinator packages and their displaced policies are deleted,
while the operating-system seam remains independently testable.

The authoritative architecture transaction reports:

```text
Architecture guardrails passed
  composite Store files: 13/13
  outside composition: 0/0
  legacy handler imports: 0/0
  direct persistence-write rows: 82
  capability module roots: 10
  reviewed mutation commands: 107
  named runtime components: 71
  in-scope non-test goroutine launch definitions: 78
  performance records: 6 (6 measured, 0 explicitly deferred)
  pending architecture decisions: 0
  build profiles enforced: 11/11 (all-files AST checks active)
rsswatch peak_tree_rss_mib=1252.0 limit_mib=2048
```

The import-fanout ratchet was not widened. `internal/cli/serve` remains at its
existing exact count of 27 after implementation details were folded behind one
private runtime handle. `internal/webui/handlers/prreview` tightened from 15 to
13. Composite Store production files tightened from 14 to 13.

## Repository gate

The full Loom gate ran with Go and frontend parallelism bounded to two and with
both `FLEET_DB_REPO` and `FLEET_DB_BIN` pinned to the exact companion above.
The first complete run exposed one stale synthetic snapshot fixture after the
Store inventory tightened from 14 to 13. The fixture was corrected and passed
under the race detector. The clean rerun completed with:

```text
=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

That transaction includes FleetDB compatibility, formatting, vet, build,
golangci-lint, LOC and package-size checks, exact import fanout, the measured
11-profile architecture guard, characterization, supervisor-disabled
validation, raw-exec/log/beads guards, generated API staleness, the repository-
wide race suite, coverage threshold, frontend tests, and frontend production
build.

## Exact-source product proof

The isolated project `loomcli-phase10-sourcecontrol` was rebuilt from the dirty
Stack 10.3 source rooted at the Loom parent above and the exact FleetDB
companion. The final Loom image is:

```text
ea41321aa69f437811b77b121d748bb111c4a92a98be847c25f8f2f60d40067b
```

The deterministic `localdogfood` manifest records run
`20260813T215816Z-44460`, planner task `LOCALMODE-2`, coder task `LOCALMODE-3`,
planner agent `agt-local-mode-ts-planner-79d03dad`, and coder agent
`agt-local-mode-ts-coder-6fd6e4d0`. `make local-mode-verify` proved both tasks
in Review, released assignees, saved planner design, completed transcripts,
zero backend exits, coder local-branch review identity, and coder diff metadata.
The supervisor-disabled verifier proved zero legacy agentdefs, daemon
processes, and daemon control sockets.

The Files UI then opened `source-repo/README.md`, saved a disposable edit,
showed `Changes 1` and `Working tree 1`, and rendered the exact working-tree
diff. The same editor restored the original single heading and trailing
newline; the UI returned to `Changes 0` and `Working tree 0`.

The screenshot
`/private/tmp/phase10-3-exact-file-diff-proof.png` has SHA-256
`4a317f77bbaafc8a1ba9c7d97bc3fd191190c78c68b7336742d9be1f909da156`.

The Pull Requests UI listed two fresh review rows from the same manifest. It
opened coder task `LOCALMODE-3` and rendered the committed
`local-mode-agent-output.txt` patch through the PR review surface.

The screenshot
`/private/tmp/phase10-3-exact-pr-review-proof.png` has SHA-256
`5c65f8e8a20668a49bc82cf359890fa6085713321d13fa9b7d908fd45ecd69a3`.

The browser error list was empty. A server/UI log scan found no panic, fatal,
uncaught, unhandled, or HTTP 5xx entry. The browser session, containers,
network, and named proof volumes were removed after capture.

## Provisioning observations

The proof remained fail-closed during setup:

- the default sibling FleetDB checkout lacked the exact capability surface and
  failed compatibility rather than silently falling back;
- the exact companion without the workflow-build overlay rejected prompt-agent
  registration because the Flue/SDK toolchain was unavailable; and
- the final rebuild initially exhausted Podman VM disk while downloading
  FleetDB modules. Only dangling untagged build images were pruned; tagged
  images, containers, and volumes were preserved. The next exact build and all
  product checks passed.

No live Codex backend, GitHub mutation, or paid provider was used for this
Stack 10.3 proof.

## Delivery boundary

Stack 10.3 changes Loom only; FleetDB remains pinned as the exact companion and
requires no contract change for this stack. The implementation and this
evidence record form one reviewable Loom PR. Stack 10.4 may begin only from the
resulting Stack 10.3 commit.
