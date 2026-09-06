# Active goal: browser verification and delivery

Updated at the user's request on 2026-09-05. This is the current execution scope; it supersedes the delivery order in earlier architecture plans for this wrap-up.

Finish the existing Loom/FleetDB recovery timeline patch, verify the reachable behavior through the real browser and service workflows, and publish the paired review branches with reproducible evidence and explicit remaining gaps. Do not expand the architecture in this pass.

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
