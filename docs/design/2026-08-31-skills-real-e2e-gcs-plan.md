# Skills Real-Service E2E and GCS Provider Plan

Status: accepted for implementation

## Implemented publication protocol

The original single-request publication design was replaced by an actor-bound,
whole-tree upload session:

1. Loom synchronously sends the complete path/hash/size/mode manifest to Fleet.
2. Fleet persists the canonical manifest and returns one transfer grant per
   path.
3. Loom uploads each file and calls the public completion endpoint for that
   path. Fleet independently reads the provider object, verifies its exact size
   and SHA-256, and records a short-lived receipt.
4. Loom calls the public publish endpoint. Fleet atomically verifies every
   receipt and appends the immutable tree event using one Redis script or one
   PostgreSQL transaction. Publication performs no GCS reads, so a maximum
   257-file tree remains inside the configured 15-second HTTP write timeout.
5. Loom treats the command as synchronous. A `202 projection_pending` response
   triggers bounded polling until the selected tree is readable.

The old `file-uploads` and direct `file-trees` publication endpoints are not
retained. This is a strict pre-launch cutover with no compatibility or migration
path.

The GCS XML upload uses a content-addressed key, a signed payload hash, and the
signed create-only `x-goog-if-generation-match: 0` precondition. S3/MinIO use
the corresponding signed `If-None-Match: *` condition. A precondition failure
for an already-present content address proceeds only to Fleet's independent
completion verification; provider generations remain recovery metadata and are
not part of Fleet's logical identity.

Test-suite structure and edge-case accountability are defined in
[Skills Edge-Case Test Architecture](2026-08-31-skills-edge-case-test-architecture.md).

Stack placement:

- Loom base: PR #533, `test/skills-s3-release-proof`, pinned at
  `452a8770986f9c570967927532f36a002dbbaf1f`
- Loom leaf: `test/skills-real-e2e-gcs`
- Fleet test target: the exact Fleet workspace-file correctness revision selected
  by the workflow; the resolved SHA must be recorded as an artifact

## Decisions

1. Skills backed by Fleet workspace file trees are not a launched feature. There
   is no backward-compatibility requirement and no legacy Skill migration.
2. The cutover is strict. Do not add dual readers, fallback decoding, legacy
   normalization, or migration checkpoints. Development data may be recreated.
3. Correctness is proven with real processes and real backing services. Mocks of
   Fleet, Redis, PostgreSQL, object storage, HTTP transfer, or projectors do not
   count as acceptance evidence.
4. MinIO supplies deterministic S3-compatible coverage on pull requests.
5. The existing Google Cloud Storage test bucket is the production-provider
   target. GCS is exercised through the same XML/S3-compatible path used by
   Fleet in production.
6. Deterministic failure controls are allowed when the system under test remains
   real. A network proxy or a test-only failpoint may delay, drop, or fail a
   boundary; it must not replace the real implementation.
7. Loom owns the cross-repository user-journey suite. Fleet owns real-backend
   publication and recovery conformance. Both consume pinned revisions.

## Testing terminology handshake

The accepted coordinates are:

| Axis | Coordinate |
|---|---|
| Depth | Cross-repository system E2E for user journeys; real-backend integration for Fleet durability invariants |
| Realness | Real Loom, Fleet, Redis/PostgreSQL, projector, HTTP, and object store processes; no internal service mocks |
| Provisioning | Ephemeral containers for PR tests; existing dedicated GCS test bucket for provider tests |
| Polarity | Positive journeys plus deterministically induced concurrency, interruption, delay, corruption, and restart |
| Target | Strict workspace-file-tree Skill publication, visibility, selection, download, and materialization |

The public E2E seam is the Skill lifecycle exposed by Loom and Fleet APIs:

1. Import or create a Skill through Loom.
2. Publish its complete file tree through Fleet's public workspace-file API.
3. Select the published revision through the public Skill CAS API.
4. Read and materialize the Skill through Loom.
5. Assert the resulting user-visible bytes, paths, modes, metadata, revision,
   and error result.

Tests must not prove a journey by reading Redis keys, PostgreSQL tables, object
store internals, or hand-creating Fleet events. Where an invariant such as one
durable logical creation cannot be observed through the public product API,
Fleet should expose it through its owning command/query seam and test that seam
against both real storage adapters.

## Required product fixes

### 1. Atomic logical tree creation

Replace the service-level `GetFileTree` followed by generic `Append` with one
Fleet-owned, revision-keyed command. The command atomically accepts the first
creation for `(workspace, tree revision)`, preserves its provenance, and returns
the accepted result to concurrent callers and ambiguous retries.

Acceptance behavior:

- Concurrent identical publications return one accepted tree identity and one
  accepted provenance.
- A client that loses the response can retry and receive the accepted result.
- A conflicting body for an existing revision fails closed.
- Redis and PostgreSQL pass the same real-backend conformance suite.

### 2. Projection recovery

Projectors must not advance a checkpoint past a failed workspace-file-tree
event. Retryable failures remain at the current event with bounded backoff and
survive process restart. Permanently invalid events must be quarantined or
otherwise surfaced as an incomplete projection; they must never be silently
skipped while the projection reports healthy.

Acceptance behavior:

- A fail-once projection becomes visible without republishing.
- Restart during retry resumes from the failed event.
- A persistent poison event does not silently advance the checkpoint.
- Later state is not presented as a complete projection across a known hole.

### 3. Loom read-your-write publication

Fleet may truthfully return `202 projection_pending` after durable append. Loom's
`WorkspaceFileStore.Publish` contract may return success only when the tree is
readable. On `202`, the Fleet adapter polls the response location or tree query
with bounded backoff, context cancellation, and a deadline. Timeout or terminal
failure is returned as failure, not success.

Acceptance behavior:

- Delayed projection causes Loom to wait and then succeed.
- Cancellation and deadline stop polling promptly.
- A tree that never becomes visible is not reported as published.
- Retry during pending visibility does not create another logical publication.

### 4. Strict cross-repository activation

Because the feature is unlaunched, update the shared contract atomically and
delete obsolete shapes rather than maintaining compatibility. The release gate
must pin and record exact Loom, Fleet, and test-corpus revisions and run before
either strict pair is released.

There is no migration phase. Any pre-release environment containing obsolete
Skill data is reset or recreated.

## PR matrix: MinIO

Every pull request for this leaf runs these real-service matrices:

| Fleet persistence | Object provider | Required |
|---|---|---:|
| Redis | MinIO | yes |
| PostgreSQL | MinIO | yes |

Each matrix entry starts real Loom and Fleet binaries, the selected database,
the real Fleet projector, and a digest-pinned MinIO server. It uses unique
Compose project names, ports, buckets, prefixes, and process ownership records.
It must tear down only resources created by its own run.

## Cross-repository E2E scenarios

Implement these as vertical TDD slices. For each slice, first demonstrate the
failure against the real running stack, then make the smallest owning-seam
change, and finally rerun both storage matrices.

1. **Exact round trip.** Publish one root `SKILL.md` plus nested text, binary,
   executable, and zero-byte files; select it; materialize it; compare literal
   expected bytes, paths, modes, metadata, and revision.
2. **Concurrent publication.** Launch concurrent identical publications and
   observe one accepted identity/provenance through the public response/query
   contract.
3. **Delayed projection.** Pause or fail the real projector after append. Fleet
   returns pending, Loom waits, projection resumes, and the same operation
   becomes readable without republishing.
4. **Lost publication response.** Allow the real request to commit, drop the
   response at a proxy boundary, retry, and receive the original accepted
   result without a second logical creation.
5. **Projector fail-once and restart.** Inject a failure in the real projector,
   restart Fleet before recovery, and prove the tree becomes visible from the
   retained checkpoint.
6. **Persistent projection failure.** Keep the failing event active and prove
   later state is not falsely reported as a complete healthy projection.
7. **Transfer integrity.** Corrupt or truncate bytes in transit and prove Fleet
   does not publish an invalid upload and Loom does not materialize an invalid
   download.
8. **Grant enforcement.** Prove expired, tampered, wrong-method, insecure, and
   redirecting grants fail without leaking signed query parameters.

Assertions use public interfaces and independently known literals. They must not
reimplement production revision or hashing algorithms to construct the expected
answer.

## Fleet real-backend conformance

Some durability properties are owned below the cross-repository user seam.
Fleet must run the same command-level suite against real Redis and PostgreSQL:

- one revision-keyed creation under concurrent requests;
- first accepted provenance is stable;
- ambiguous retry returns the accepted creation;
- retryable projection error does not advance the checkpoint;
- restart resumes the failed event;
- poison handling makes incompleteness explicit.

These are integration tests against real adapters, not mocks. Direct adapter
inspection is permitted only inside Fleet's owning storage conformance suite,
not as evidence for Loom's E2E journey.

## GCS provider gate

The provider gate uses the existing GCS test bucket through the production
XML/S3-compatible configuration. It is release-blocking and may also be invoked
manually. It does not run for untrusted forks that cannot receive credentials.

Expected secret/configuration interface:

- `GCS_TEST_BUCKET`
- `GCS_HMAC_ACCESS_KEY_ID`
- `GCS_HMAC_SECRET_ACCESS_KEY`
- optional project/region values already required by the production adapter

Map these at the workflow boundary to Fleet's existing S3-compatible
configuration; do not leak provider credentials into Loom or test output.

Every run uses a unique prefix:

```text
loom-skills-e2e/<github-run-id>/<github-run-attempt>/<random-suffix>/
```

Safety requirements:

- Never perform bucket-wide cleanup.
- Never delete by an unresolved or empty prefix.
- Record each object and GCS generation created by the run.
- Clean up only the recorded objects/generations under the exact run prefix.
- Redact signed URL query strings, userinfo, and authorization headers from
  logs and uploaded artifacts.
- Preserve diagnostics on failure without preserving credentials.

Provider scenarios:

1. Fixed-length, single-request presigned PUT.
2. Fleet verification and publication.
3. Presigned GET followed by Loom size and SHA-256 verification.
4. Immediate read and materialization after successful publication.
5. Invalid checksum rejection.
6. Expired and tampered signed URL rejection.
7. Exact run-owned cleanup, including generations created by the run when
   bucket versioning is enabled.

Do not use SigV4 chunked transfer encoding: the supported production path is a
fixed-length request, and GCS XML interoperability does not support combining
V4 signatures with chunked transfer encoding.

## Failure-control requirements

Failure controls must manipulate boundaries without substituting fake services:

- A network proxy may delay connections, cut a committed response, truncate a
  transfer, or return a redirect.
- A test-only Fleet failpoint may pause or fail the real projector at a named
  event boundary.
- Failpoints must be disabled by default, unavailable in production builds or
  require an explicit test-mode capability, and expose no general mutation API.
- Every control must emit a run-owned activation record so a test proves that
  the intended fault actually occurred.

Sleeping and hoping for a race is not acceptable. Every concurrency or recovery
test must deterministically establish its precondition before continuing.

## CI and evidence

The MinIO Redis/PostgreSQL matrix is a required PR check. The GCS job is a
required provider/release check. Each job uploads:

- exact Loom and Fleet SHAs;
- storage and provider mode;
- image digests;
- scenario-level results and timings;
- sanitized Loom, Fleet, projector, proxy, and object-provider logs;
- cleanup result;
- the first failing public request/response with credentials redacted.

A provider credential/billing/permission problem is reported as an
infrastructure failure, never as a product pass. A skipped GCS job is not valid
release evidence.

## Initial implementation sequence

Use red-green vertical slices rather than adding the whole harness before any
behavior is exercised:

1. Extend the existing skills compatibility workflow with one real
   Redis/MinIO exact-round-trip tracer bullet.
2. Run it and retain evidence that the assertion is capable of failing.
3. Add the PostgreSQL entry for that same journey.
4. Add deterministic delayed-projection control and reproduce Loom's premature
   success before fixing read-your-write behavior.
5. Add concurrent and lost-response cases, then implement Fleet's atomic
   revision-keyed creation seam.
6. Add projector fail-once/restart cases, then repair checkpoint advancement.
7. Add the remaining integrity and grant cases.
8. Add the existing-bucket GCS release gate last, after the provider-neutral
   journey passes both local matrices.

## Definition of done

- No legacy Skill compatibility or migration code is introduced.
- Both MinIO matrices pass with real services and no internal mocks.
- Every correctness fix has a real failure reproduction that goes red before
  its implementation goes green.
- The existing GCS bucket provider gate passes through the production path.
- Concurrent retry, projection recovery, read-your-write, integrity, and grant
  scenarios are deterministic and externally observable.
- Exact revisions and sanitized failure evidence are retained by CI.
- The Loom leaf remains a clean child of PR #533 and is opened as a separate
  stacked pull request.
