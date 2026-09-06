# Active goal: browser verification and delivery

Updated at the user's request on 2026-09-06. This is the current execution scope; it supersedes the delivery order in earlier architecture plans for this wrap-up.

The immediate objective is to finish browser verification of the existing paired SSE/projection changes and deliver reviewable draft PRs with reproducible evidence. Keep the broader architecture goal active, but do not expand this delivery slice into whole-client recovery or a transport migration.

The v6 timeline pair (Loom #679 / FleetDB #285) and workspace bootstrap prerequisites (Loom #680 / FleetDB #287) are published. Their earlier browser evidence and cleanup are recorded in [the bootstrap proof](../testing/postgres-sse-browser-proof.md).

## Completed browser delivery slice

The final paired source passed all seven real PostgreSQL browser regressions in
32.2s, with zero skips, retries or flaky results. Independent review verified the
actual two-client replay traces, exact cursor reuse and ordered IDs. Manual UI
create/status/review/close also matched API state. See [the final proof](../testing/postgres-sse-regression-proof.md).

Published dependent drafts are [Loom #682](https://github.com/tysonthomas9/loomcli/pull/682)
and [FleetDB #288](https://github.com/BrowserOperator/fleet-db/pull/288). Tested source
commits are `c9267da8e` and `a82180b2`. The real PostgreSQL HTTP regression and
focused static/service/harness gates passed after correcting late lease cleanup.

The approved VM restart restored runtime access. The owned browser project,
volumes and dedicated profile are cleaned up. The separate PostgreSQL proof
fixture is stopped with its data preserved, and the shared VM remains running.
Earlier pending-runtime notes are superseded by this evidence. Final hosted CI
and the broader architecture requirements remain separate; no merge or deployment
is included in this delivery.

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

The goal tool supports completion or blocking, not editing objective text or reactivating a blocked tracker. Restoring the runtime and finishing this delivery slice do not complete the original architecture objective. The remaining scope above is preserved.
