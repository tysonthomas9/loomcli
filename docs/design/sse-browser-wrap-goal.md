# Active goal: browser verification and delivery

Updated at the user's request on 2026-09-06. This is the current execution scope; it supersedes the delivery order in earlier architecture plans for this wrap-up.

The immediate objective is to finish browser verification of the existing paired SSE/projection changes and deliver reviewable draft PRs with reproducible evidence. Keep the broader architecture goal active, but do not expand this delivery slice into whole-client recovery or a transport migration.

The v6 timeline pair (Loom #679 / FleetDB #285) and workspace bootstrap prerequisites (Loom #680 / FleetDB #287) are published. Their earlier browser evidence and cleanup are recorded in [the bootstrap proof](../testing/postgres-sse-browser-proof.md).

## Current progress and next actions

- Browser verification exposed two test weaknesses: an obsolete connection indicator and reconnect tests that could recover through page reload. The rewritten tests passively observe the application's actual Fetch SSE frames, correlate scoped mutation IDs with rendered changes, and require reconnect on the captured Last-Event-ID without reloading. Normal projection refreshes remain part of the production contract.
- Real browser status changes exposed HTTP500 on enrolled PostgreSQL public issue claims. The paired FleetDB patch routes claim, release and lock-only release through committed command ownership. Independent source review is complete. Focused service, build, lint and harness checks passed; the final deleted-issue cleanup edit still needs its PostgreSQL integration rerun.
- Four intermediate browser cases passed: connection, create, rapid creates and close. The final seven-test suite, including two-client delivery and a real proxy interruption, has not passed on the final source. See [the regression proof](../testing/postgres-sse-regression-proof.md) for exact evidence and limitations.
- Restore runtime access only after approval to restart the shared Podman VM. It reports running but rejects SSH after disk exhaustion; the PostgreSQL fixture reported an I/O error. The restart approval is pending. Cleanup of the current owned project `loomcli-pg-browser-regression-0905` is also pending; prior bootstrap cleanup does not cover this project.
- Rebuild the paired final source, rerun the PostgreSQL HTTP integration test and `make local-mode-postgres-sse-verify`, inspect browser artifacts, then clean up only run-owned resources. Publish the current branches `test/pg-sse-regressions` and `feat/pg-public-issue-routing` as dependent draft PRs, recording runtime checks as unverified until they actually pass. Keep FleetDB #286 aligned with the evidence.

The wrap-up is complete only when the paired delivery is published, final verification outcomes and remaining gaps are documented, and owned-resource cleanup is accounted for. An unavailable runtime is an explicit verification gap, not a successful test. The broader architecture objective remains unfinished.

Published dependent drafts: [Loom #682](https://github.com/tysonthomas9/loomcli/pull/682) (implementation/evidence commit `4f147037f`) and [FleetDB #288](https://github.com/BrowserOperator/fleet-db/pull/288) (implementation commit `c0298005`). Final runtime verification remains pending as described above.

## Acceptance criteria

1. Finish and independently vet the current v6 timeline contract and permission checks. Run the relevant regression, build and repository gates.
2. Use isolated, run-owned services and a dedicated browser profile. Exercise the current fetch-based SSE transport through the real product workflow; create any test data through supported product APIs or UI. Do not substitute fixture-backed browser responses for paired-service evidence.
3. Check initial rendering and selected history, mutation-driven updates, reconnect behavior, and workspace/selection changes where the runtime supports them. Record observed UI, request/cursor behavior and screenshots. Mark scenarios that cannot be reached as unverified with the concrete reason.
4. Keep prepared recovery data private and preserve the accepted checkpoint. Do not enable reset acknowledgment or claim whole-client recovery without the missing publication and coverage guarantees.
5. Document the exact branches, tested revisions, commands, results, browser evidence and remaining work. Push both branches and create paired draft PRs. Do not merge or deploy.
6. Clean up only resources created for this verification. Finish with a concise, self-contained handoff.

## Deferred architecture work

The broader streaming/projection objective remains unfinished. Whole-client atomic cache publication and exact acknowledgment/resume, external-view coverage, historical enrollment/rebuild, remaining public command/lifecycle routing, Redis correctness proofs and executable cross-repository CI gates remain tracked in the earlier architecture and FleetDB progress documents. They are follow-up work, not reasons to keep expanding this patch.

## Goal tracker limitation

The session goal remains active. Its tool supports only completion or blocking, not editing the objective text. This document records the user-directed scope change without incorrectly marking the original architecture objective complete.
