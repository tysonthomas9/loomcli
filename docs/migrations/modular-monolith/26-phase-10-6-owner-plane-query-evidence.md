# Phase 10.6 Owner-Plane Deletion and Query Evidence

- **Status:** Source, architecture, Loom gates, companion FleetDB gates, and
  packaged Desktop restart/read-surface proof green
- **Stack:** 10.6 Owner-plane deletion and `app/query`
- **Loom branch:** `modular-monolith-phase10-06-owner-plane-query`
- **Base:** stack 10.5 at `b032bb049`
- **FleetDB companion:** `fleet-db-modular-monolith-phase7` at `aec7525e`

## Implemented boundary

The production `internal/domain`, `internal/store`, and `internal/ops` planes
are deleted. Their state, records, command/query ports, and persistence
contracts now live with the owning Agents, Automation, Execution, Interaction,
Workflow Catalog, or Workspace module. FleetDB and test-only memory adapters
map transport and storage details at those owner seams; no forwarding package,
type alias, composite Store fallback, or generic Ops facade remains.

`internal/app/query/operationalview` owns the immutable Workspace topology
projection used by delivery and local Source Control mechanics. Its topology
surface is four read-only operations: `Active`, `ByKey`, `ResolveKey`, and
`Paths`. It joins Workspace-owned records with machine-local placement without
exposing persistence ports or mutation authority. Returned repository groups,
paths, summaries, and nested slices are fresh projections, so callers cannot
mutate owner records through the view.

The same package owns a separate `WorkspaceRosterQuery`. It joins
Workspace-owned records with Agents-owned Agent and Role records for the
workspace sidebar. It is intentionally not cached with topology: agent
creation, archival, and runtime metadata must be visible on the next read.
The roster query validates owner state, sorts deterministically, and returns
defensive copies.

The former operational query packages were folded into their deep owner roots.
Execution now owns run audit projection and its record contracts; Workspace
owns its record adapters; Agents, Automation, and Interaction own their
remaining persistence records. The shared `internal/platform/persistence`
package contains backend-format error classes but no data access, transaction,
repository, or product policy. Owner modules cannot import that backend
vocabulary except in the Workspace mapping adapter; an architecture guard
enforces this. Remaining delivery and legacy direct-record callers are retired
in stack 10.11 rather than being reclassified as owner semantics.

The packaged proof exposed a real FleetDB adapter regression: the moved
Execution `WorkerNode` record had lost its snake-case JSON tags, so a real
FleetDB response decoded into an empty owner record and failed the owner
envelope check. The tags are restored. The adapter test now serves a literal
snake-case payload instead of serializing the same Go type on both sides, and
`owner_wire_contract_test.go` reflection-checks all 22 owner records decoded
directly from FleetDB responses for explicit wire names.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| Owner-local state and ports | Owner API, adapter, and conformance suites across Agents, Automation, Execution, Interaction, Workflow Catalog, and Workspace pass under race. | Green |
| Cross-owner immutable projection | `workspace_query_test.go` covers active/by-key/name resolution, placement, stable ordering, error propagation, and defensive copies. `workspace_roster_query_test.go` covers the dynamic Agent/Role join, sorting, invalid owner state, explicit empty results, and defensive copies. | Green |
| Delivery delegation | WebUI application, workspace handler, store-adapter, and workspace-coordinator suites pass; the WebUI receives the immutable projection rather than constructing Workspace/repository topology itself. | Green |
| FleetDB owner wire mapping | The control-plane adapter decodes a literal snake-case WorkerNode response, all 22 directly decoded owner response records require explicit JSON tags, and the Execution envelope tests pass. | Green |
| Horizontal-plane deletion | `TestRetiredHorizontalRootsCannotReturn` requires all three retired roots to be absent and rejects every production or test import. | Green |
| Owner error vocabulary | `TestCapabilityOwnersDoNotExposeBackendErrorVocabulary` rejects platform persistence errors from owner modules except the explicit Workspace backend-to-owner mapping adapter. | Green |
| No replacement mega-facade | Architecture profiles, direct-write classification, import fanout, package-shape ratchets, and the zero composite-Store inventory reject a renamed Store/Ops/service/repository facade. | Green |
| Deep owner package shape | Owner roots use a 40-file ceiling while every other and nested adapter package retains the strict 25-file ceiling. Script tests prove 37 passes, 41 fails, and a 26-file nested adapter still fails. | Green |
| Product UI reads | Computer Use verified the exact packaged Desktop bundle after stop/start: workspace, repository, agent roster, Agent run surface, and workflow information all persisted and rendered. A bound-workspace restart registered `1/1` workspaces with zero worker-envelope or registration failures. | Green |

## Architecture result

The measured architecture transaction passed with peak process-tree RSS
`1210.6 MiB` against the `2048 MiB` limit:

- composite Store files: `0/0`;
- outside-composition Store use: `0/0`;
- legacy handler imports: `0/0`;
- reviewed persistence-write rows: `71` across `85` sites;
- capability module roots: `10`;
- production package topology: `153` total, `18` under `internal/modules`,
  `135` outside module roots, `37` one-file, and `55` one-or-two-file;
- reviewed mutation commands: `107`;
- named runtime components: `71`;
- in-scope non-test goroutine launch definitions: `74`;
- measured performance records: `6/6`;
- pending architecture decisions: `0`; and
- build profiles: `11/11`.

The package-size rule is class-based rather than an owner allowlist. Deep
capability roots may hold up to 40 cohesive implementation files; nested
adapters and all other packages remain capped at 25. This preserves pressure
against shallow fragmentation while allowing an owner root to hide substantial
policy behind a narrow interface.

## Loom verification

The Go gate was executed as its measured architecture transaction, static
stages, race transaction, and coverage transaction. The host had only about
3 GiB free, so the race package suffix was repeated in two bounded groups after
the monolithic linker exhausted disk. Every package passed under race, and the
complete non-race coverage transaction passed at `62.9%` against the `60%`
threshold. The paired FleetDB source and binary were explicitly pinned for all
runtime-aware tests.

The frontend format/typecheck/build/lint/architecture/generated-code and full
unit gate passed. A pre-existing timing assumption in the terminal replay test
was exposed under the full suite: the fixture asserted after about 42 ms even
though the production visibility retry is deliberately 50 ms. The test now
waits for the callback contract. The focused test then passed 30 consecutive
runs and the full frontend gate passed.

The Go race gate similarly exposed a host-global tmux fixture. Tests changed
the global `remain-on-exit` option to preserve a pane after an absent `loom`
binary exited, allowing another package process to restore the option before
inspection. The fixture now installs a test-owned blocking `loom` executable
and never changes global tmux state. The failing Claude case passed 20
race-enabled repetitions, the full Automode race package passed, and the same
package passed again inside the repository race transaction.

The generated OpenAPI staleness check passed after the sandboxed attempt was
allowed to resolve its pinned generator. `golangci-lint` reported zero issues.
The package-size script suite passed `16/16`; the import-fanout script suite
passed `20/20`.

## Companion FleetDB verification

The companion checkout is unchanged and clean. Its gate was reproduced in
safe stages because the stock local target combines whole-repository race and
`coverpkg` instrumentation beyond the host's available disk, and then issues a
broad `podman rm` for every container named `reaper`. No foreign container was
stopped or removed.

The equivalent staged result is green:

- build, vet, dead-code/key-construction checks, harness lint, and
  `golangci-lint` (zero issues);
- every package under the race detector;
- build-pipeline and smoke integration in the repository's documented CI mode,
  which skips only nested unit/lint reruns already proven separately;
- Redis/Postgres storage and archive contracts plus the integration API suite
  against the active Podman socket with Ryuk disabled;
- merged coverage `80.7%`, with all `28` enforced packages above the `50%`
  floor;
- full E2E in `108.515s`, including authorization, SSE fan-out, compaction,
  crash recovery, Redis restart, and workspace-format restart persistence; and
- harness evaluation `32/32`, score `100`, with zero blocker failures.

Podman preflight and postflight checks identified the pre-existing Loom stacks
and left them untouched. The contract tests and E2E left no test-owned
containers behind. The paired `fleet-db` and `fdb` binaries were rebuilt after
the pipeline's intentional clean step.

## Packaged Desktop proof record

The exact release bundle at
`desktop/src-tauri/target/release/bundle/macos/Loom Agents.app` was rebuilt from
the current source, including all six bundled workflows, the Loom sidecar, the
paired FleetDB sidecar, and the Tauri shell. Its rebuilt Loom sidecar SHA-256 is
`0fba5f33795cd3c2564b3e327e922bc146717ba70722a688208ba04c105a1c66`.
For unambiguous Computer Use targeting, the sealed bundle was copied to the
run-owned `/private/tmp/phase10-06-Loom-Agents.app` and launched with both
`LOOM_DESKTOP_DATA_DIR` and `LOOM_CONFIG_DIR` set to
`/tmp/phase10-06-desktop-data`. No default local-mode stack, browser profile,
or persistent Desktop data was reused or stopped.

The first UI journey created `PHASE10-06-PROOF`, admitted the `loomcli`
Repository Reference, and created the enabled autonomous `phase10-reader`
Agent bound to `internal.task.ready`. It intentionally created no tasks and ran
no model workload. Computer Use then verified the Workspace sidebar, Agent run
surface, and workflow information surface.

After a full local-runtime stop/start, the workspace, Repository Reference,
Agent, trigger, and active workflow version all remained visible. The prior
local checkout had been removed independently, so startup correctly preserved
the durable topology while reporting the local placement as unbound. A
test-owned minimal Git checkout with the recorded origin was restored at the
deterministic workspace path; self-heal re-bound it, and a second packaged
restart reported `total_workspaces=1 registered=1`. From that restart onward,
the log contained `0` `worker node escaped requested envelope` conflicts and
`0` `register task worker node` failures.

Computer Use screenshots (PNG, SHA-256):

- `/private/tmp/phase10-6-bound-restart-proof.png` —
  `0ade21b2f5e171e148b4d5205835b45c3329b97c4deee119d012d3c6a91240d9`;
- `/private/tmp/phase10-6-restart-agent-proof.png` —
  `d8f609322cd420ae2eaebac156714bb00225ded41a329a976816fee00ad98686`;
- `/private/tmp/phase10-6-workflow-read-surface.png` —
  `411cbfe2662c41b81f896f53379732ebcf1217122619d2d90dedf44fe0e76cde`.

The isolated packaged runtime was stopped after proof. The user's pre-existing
Desktop and local-mode processes were left untouched.
