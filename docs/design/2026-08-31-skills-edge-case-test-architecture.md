# Skills Edge-Case Test Architecture

Status: accepted for implementation

Related plan: [Skills Real-Service E2E and GCS Provider Plan](2026-08-31-skills-real-e2e-gcs-plan.md)

## Objective

Give every workspace-file-tree Skill edge case an explicit, reviewable test or
an explicit not-applicable disposition without turning the test suite into one
large, slow, opaque cross-repository script.

The suite must provide both:

- confidence from real Loom, Fleet, database, projector, HTTP, and object-store
  execution where those dependencies affect behavior; and
- fast, exhaustive coverage at narrower owning seams where starting the entire
  system would add cost without increasing confidence.

“All edge cases are covered” means every stable edge-case ID is assigned to the
correct test seam, backend/provider dimensions, named test, and current status.
It does not mean every case runs through every process and provider.

## Product decisions reflected by the suite

1. Fleet-backed Skills are not launched. There is no backward-compatibility or
   legacy-data migration requirement.
2. The shared Skill/tree contract cuts over strictly. Do not add dual readers,
   fallback decoding, legacy normalization, or migration-only test harnesses.
3. Redis and PostgreSQL are real persistence adapters and must pass the same
   Fleet-owned publication and projection conformance.
4. MinIO provides deterministic S3-compatible pull-request coverage.
5. The existing GCS test bucket is the production-provider release target.
6. Deterministic failpoints and a network proxy may induce failures, but they
   may not replace the real implementation under test.
7. Product assertions use public interfaces. Direct storage inspection is
   allowed only inside the owning Fleet storage-conformance seam.

## Test seams

Edge cases are assigned to the narrowest seam that owns the invariant.

| Edge-case family | Owning test seam | Required dependencies |
|---|---|---|
| Paths, Unicode, limits, and canonical identity | Loom/Fleet domain and golden-vector suites | In-process only |
| Blob identity, size, digest, and immutable object behavior | Fleet object-store conformance | Real MinIO; selected GCS release cases |
| Transfer grants and HTTP security | Fleet/Loom transfer integration | Real HTTP, proxy, MinIO or GCS |
| Logical publication, provenance, and Skill CAS | Fleet publication command conformance | Real Redis and PostgreSQL |
| Visibility, selection, download, and materialization | Loom-owned cross-repository E2E | Real Loom, Fleet, database, projector, HTTP, object store |
| Retry, restart, and projection recovery | Loom-owned failure E2E plus Fleet projector conformance | Real processes and deterministic failure controls |
| Release pairing and provider configuration | CI contract and provider gate | Pinned repositories and existing GCS bucket |
| Retention, scrub, and garbage collection | Fleet operational integration | Real database and object provider |

### Why not make all cases E2E?

An unsafe path such as `../SKILL.md`, a Unicode collision, or a maximum-file
count is owned by deterministic domain validation. Starting five processes to
exercise every spelling would be slower and less readable without testing a
different behavior. Those matrices belong in table-driven domain tests, with a
small representative subset retained in the public lifecycle E2E.

Conversely, publication races, lost responses, `202 projection_pending`, and
projector restart cannot be proven by a domain test. They require real backing
services and externally observable outcomes.

## Suite layout

### Chosen representation

The suite uses **scenario-driven executable specifications backed by a deep
real-service harness and a typed Go coverage registry**.

Each artifact has one job:

| Artifact | Responsibility |
|---|---|
| Go scenario | State the user-observable behavior being proved |
| Checked-in fixture | Make exact inputs and expected bytes reviewable |
| Harness module | Hide real-process invocation, faults, polling, evidence, and cleanup |
| Canonical Go catalog | Own all 1-95 behaviors, primary owners, seams, explicit required coordinates, and N/A decisions |
| ID-only test marker | Bind a canonical ID to one executable top-level test without repeating catalog facts |
| Generated JSON shard | Record only passing test identity and actual repository/backend/provider coordinates |
| Readiness aggregator | Merge Loom/Fleet shards and report every missing explicit coordinate |
| Design document | Record why seams, policies, and exclusions were chosen |
| TDD loop | Reproduce one behavior before making its smallest owning-seam fix |

YAML is neither the scenario language nor an authored registry. Distributed
scenarios require concurrency, cancellation, restart, and rich failure
reporting; encoding those operations in YAML would create an untyped interpreter
that is harder to understand than Go. Test-local metadata contains only an ID;
the one deliberate central semantic catalog keeps each canonical behavior,
owner, seam, decision, and minimal coordinate list in one reviewable typed row.
CI emits versioned JSON evidence from actual passing processes. Any YAML view is
generated output only.

The first reviewable implementation is deliberately small: one lifecycle
scenario, one harness implementation, one fixture family, and only the registry
entries directly covered by that scenario. New interface methods are added only
when a subsequent red-green slice demonstrates repeated mechanics.

The lifecycle scenarios move out of the Vercel corpus shell script into a
dedicated Go suite:

```text
test/skills-e2e/
├── README.md
├── coverage_test.go
├── suite_test.go
├── lifecycle_test.go
├── publication_test.go
├── projection_test.go
├── transfer_test.go
├── harness/
│   ├── environment.go
│   ├── loom.go
│   ├── faults.go
│   └── evidence.go
├── registry/
│   ├── registry.go
│   └── registry_test.go
└── testdata/
    └── exact-round-trip/
        ├── initial/
        │   ├── SKILL.md
        │   ├── assets/payload.bin.hex
        │   ├── docs/nested.txt
        │   ├── empty.dat.empty
        │   └── scripts/run.sh.executable
        ├── updated/
        │   └── ...
        └── expected.json
```

The suffixes keep otherwise opaque fixture properties visible in review. The
harness stages `.hex`, `.empty`, and `.executable` recipes as the real binary,
zero-byte, and mode-`0755` files before invoking Loom.

The existing Vercel corpus test remains focused on one question: whether the
pinned external corpus imports and materializes successfully. It does not own
the product lifecycle and failure scenarios.

## Harness module

The harness is a deep test module: a small scenario-facing interface hides
process invocation, temporary configuration, polling, path canonicalization,
log capture, evidence formatting, and cleanup.

Illustrative interface:

```go
loom := harness.Open(t)
initialSource := loom.SkillFixture("exact-round-trip/initial")
updatedSource := loom.SkillFixture("exact-round-trip/updated")

loom.SkillImport(initialSource)
initial := loom.SkillShow("exact-round-trip")

loom.SkillImport(updatedSource)
selected := loom.SkillShow("exact-round-trip")

materialized := loom.SkillMaterialize()
materialized.RequireExactTree(updatedSource, "exact-round-trip")
```

The interface must not grow one method per edge case. Scenarios compose a small
set of product actions, failure controls, and behavioral assertions.

Each scenario-facing product action maps to exactly one public command. The
harness must not combine `skill import` with an implicit `skill show`, or hide a
sequence of public operations behind a scenario-specific verb. Fixture staging
and result comparison remain behind the harness because they are test mechanics,
not product actions.

The harness invokes the real `loom` binary and Fleet HTTP interface. It does not
import internal Loom/Fleet packages to perform product operations, query Redis
or PostgreSQL as journey evidence, or hand-create events.

## Human-readable scenarios

Each behavior receives a separate top-level named test:

```text
TestSkillUpdateSelectsAndMaterializesExactRevision
TestIdenticalSkillReimportKeepsContentRevision
TestSkillContentUpdatePreservesBundledFiles
TestSkillRematerializationRemovesStaleFiles
TestSkillDeletionPrunesExistingMaterialization
TestSkillListReportsSelectedRevision
TestPublication/concurrent_publishers_receive_one_creation
TestPublication/lost_response_retry_returns_original_result
TestProjection/pending_publication_waits_until_readable
TestProjection/restart_retries_the_failed_event
TestTransfer/corrupt_download_is_not_materialized
TestGCS/presigned_round_trip
```

Names describe behavior rather than implementation or edge-case numbers. Each
test calls its typed scenario's `Covers(t)` method, which verifies that the
stable scenario ID is attached to the declared top-level test.

Successful output is concise:

```text
PASS TestSkillUpdateSelectsAndMaterializesExactRevision
PASS TestSkillRematerializationRemovesStaleFiles
```

Failures lead with the observable difference and point to retained evidence:

```text
FAIL TestSkillUpdateSelectsAndMaterializesExactRevision

scripts/run.sh:
  expected mode: 0755
  actual mode:   0644

Evidence: artifacts/skills-e2e/redis-minio/...
```

Fleet, database, proxy, and provider logs are captured as artifacts. They are
not interleaved into successful scenario output.

## Declarative fixtures and independent oracles

Initial, updated, and expected Skill trees are checked-in files rather than
repeated shell heredocs. `expected.json` records independently reviewed literal
outcomes:

- Skill metadata;
- canonical file order;
- path, media type, size, SHA-256, and executable mode;
- expected opaque tree revision where identity is under test; and
- a readable byte/hex description for binary fixtures.

Assertions must not call production hashing or manifest code to derive their
expected values at test time. Domain golden vectors and the E2E fixture manifest
share reviewed literals but do not share the production implementation.

## Executable evidence and canonical accountability

Each public scenario remains immediately above its executable E2E test. The
scenario is readable, but evidence repeats only canonical IDs:

```go
var concurrentTreePublication = registry.Scenario{
    ID:       "concurrent-tree-publication",
    Behavior: "concurrent imports select the same accepted revision",
    Cases:    []registry.EdgeCase{{ID: 50}},
}
```

Ordinary owning-package tests call `registry.MarkEvidence(t, ids...)`. A
`go test -json` parser keys pending markers by package plus top-level test and
promotes them only when that exact test passes. It rejects subtest ownership,
so a parent cannot accidentally claim coverage from one passing child.

Every shard uses `skills-edge-evidence/v2` and records repository, exact
revision, ID, package, test, and only the backend/provider actually executed.
It contains no trusted behavior, owner, seam, or intended matrix.

The paired aggregator validates shards against the canonical catalog, merges
repeated evidence across processes, and matches explicit required tuples.
Empty required dimensions mean that dimension is irrelevant, not that runtime
evidence must be empty. It retains extra truthful evidence but never lets one
coordinate satisfy a different explicit tuple. Readiness output always lists
all missing tuples deterministically and is nonzero while incomplete. Only
catalog decisions 72-77 may be N/A; there is no green unresolved disposition.

## Matrix selection

Do not run a Cartesian product of every case across every backend and provider.
Each canonical row names only the coordinates capable of changing its behavior.

Examples:

```yaml
- id: 4
  behavior: unsafe and non-canonical paths are rejected
  seam: loom-domain
  backends: []
  providers: []

- id: 64
  behavior: publish succeeds only after the tree is readable
  seam: loom-fleet-e2e
  backends: [redis, postgres]
  providers: [minio]

- id: 42
  behavior: production transfer grants require HTTPS
  seam: provider-transfer
  backends: [redis]
  providers: [minio, gcs]
```

Matrix rules:

- Domain invariants run once, exhaustively, in-process.
- Persistence semantics run against real Redis and PostgreSQL.
- Provider-neutral E2E runs against MinIO on pull requests.
- Provider-specific signing and interoperability run against the existing GCS
  test bucket as a release gate.
- A second dimension is added only when its adapter can change the behavior.

## Original edge-case families

The original 95-case review remains the stable source of case IDs. Its families
map as follows:

| IDs | Family | Primary seam |
|---:|---|---|
| 1–22 | File and manifest | Domain/golden vectors, representative lifecycle E2E |
| 23–34 | Object identity and integrity | Fleet object-store conformance |
| 35–46 | Transfer security | Transfer integration and provider gate |
| 47–60 | Publication and concurrency | Fleet publication conformance plus public E2E |
| 61–71 | Failure and visibility | Failure E2E and projector conformance |
| 72–83 | Existing data and rollout | Strict-release CI disposition |
| 84–95 | Retention and operations | Fleet operational integration |

### No-migration disposition

Cases 72–77 are `not_applicable`: the feature is unlaunched, legacy Skill data
does not require preservation, and obsolete development data may be recreated.
No compatibility reader, backfill, checkpoint, invalid-record migration report,
or migration preflight should be implemented or tested.

Case 78 remains required: runtime validation is strictly tree-only. Case 80 is
reframed as exact cross-repository revision pairing and release gating without a
migration phase. The remaining rollout cases retain their provider, evidence,
and CI responsibilities.

## Real-service definition

“No mocks” applies where a dependency affects the behavior being claimed:

- Fleet publication/projector tests use actual Redis or PostgreSQL.
- Cross-repository scenarios run actual Loom and Fleet processes.
- Transfer tests use actual HTTP requests and MinIO or GCS.
- Failure tests operate on real processes through deterministic failpoints or a
  real network proxy.

Pure domain tests do not need external processes. Their claim is deterministic
validation or identity, not distributed-system behavior.

### Case 70 remains partial

Loom snapshots the previous managed projection before replacement and restores
it after any one-shot filesystem mutation failure. If restoration itself fails,
the command surfaces both the original and rollback errors and deliberately
leaves the old marker in place, so the next invocation cannot mistake a mixed
tree for current and will reconcile it.

This is recovery hardening, not whole-projection atomicity. The public
projection spans `.agents/skills` and `.claude/skills`; they cannot be switched
with one portable filesystem operation, and rollback cannot guarantee success
under a persistent filesystem fault. Case 70 must not be emitted as covered
until both views are backed by a single atomically switched generation (or an
equivalent transactional topology) while preserving allowed unrecorded files.

A failpoint controls when a real implementation fails; it does not return a
fabricated successful result. Every fault control must provide a deterministic
activation handshake so the test proves the intended fault occurred before it
asserts recovery.

## CI lanes

1. **Domain lane:** fast table and golden-vector tests on every pull request.
2. **Fleet real-backend lane:** publication and projector conformance against
   Redis and PostgreSQL on every relevant Fleet pull request.
3. **Cross-repository MinIO lane:** Loom/Fleet user journeys against required
   persistence matrices on every relevant Loom leaf pull request.
4. **GCS provider lane:** existing-bucket production-path smoke and security
   tests as a required release check and manual diagnostic workflow.
5. **Operational lane:** retention, scrub, and reclamation integration tests
   once those Fleet modules exist.

The workflow should remain declarative:

```yaml
- name: Start real-service test environment
  run: make skills-e2e-up

- name: Run Skill E2E
  run: make skills-e2e

- name: Capture evidence
  if: always()
  run: make skills-e2e-evidence

- name: Stop test environment
  if: always()
  run: make skills-e2e-down
```

Container startup, readiness, run ownership, diagnostics, and cleanup live
behind those targets rather than growing inline workflow shell.

## Implementation order

1. Add typed coverage declarations beside executable tests; keep the remaining
   accepted dispositions in the design/backlog until their owning tests exist.
2. Create the Go harness and checked-in exact-round-trip fixtures.
3. Move the current Redis/MinIO lifecycle tracer from shell into the Go suite
   without changing its externally observed behavior.
4. Reduce the compatibility script and workflow to their owning concerns.
5. Add PostgreSQL real-backend and cross-repository matrix coverage.
6. Add one red-green vertical slice at a time for pending visibility,
   concurrent publication, ambiguous retry, projector recovery, transfer
   integrity, and grant security.
7. Add the existing-bucket GCS provider lane after provider-neutral scenarios
   pass locally.
8. Add an ID-only marker only beside a sufficient owning test. Generate v2 JSON
   from actual runtime coordinates; YAML, if requested, remains generated only.

## Definition of done

- Every stable edge-case ID has exactly one explicit disposition.
- Cases 72–77 are recorded as not applicable for the accepted strict cutover.
- Scenario files read as product behavior rather than process plumbing.
- Fixtures and expected outcomes are independently reviewable.
- No lifecycle scenario remains embedded in the Vercel corpus compatibility
  shell script.
- Real Redis and PostgreSQL cover persistence-dependent invariants.
- MinIO covers provider-neutral pull-request behavior.
- The existing GCS test bucket covers the production interoperability path.
- Failure cases are deterministic and prove their fault activation.
- CI output identifies the failed behavior before pointing to detailed logs.
- Workflow files describe orchestration policy; implementation details remain
  behind the harness and Make targets.
