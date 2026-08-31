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

The lifecycle scenarios move out of the Vercel corpus shell script into a
dedicated Go suite:

```text
test/skills-e2e/
├── README.md
├── edge-cases.yaml
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
└── testdata/
    └── exact-round-trip/
        ├── initial/
        │   ├── SKILL.md
        │   ├── assets/payload.bin
        │   ├── docs/nested.txt
        │   ├── empty.dat
        │   └── scripts/run.sh
        ├── updated/
        │   └── ...
        └── expected.json
```

The existing Vercel corpus test remains focused on one question: whether the
pinned external corpus imports and materializes successfully. It does not own
the product lifecycle and failure scenarios.

## Harness module

The harness is a deep test module: a small scenario-facing interface hides
process invocation, temporary configuration, polling, path canonicalization,
log capture, evidence formatting, and cleanup.

Illustrative interface:

```go
env := e2e.Open(t)

initial := env.ImportSkill(t, "exact-round-trip/initial")
updated := env.ImportSkill(t, "exact-round-trip/updated")
shown := env.ShowSkill(t, "exact-round-trip")
installed := env.MaterializeSkills(t)

env.RequireSkill(t, shown, "exact-round-trip/expected.json")
installed.RequireExactTree(t, "exact-round-trip", "exact-round-trip/expected.json")

env.Faults().FailProjectionOnce(t)
env.RestartFleet(t)
```

The interface must not grow one method per edge case. Scenarios compose a small
set of product actions, failure controls, and behavioral assertions.

The harness invokes the real `loom` binary and Fleet HTTP interface. It does not
import internal Loom/Fleet packages to perform product operations, query Redis
or PostgreSQL as journey evidence, or hand-create events.

## Human-readable scenarios

Each behavior receives a separate named test or subtest:

```text
TestSkillLifecycle/updates_the_selected_revision
TestSkillLifecycle/materializes_exact_bytes_and_modes
TestPublication/concurrent_publishers_receive_one_creation
TestPublication/lost_response_retry_returns_original_result
TestProjection/pending_publication_waits_until_readable
TestProjection/restart_retries_the_failed_event
TestTransfer/corrupt_download_is_not_materialized
TestGCS/presigned_round_trip
```

Names describe behavior rather than implementation or edge-case numbers. The
coverage registry maps stable IDs to these names.

Successful output is concise:

```text
PASS TestSkillLifecycle/updates_the_selected_revision
PASS TestSkillLifecycle/materializes_exact_bytes_and_modes
```

Failures lead with the observable difference and point to retained evidence:

```text
FAIL TestSkillLifecycle/materializes_exact_bytes_and_modes

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

## Edge-case coverage registry

`test/skills-e2e/edge-cases.yaml` is the authoritative accountability map. Each
entry contains:

```yaml
- id: 50
  behavior: concurrent publication creates one logical tree
  owner: fleet
  seam: fleet-publication
  test: TestTreeCreation/concurrent_identical_publish
  backends: [redis, postgres]
  providers: [minio]
  status: planned
  rationale: publication atomicity varies by persistence adapter
```

Allowed statuses are:

- `covered`: the named test exists and its required matrix is enforced;
- `planned`: accepted work with an owning seam and matrix;
- `not_applicable`: excluded by an explicit product decision, with rationale;
- `blocked`: impossible to validate until a named interface or capability
  exists, with an owner for that prerequisite.

CI validates:

- every stable edge-case ID appears exactly once;
- every entry has an owner, seam, status, and rationale;
- every `covered` test name exists;
- backend/provider values come from the canonical dimension vocabulary;
- `not_applicable` and `blocked` entries contain nonempty reasons; and
- no required matrix silently disappears from workflow configuration.

## Matrix selection

Do not run a Cartesian product of every case across every backend and provider.
Each registry entry declares only the dimensions capable of changing its
behavior.

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

1. Add the coverage registry with all 95 IDs and accepted dispositions.
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
8. Change registry entries from `planned` to `covered` only when the named test
   and required matrix are enforced in CI.

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
