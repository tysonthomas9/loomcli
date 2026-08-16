# Phase 10.5 Repository Admission Evidence

- **Status:** Implementation, aggregate gate, architecture, and packaged
  sidecar restart proof green; visual Desktop proof pending an unlocked macOS
  session
- **Stack:** 10.5 Workspace and Repository Admission
- **Loom branch:** `modular-monolith-phase10-05-repository-admission`
- **Base:** stack 10.4 at `4855b9c1c010dd5ecc3e1f525de67da49a0a4093`
- **FleetDB companion:**
  `fleet-db-modular-monolith-phase7` at
  `aec7525e` (matching canonical OpenAPI contract)

## Implemented boundary

`internal/app/repositoryadmission` now owns the transport-neutral create and
later-admission workflow, durable prepare-before-materialize ordering, exact
operation replay, owner-generation fencing, lease renewal, failure projection,
restart recovery, status lookup, runtime registration, and shutdown.

`internal/infra/repositoryadmission` is its private local adapter. It owns the
FleetDB transport, exact local recovery journal, filesystem placement, Git
worktrees and checkout receipts, local-state publication, rollback, and prompt
bootstrap. Delivery composes the workflow through the narrow
`internal/cli/serve/opsimpl.RepositoryAdmission` facade; it does not receive
FleetDB, journal, Git, or filesystem mechanisms.

The process-local WebUI job store and the former
`internal/cli/serve/workspacemgr` production package are deleted. The WebUI
polls the durable admission identifier and projects only FleetDB-owned state.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| Create plus repository admission | `TestWorkflowPersistsBeforeMaterializationAndProjectsOnlyDurableStatus` proves durable prepare precedes local materialization and completion is projected from the durable record. | Green |
| Later repository admission | `TestWorkflowLaterAdmissionReplaysCommittedResultWithoutRematerializing` exercises the production `StartAddRepositories` application interface. | Green |
| Exact replay | The later-admission test proves stable admission identity and one materialization; `TestRepositoryAdmissionPlanCreateAllowsOnlyExactProtectedRecovery`, `TestRepositoryAdmissionPlanAddReplaysOriginalNamesOnlyForExactProtectedIntent`, and `TestLocalJournalReplaysExactIntentAndRejectsDivergence` reject divergent recovery. | Green |
| Concurrent admission | `TestWorkflowConcurrentExactAdmissionMaterializesOnce` and the cross-journal materialization-lock tests prove one materializer for one exact operation and canonical target. | Green |
| Restart and crash recovery | `TestWorkflowRecoveryClaimsExpiredOwnerAndCompletesProtectedIntent`, `TestWorkflowRecoveryReconstructsDefinitivelyMissingDurableCoordinates`, `TestLocalJournalSurvivesReloadAndBindsExactlyOnce`, and the exact recovery/renew response tests prove durable owner replacement, protected-intent resume, and CAS rebinding after definitive durable-coordinate loss. The packaged sidecar proof below repeats the same loss and reconstruction with two real Git repositories. | Green |
| Cleanup | `TestPersistAddReposRecordsRollsBackClonedCheckoutOnLocalStateFailure` proves checkout and catalog rollback; `TestCreateEmptyWorkspaceRollsBackNewRootAndCatalogOnBootstrapFailure` proves operation-created workspace-root and durable catalog rollback. | Green |
| Failure | `TestWorkflowFailureIsProjectedFromDurableAdmission` proves the terminal UI projection is derived from durable failure state. Corrupt journals and credential-bearing clone sources fail closed in the infra journal suite. | Green |
| Durable UI polling | WebUI coordinator tests prove the job ID is the exact durable admission ID and lookup does not infer status from Workspace lifecycle. Create/Add Repo modal tests cover progress, completion, terminal failure, and connection loss. The packaged sidecar kept all three original job URLs resolvable after normal restart and after Fleet snapshot loss. | Green in component and packaged-runtime tests; visual Desktop pending |
| Retired process-local paths | `TestPhase10RepositoryAdmissionOwnsWorkflowAndProcessLocalJobsStayDeleted`, control-plane guards, and the architecture inventories prevent the job store and `workspacemgr` production path from returning. | Green |
| Packaged product journey | The exact release bundle's Loom/FleetDB sidecars proved create-plus-admit, later add, durable completion, negative failure, normal restart, deliberate Fleet snapshot loss, reconstruction, old-job alias lookup, and exact replay. The same state still requires visible confirmation through the packaged Desktop UI. | Runtime green; visual check pending macOS unlock |

## Verification commands

The matching FleetDB checkout is required because the sibling
`fleet-db/unified-agents` checkout predates the Phase 10 contract.

```bash
TMPDIR=/tmp/loom-go-tmp \
GOCACHE=/tmp/loom-go-build \
GOTMPDIR=/tmp/loom-go-tmp \
go test -race \
  ./internal/app/repositoryadmission \
  ./internal/infra/repositoryadmission \
  ./test/repositoryadmission \
  -count=1
```

Result: all three packages passed with the race detector.

```bash
TMPDIR=/tmp/loom-go-tmp \
GOCACHE=/tmp/loom-go-build \
GOTMPDIR=/tmp/loom-go-tmp \
FLEET_DB_REPO=../fleet-db-modular-monolith-phase7 \
CHECK_GO_SKIP_ARCHITECTURE=1 \
make gate
```

Result: Go and frontend quality gates passed. The Go lane covered the 16
repository checks, race and coverage; the frontend lane covered format,
typecheck/build, lint, architecture, generated-code staleness, and tests. The
first sandboxed attempt reached the pinned OpenAPI generator and was blocked
from resolving `proxy.golang.org`; the otherwise identical network-enabled run
passed.

```bash
TMPDIR=/tmp/loom-go-tmp \
GOCACHE=/tmp/loom-go-build \
GOTMPDIR=/tmp/loom-go-tmp \
make check-architecture
```

Result:

- composite `Store` files: `12/12`;
- outside composition: `0/0`;
- legacy handler imports: `0/0`;
- reviewed persistence-write rows: `77`;
- capability module roots: `10`;
- reviewed mutation commands: `107`;
- named runtime components: `71`;
- goroutine launch definitions: `77`;
- measured performance records: `6/6`;
- pending architecture decisions: `0`;
- build profiles: `11/11`; and
- peak process-tree RSS: `1222.9 MiB` of the `2048 MiB` limit.

The new `Journal.Rebind` CAS operation is explicitly classified as mutating.
The source-backed inventory remains `77` write rows and increases the existing
`RepositoryAdmissionJournal.withLockedFile` row from six to seven sites; the
strict site ratchet is now `93`. The measured architecture command remains a
separate required proof rather than being skipped implicitly.

## Packaged Desktop proof record

The release bundle built, resealed, and passed strict code-signature
verification with the matching FleetDB sidecar using:

```bash
cd desktop
FLEET_DB_REPO=../../fleet-db-modular-monolith-phase7 npm run refresh:app
```

The bundle is
`desktop/src-tauri/target/release/bundle/macos/Loom Agents.app`. Its embedded
Loom SHA-256 is
`cee6aaea39668c58c1e6e1bb98859f11d7624ab66772647873b50350cf3ba61e`;
its FleetDB SHA-256 is
`74f72a7e80e77b7b1a54bf2e6f418f7d40eeb267860b279e4a2a9a6086b4d540`.
The refresh script's final LaunchServices `open` step returned
`kLSNoExecutableErr`; the executable is present, `CFBundleExecutable` points to
it, and `codesign --verify --deep --strict` passes. The proof therefore started
the exact packaged Loom sidecar directly instead of treating that failed `open`
as a Desktop launch.

The product-proof handshake was:

- **depth:** packaged release sidecars and bundled WebUI;
- **realness:** real embedded FleetDB plus real public Git clones, with no
  paid model call required for Repository Admission;
- **provisioning:** a uniquely owned
  `/tmp/loom-phase10-5-proof-resume-20260815-1` data directory;
- **polarity:** successful create/add/replay plus a deliberately missing
  repository and deliberate durable-state loss; and
- **target:** the Stack 10.5 Repository Admission process manager.

The runtime created workspace `PHASE10-5-PACKAGED-RECOVERY-2026` from
`octocat/Hello-World`, attached `octocat/Spoon-Knife`, and rejected
`octocat/phase10-5-definitely-missing` without adding a third repository. The
accepted job IDs were respectively
`62a8a1bc7bb46c2ca45661bca4c12098`,
`9c98da1842ea5402d7698e0d00d73e8d`, and
`055c12a8cc743e7df03f0ec5a68b8b3a`. All three remained queryable after a
normal stop/start.

For the crash/loss proof, both files named `fleet-db/redis-snapshot.json` and
`fleet-db/redis-snapshot.json.bak` were moved aside while the protected local
journal, workspace cache, and checkouts were preserved. On restart the create
admission was reconstructed first, the successful later admission followed,
and the failed later admission remained failed. The journal retained each old
admission ID as an alias to its new exact durable coordinate. The two old
successful job URLs returned `done`, the failed URL returned `failed`, the
workspace exposed exactly `hello-world` and `spoon-knife`, and replaying the
Spoon-Knife request returned recovered admission
`b221d68ebadcca69b19bc215f301c81f` without duplicating the repository.

The user's long-lived default Desktop data remains a separate platform issue,
not part of this clean proof. Its Fleet snapshot had grown to about 967,000
keys and repeatedly opened FleetDB's Redis circuit breaker during startup. The
earlier snapshot inspection attributed 927,207 of those keys to unbounded
automation delivery/cron claim replay records, and snapshot dumps exceeded
their time budgets. Stack 10.5 now reconstructs its own exact saga after
definitive durable-coordinate loss; it does not claim to fix that replay-key
retention defect.

The visual Desktop actions and screenshots remain intentionally unrecorded
because Computer Use reports that macOS is locked. Build and packaged-sidecar
output are not being substituted for the required visible UI proof.
