# Committed projection runtime integration remaining

This is implementation work still required, not a production guarantee. The
Fleet PostgreSQL branch currently has committed applier/receipt primitives,
but production services and the background manager still use the legacy
projector. Public mutation reads still read raw source events. Changing wakeup
notifications alone cannot close that gap.

## Required runtime transition

1. Select committed processing using persisted workspace/incarnation/anchor
   enrollment. Invalid selection must fail rather than fall back to raw
   delivery. Existing enrollment assumes trusted fresh history; it does not
   certify an already materialized workspace or provide its migration proof.
2. Route inline and background projection through ApplyNext. Inline calls
   drain canonical source order through their event, rather than applying the
   supplied payload directly. A failed predecessor remains a barrier.
3. Give the mutation endpoint a committed reader: head is the applied prefix,
   and pages use ReadCommittedProjectionPage. Notifications are wakeups for
   authoritative rereads, with periodic rereads covering missed notifications.
4. Reject unsupported commands before source append. Expose blocked-lane
   status and the failing action/event; never skip that event.
5. Preserve current enrollment restrictions on source retention and workspace
   lifecycle until their protocols are implemented and verified.

Seven action contracts remain gated: issue.claim/release, workspace.update/
delete, driver_run.suspend/resume, and role.delete. Workspace bootstrap is
constrained. Driver/task command events require transaction-bound command
evidence; legacy events without evidence cannot simply be replayed as valid.
These gaps block unrestricted cutover, not further implementation.

## Required proof

Use a fresh, explicitly enrolled workspace for the first production-path test.
Through the public mutation endpoint prove failed-predecessor suppression,
competing inline/background workers, process restart, and commit-before-wakeup
loss. Then complete the gated actions, migration/enrollment contract, database
server restart, and paired Loom browser recovery. Keep exact branch/commit
evidence and distinguish these classes from deterministic frontend tests.

Loom must then capture the committed fence before recovery queries, reread all
required participants, and acknowledge retained cursor reset only after success.
A retention expiry during recovery invalidates the attempt. A fixed upper replay
boundary prevents an unbounded writer from starving connection readiness.
