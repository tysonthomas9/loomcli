# Phase 10.2 Artifacts and Run Capture Evidence

- **Status:** Complete; packaged real-Codex execution, fail-closed evidence,
  and process-kill/restart replay are proven
- **Date:** 2026-08-13
- **Parent decision:**
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Supporting decision:**
  [Artifacts owns evidence policy](../../adr/0005-artifacts-own-evidence-policy.md)
- **Stack:** 10.2 of 10.12
- **Loom implementation commits:** `531e12e0d` (owner cutover and deletion),
  `9cc3a7b62` and `a303ab70c` (root-scoped architecture scans), `ee7a92e4a`,
  `09bdeb62d`, and `4cdfbd826` (observable generation-fenced Desktop startup),
  and `2f5d37374` plus `1b29adc0a` (bounded transient dependency recovery and
  shallow startup composition)
- **FleetDB companion commits:** `232c7d5`, `b442fc1`, `107c6fc` (projector
  circuit-open recovery), and `aec7525` (authentication failure
  classification)

## Outcome

Artifacts now owns durable evidence content, authorization, canonical format,
redaction requirements, bounds, truncation provenance, finalization, and
visible capture-failure state. Execution remains the lifecycle owner for
DriverRun and TaskRun. Interaction remains the lifecycle owner for
AgentSession and TerminalSession. Neither owner duplicates Artifact content or
policy.

`internal/app/query/runcapture` is the immutable authorized Read Projection
that groups lifecycle state with separately persisted transcript, diff, log,
report, prompt, and scrollback Artifacts. Transcript Evidence is one facet of
that projection. Durable reads never fall back to local session files.

Backend-specific decoding and mechanical secret replacement remain private
adapters. They cannot choose evidence ownership, authorization, durability,
limits, or visibility. A successful work outcome remains successful if
evidence capture fails, while the capture failure is separately durable and
visible to the operator.

The production `internal/sessions` package is deleted without a reader,
forwarder, migration, compatibility alias, or dual write. The explicit
operator-only cleanup for old on-disk session directories is private to the
existing CLI cleanup package; it never creates or reads the archive. This
deletes one production package rather than replacing Sessions with a shallow
adapter package.

## Requirement-to-proof matrix

| Required proof | Ownership and behavior | Authoritative automated coverage |
|---|---|---|
| Authorization | Artifact writes bind issuer, action, workspace, lifecycle owner, owner ID, and generation. Run Capture validates the selected lifecycle relationship before reading Artifact content. | `TestServiceAuthorityIsIssuerActionWorkspaceAndOwnerBound`, `TestSessionServiceRejectsForeignAuthorityAndGeneration`, `TestSessionServiceRejectsCrossOwnerPersistedArtifact`, `TestRunCaptureRejectsRelationshipMismatchBeforeArtifactRead`, and `TestRunCaptureHidesMismatchedSelectedTranscript` |
| Redaction | Artifacts owns the redaction requirement and the complete `loom.evidence.*` metadata namespace; runner-supplied reserved fields are removed before canonical state is written. The private mechanism returns transformed bytes only, and a redaction error fails closed before publication. | `TestEvidencePolicyOwnsRedactionAndReservedMetadata`, `TestNormalizeFailOwnsCompleteReservedMetadataNamespace`, and `TestEvidencePolicyFailsClosedWhenRedactionFails` |
| Bounds | Source intake is bounded at 64 MiB and finalized evidence at 63 MiB, leaving transport and provenance headroom. Oversized data is not allocated or published unbounded. | `TestPolicyBoundsFailClosedWithoutAllocatingLimitSizedPayloads` and the boundary cases in `evidence_policy_test.go` |
| Truncation | Text stays valid UTF-8; transcripts stay canonical JSONL; the final system event and Artifact metadata record a fixed truncation reason and original size. | `TestEvidencePolicyTruncatesTranscriptWithValidProvenance`, `TestEvidencePolicyDoesNotTruncateFinalEventThatExactlyFits`, `TestEvidencePolicyTruncatesGenericEvidenceDeterministically`, and `TestEvidencePolicyPreservesUTF8BoundaryWhenTruncatingText` |
| Finalization and replay | Content is uploaded, finalized, and owner-referenced as one retry-safe lifecycle. A replay with identical content converges; different content cannot overwrite a finalized Artifact. | `TestCreateContentUploadsFinalizesAndReferencesDeclaredArtifact`, `TestCreateContentReusesMatchingFinalizedArtifactAndCommitsReference`, `TestCreateContentRejectsDifferentContentForFinalizedArtifact`, and `TestSessionServiceOwnsRetrySafeContentLifecycle` |
| Failure state | Malformed, transport-failed, and pre-content failures produce durable sanitized `capture_failed` state. Runner-supplied evidence state cannot forge system-owned status. Work outcome and evidence outcome remain independent. | `TestCreateContentPersistsSanitizedCaptureFailureAndReplayNeverPublishes`, `TestCreateContentPersistsTransportFailureAfterDeclaration`, `TestSessionServiceRecordsPreContentCaptureFailure`, `TestHostBridgeKeepsSuccessfulWorkOutcomeWhenEvidenceCaptureFails`, and `TestHostBridgeRejectsRunnerForgedEvidenceState` |
| Restart and integrity | Run Capture reconstructs evidence from lifecycle-owner records plus finalized Artifacts after service replacement. Missing, corrupt, unavailable, truncated, and capture-failed states remain explicit. Hash or relationship mismatches never fall back to disk truth. | `TestTranscriptEvidenceSurfacesMissingUnavailableCorruptAndRestartedContent`, `TestReadEvidenceSurfacesDurableContentIntegrityFailure`, and the restart product journey below |
| UI evidence | Session history and run detail render independent transcript, diff, and scrollback states; truncation and capture failure are visible without exposing internal metadata. | `SessionRunDetail.executionEvidence.test.tsx`, `SessionHistorySection.test.tsx`, `session-history.spec.ts`, and the packaged Desktop screenshots below |
| Sessions deletion | No production import or source root may restore `internal/sessions`; session projections read owner state and Run Capture only. Ignored runtime-directory constructors/configuration, package-global session environment state, generic transcript hook dispatch, and source-compatibility aliases are also retired and guarded. | `TestRetiredHorizontalRootsCannotReturn`, `TestRetiredSourceCompatibilityAPIsCannotReturn`, `TestRetiredCompatibilitySurfacesCannotReturn`, `TestSessionProjectionNeverReadsLegacyArchive`, `TestSessionDiffReadsRunCaptureInsteadOfArtifactOrFilesystemFallback`, and the exact production-package inventory |

## Durable product proof

The signed packaged Desktop was launched with isolated data at
`/private/tmp/phase10-2-desktop-proof-20260812`. The journeys used the packaged
Loom and FleetDB sidecars and the same UI read paths used by operators.

### Finalized transcript and restart

Task `PHASE10-2-PROOF-20260812-6` completed with exit code `0`. Its finalized
Transcript Evidence contains 160 canonical entries and 38,819 bytes with
content digest
`sha256:1bff4cd18ff1b3eef88732e8c3b19e9e27f7bf64277ddf95cb775ba45808719d`.
The packaged UI rendered the transcript and, after the Desktop stack was
stopped and restarted, the same transcript endpoint returned `200` and the UI
rendered the same durable evidence.

The screenshot
`/private/tmp/phase10-2-finalized-transcript-product-proof.png` has SHA-256
`95c32a8e554c2b23ff281ae7eafe478d7a3a3905ce1c4d77303af089373e42a6`.

### Corrupt evidence fails closed

Task `PHASE10-2-PROOF-20260812-7` completed its work with exit code `0`, while
its intentionally malformed transcript was rejected as evidence. The UI
showed `EVIDENCE INCOMPLETE` and `Transcript capture failed (evidence corrupt)`
without rendering the corrupt bytes. The session projection remained
completed with `has_transcript=false`; the transcript endpoint returned `503`
with `transcript capture failed`; and the durable Artifact state was failed
with failure class `evidence_corrupt`, zero size, and no content hashes. The
malformed blob was not persisted.

The screenshot
`/private/tmp/phase10-2-corrupt-transcript-fail-closed-proof.png` has SHA-256
`f1588b3a6baa572bacce56a21b37d339202ba5da26a63651fea62c0851c55de0`.

### Final exact-source real-Codex journey

The real-Codex run was initiated entirely through the packaged Desktop UI on
Loom ancestor `4cdfbd826` with FleetDB `107c6fc`. Task
`PHASE7-FINAL-PROOF2-20260807-2` was claimed by
`agt-phase7-final2-planner-d923a4e1` and completed with exit code `0` in
3 minutes 58 seconds. The UI showed runtime `local-cli-codex`, 621.4K summary
tokens (1.1M in run detail), zero reported cost, finalized Transcript Evidence,
and an intentionally missing diff for this design-only run. The saved design
was visible and the repository was unchanged.

The initial completed-run screenshot
`/private/tmp/phase10-2-exact-source-real-codex-transcript.jpeg` has SHA-256
`6a63101dcc77c136c49da7d39fa38dfa978b8ee3716e6687a0eb8c1782d25582`.

The final app was then rebuilt and resealed from pushed Loom closure commit
`1b29adc0a` and FleetDB companion commit `aec7525`. Those closure commits are
strict descendants of the real-Codex execution commits; their additional
changes are limited to Desktop startup observability, transient recovery, and
the FleetDB projector/authentication failure paths exposed by the restart
proof. The packaged Loom reports `loom version dev (1b29adc0a)`, and deep
strict code-sign verification passes. Its executable hashes are:

| Executable | SHA-256 |
|---|---|
| Loom sidecar | `049f2f613e4bc6a2cc1d846a1cd268d3b834db2f89529c5c3e36ac132b8fcada` |
| FleetDB sidecar | `74f72a7e80e77b7b1a54bf2e6f418f7d40eeb267860b279e4a2a9a6086b4d540` |
| Desktop executable | `fddd9fe699b52d29e95372c9797051e4103ad4d8491472d0c523594539a51ce2` |
| Pinned Node.js runtime | `34c0af7cb2ba9eeb14e0675695e3f6da15fa5e98901e62149cf4fc1d594c8fa0` |

The packaged Loom and FleetDB service processes were then terminated and the
final app relaunched against the same persisted Desktop dataset. FleetDB loaded
the 851K-entry snapshot, remained alive while Redis reported an open circuit,
and retried affected projectors. Loom treated the authenticated FleetDB 503 as
transient, stayed on the visible `Starting Loom` screen, and started its server
after the dependency recovered. It did not emit the former HTTP 401
misclassification, exit the serve process, or strand the Desktop launcher.

Under replacement service/FleetDB PIDs `92676` and `92688`, the packaged UI
reopened the same task, listed one completed run, selected that run, and
rendered the finalized transcript. The restarted transcript request returned
HTTP 200 at 2026-08-13 09:33:59 PDT. The post-restart screenshot
`/private/tmp/phase10-2-exact-source-real-codex-restart-replay.jpeg` has
SHA-256
`0fe59951004287a244e2b5254b40429e21c57ea64a1f74d86e5ddf3869af4286`.

## Architecture and deletion evidence

The shrink-only production topology is now:

- 156 total production packages, down from 157 after Stack 10.1;
- 17 packages under `internal/modules`;
- 139 packages outside `internal/modules`, down from 140;
- 38 one-file packages, down from 39; and
- 60 one-or-two-file packages, unchanged from the Stack 10.1 ceiling.

The architecture checker covers all 11 build profiles and reports:

- Store files: 14 of 14 classified;
- outside-composition Store files: 0 of 0;
- legacy handler imports: 0 of 0;
- direct persistence-write rows: 82;
- capability roots: 10;
- reviewed mutations: 107;
- runtime components: 71;
- goroutine launch definitions: 78;
- performance records: 6;
- pending decisions: 0; and
- peak tree RSS: 1266.7 MiB against a 2 GiB limit in the latest exact-companion
  Go gate.

`internal/sessions`, its CLI transcript flags and finalizer, its WebUI archive
serving, notify-token path, generic transcript hook dispatcher, filesystem
fallback tests, ignored runtime-directory constructor/configuration, and
package-global session environment state are deleted. WebUI composition has
one explicit session-projection constructor over lifecycle-owner queries,
Interaction history, and Run Capture. The retired-root, file, symbol, and
package-inventory guards make restoration visible in CI.

## Repository gates

The exact companion FleetDB branch passed its complete quality gate with
Redis/Postgres projection coverage, 80.8% total coverage, the container E2E
suite, restart and Redis degradation/recovery journeys, and the harness eval:

```text
=== Quality Gate PASSED ===
```

The Loom branch at owner-cutover commit `531e12e0d`, built against that exact
FleetDB checkout and binary in a clean runtime environment, passed the
aggregate Go and frontend gate after the compatibility-surface review:

```text
=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

After hosted CI enabled gosec `G122`, the affected architecture scans were
converted from callback-path file opens to `os.OpenRoot`-scoped filesystem
reads. At pushed closure commit `9cc3a7b62`, the exact-companion `make check-go`
then passed all 16 steps, all 11 architecture profiles, the full race suite,
and the 64.0% coverage threshold. Commit `a303ab70c` applies the same rooted
scan to the remaining retired-backend check; its focused test and a fresh-cache
whole-repository golangci-lint run pass with zero issues. The hosted rerun is
the final independent check for that test-only closure commit.

At final pushed Loom closure `1b29adc0a`, `make check-go` was rerun with both
`FLEET_DB_REPO` and `FLEET_DB_BIN` pinned to companion `aec7525`. All 16 steps,
all 11 architecture profiles, the full race suite, and 64.0% coverage passed;
the measured architecture process tree peaked at 1266.7 MiB.

At final pushed FleetDB closure `aec7525`, `make gate` passed static analysis,
the unit and race suites, the real Redis pipeline suite, smoke tests, Postgres
storage contracts, archive/Postgres tests, API integration tests, the
containerized E2E suite, and the harness evaluation. Aggregate coverage was
80.8%. The longest data-service legs completed in 279.451 seconds for the real
Redis pipeline suite and 175.009 seconds for the Postgres storage contracts.
The gate ended with:

```text
=== Quality Gate PASSED ===
```

Focused proof also includes the complete frontend Vitest suite (8,741 passed,
1 skipped), the production frontend build, Artifacts/Execution/Interaction/
Run Capture/driver/WebUI Go suites, the 11-profile architecture check, and 50
consecutive passes of FleetDB's cancellation-during-catch-up regression.

## Delivery boundary

The FleetDB companion branch contains the additive durable Artifact
capture-failure contract in `232c7d5`, the clean projector-shutdown fix in
`b442fc1`, circuit-open projector recovery in `107c6fc`, and correct transient
authentication classification in `aec7525`. Stack 10.2 is complete: this
evidence record is part of the paired draft PRs, the final exact-companion
FleetDB gate has passed, and no implementation or verification work remains
within the Stack 10.2 boundary.
