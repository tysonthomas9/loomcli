# Hub admission order and the remaining source frontier

## Reproduced defect and repair

`Hub.Broadcast` previously sent directly to the broadcast channel whenever it
had capacity, even if older accepted events remained in `retryQueue`. The
100ms retry drain could therefore deliver B before A. Client delivery sequence
numbers are assigned at fanout, so they recorded that inverted order.

Broadcast admission now shares `retryMu` with retry draining. New offers append
to an existing backlog even when fast-channel capacity becomes available. The
channel and retry suffix form one FIFO in admission order. Neither admission
nor draining blocks on channel capacity. Bounds and explicit drop/resync behavior
remain in place. Concurrent producers are ordered by admission under the mutex,
not by wall-clock call start or by an inferred ordering of opaque cursor strings.

`TestHub_NewBroadcastCannotOvertakeRetryBacklog` deterministically failed before
the repair: it delivered `c1.newer` while expecting `c1.older`. A second regression
covers partial retry draining followed by a new offer. Existing saturation and
workspace/repository-scoped resync tests remain part of the race package.

## Required source frontier

This repair cannot certify source order across replay and live delivery. The
handler reads replay separately while the subscriber broadcasts live payloads.
Its authenticated head is currently traced, not used as a handoff fence.
A delayed durable payload can therefore still regress a newer replay checkpoint;
choosing the largest client delivery sequence during overflow does not solve it.

The independently vetted next design is one authoritative page chain per SSE
connection:

1. Register before reading. Durable broadcasts wake the reader rather than
   carrying authoritative mutation IDs into the writer.
2. Read initial catch-up and subsequent pages through the same connection cursor.
   Emit ordered mutations and filtered checkpoints only from that page chain.
3. Coalesce wakeups with a pending generation: a wakeup during the final empty
   read must cause another read. Bounded periodic reconciliation covers lost
   notifications and process-local delivery gaps.
4. Keep transient notifications separate and cursorless. Overflow schedules an
   authoritative reread; discarded payloads cannot select a replacement cursor.
5. Preserve bounded replay and explicit resync. Advancing past undispatched
   records requires a defined authoritative snapshot/refetch fence, including
   what happens when that refetch fails.
6. Use FleetDB's committed projection prefix as the eventual page source. Raw
   append order alone does not prove projected query effects are visible.

Current seams are `subscription.Module` handler wiring,
`BackendMutationSubscriber.GetMutationPage`, `MultiWorkspaceSubscriber` workspace
routing, and `realtime.Handler.fetchCatchUp`/`streamLoop`. FleetDB's internal
`ReadCommittedProjectionPage` exists in the projection stack, but production
manager/public feed integration is still pending. A wakeup-only transport must
not be called projection-safe until that source integration is proved.

Required regressions include replay through B with delayed A, durable overflow
followed by transient traffic, a commit during final-read/wakeup handoff,
disconnect after every emitted checkpoint with exact suffix resume, and retention
expiry plus failed snapshot recovery without an uncertified cursor advance.
This document records that required work; this PR implements only hub admission
ordering. It supplies no paired browser or storage restart evidence.

## Recorded validation

Realtime and app packages passed with `go test -race -p 1` (5.137s and
13.771s). Scoped Go lint reported zero issues. Independent source review found
no blocker or new lock inversion. These are deterministic/local HTTP checks.
