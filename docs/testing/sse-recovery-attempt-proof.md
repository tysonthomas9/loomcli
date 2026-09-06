# Browser recovery attempt ownership

An expired-cursor offer previously started ordinary query refresh while the transport immediately retried the same expired checkpoint. EventProvider now synchronously suspends the exact SSE client and starts one cancellable native issue-recovery read. The transport lease invalidates the old parser generation before asynchronous work begins; later frames, including frames already buffered in the same response, cannot advance its accepted checkpoint.

The attempt retains prepared native data privately. It exposes only idle, reading, prepared, or failed status through EventContext. It has no snapshot getter, cache publication operation, or checkpoint-reset operation. A prepared result is therefore not successful whole-client recovery. Expiry releases prepared data and leaves the transport suspended; explicit retry revokes the attempt and reconnects from the old accepted checkpoint. Failed reads do not create an automatic HTTP retry storm.

Workspace, repository, credential, manual connection, sign-out and unmount transitions revoke ownership. Credential generation changes on actual token replacement, including one authenticated credential replacing another; observers receive only a generation number. Reentrant observers cannot deliver an older generation after a newer one. The client and coordinator are installed at React commit boundaries. Mutation and resync fan-out recheck ownership between subscribers so a synchronous retry or scope transition stops obsolete dispatch.

Ordinary registered-query refresh remains available for visible data, but its completion cannot publish the private snapshot or reset the checkpoint. It is canceled on sign-out and scope transitions. This change does not add all-view recovery certification, a new user-facing recovery workflow, or automatic resume after snapshot preparation.

## Remaining publication requirements

Before adding reset acceptance, establish complete coverage for graph/dependency views, selected details and history, blocked and dormant filtered caches, and externally backed views. One publication generation must fence ordinary reads, unresolved commands, optimistic writes and time-derived membership. Only then can an exact prepared boundary become a browser checkpoint. The existing short-lived server handle and source identity are prerequisites, not sufficient acknowledgment proof.

## Validation

Focused transport tests use the real fetch-event-source parser with controlled HTTP streams, including ignored abort and buffered-frame cases. Provider tests use the actual transport client and attempt controller with a mocked stream-library boundary and deferred recovery reader. These prove orchestration and ownership; they are not a running paired Loom/Fleet browser proof. Dedicated controller tests cover late ignored cancellation, replacement, reentrant callbacks, expiry after preparation and bounded timers. Credential tests cover non-null replacement, same-token stability, listener exceptions, unsubscribe and reentrant replacement.

Final command results are recorded in the draft PR and logs under `/private/tmp/sse-stack-review/recovery-attempt-*`. No merge, deployment, snapshot publication or checkpoint acknowledgment is included.

Local validation: `make check-frontend` passed all six stages, with 426 test files and 9,261 tests passing (81.68% statement coverage). A harmless cleanup-ref ESLint warning was subsequently removed by capturing the stable ref object; final scoped ESLint passed and all 43 provider regressions passed on the frozen source. `npm run build` passed in 2.56s (existing chunk-size advisory). The gate used deterministic browser environments, not a running paired stack.
