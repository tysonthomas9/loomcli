# Session query recovery and read failures

The recovery coordinator now enrolls enabled task sessions, workspace session
count and issue-to-session mapping. Their shared scoped request owner starts a
fresh request for recovery and rejects failure, cancellation or supersession.
Ordinary refreshes join active recovery; reconnect starts a fresh ordinary read
unless recovery already owns the request. Per-scope owners prevent old workspace
or task results from committing after a switch, including A-to-B-to-A changes.
Disabling and unmounting withdraw the participant and cancel its work.

Task polling retains its 3s/10s behavior. Session-count polling retains its
visibility-aware five-minute interval and event debounce. Issue-session mapping
now handles global refresh and connection epochs in addition to terminal events.
Ordinary noncritical read failures retain the prior data; strict recovery rejects.

## End-to-end response honesty

Collection API wrappers propagate missing/unavailable endpoints and failed or
malformed responses instead of turning them into successful empty collections.
They forward AbortSignal and validate container shape plus consumed session
identity/activity and tab identity/liveness/classification fields. A successful
empty collection remains valid. The shared request owner also fences loaders
that ignore cancellation and handles a request started by a reentrant callback.

The task-session service previously ignored failed control-plane or local index
reads and could acknowledge an empty result. It now propagates those failures,
returns a canonical empty array after a successful empty read, and uses a strict
local index query. Strict query rejects corrupt JSON and missing session IDs
before filtering. Configured session-directory failures and genuine workspace
resolution failures also fail the read. An absent workspace with an explicit
runtime directory remains a supported local fallback.

Diagnostic queries and index compaction retain their documented best-effort
repair behavior through the shared parser. Optional transcript/diff/usage
artifact enrichment remains best-effort; it does not define list membership.

## Proof scope and remaining work

Deterministic tests cover recovery with a pre-existing request, API failure,
ignored abort signals, workspace/task switches, disabled participants,
reconnects during recovery, malformed records and valid empty collections.
Go fault tests inject failed control-plane/workspace storage reads and corrupt
local index data. Handler tests assert successful empty sessions encode as [].

This extends registered-surface recovery; it does not reset the SSE cursor or
certify an atomic workspace snapshot. File-browser/checkouts/git/skills surfaces
and bounded recent activity still need honest recovery contracts. A committed
projection fence, retention/reset acknowledgment, fixed replay boundary, real
storage restart and paired browser proofs remain required.

## Validation

- Full frontend unit suite: 412 files, 8,891 tests passed.
- Frontend typecheck and scoped ESLint passed.
- Race-enabled Go tests passed for internal/sessions, webui/svcimpl and
  webui/handlers/misc; scoped Go lint reported zero issues.
- Independent review vetted request ownership, response validation and Go read
  failure propagation; identified issues were repaired and no scoped blocker remained.

These are deterministic test and static-check results. This milestone does not
claim real database-server restart or paired browser recovery proof.
