# Transient SSE cursor isolation

A local refresh or terminal notification has no durable FleetDB source cursor.
Previously the mutation endpoint synthesized a timestamp/counter ID for it,
replacing the client resume checkpoint with a value the durable source did not
issue. A transient overflow could also clear or replace an established checkpoint.

Mutation frames now carry an ID only when their payload has a durable cursor.
Transient mutation and overflow resync frames omit the ID field completely.
An explicit empty `id:` remains a deliberate reset. A client with no offered
live event cannot reuse its initial request cursor as an overflow checkpoint,
because replay may already have advanced the connection.

The frontend resync callback reports the effective parser checkpoint. The
fetch-event-source message ID alone cannot distinguish omitted and empty IDs.
The provider test mock now models the library's mutable resume headers.
The separate log endpoint retains its process-local event IDs. Replay preparation
and its final checkpoint append are extracted without changing behavior to meet
the Go complexity gates; the parent PR failed that lint check.

## Regression evidence

Server tests cover absent, malformed and valid timestamps, an internal delivery
cursor that must not be trusted as a source cursor, transient overflow after a
replay checkpoint, and overflow before any live event is offered. The live HTTP
fixture asserts the exact durable source cursor. Client tests use the real
fetch-event-source parser to verify durable mutation, no-ID transient mutation,
no-ID overflow resync, network failure and retry with the original Last-Event-ID.
A separate explicit-empty-ID case verifies that retry omits that header.
The frontend regression failed before the callback fix.

## Limits

This is deterministic transport and HTTP proof, not a paired browser or storage
restart proof. A queued older durable event can still regress a newer replay
checkpoint through overflow; a global ordered delivery frontier remains required.
When the last discarded event is transient, keeping the existing checkpoint may
replay extra durable events. Resync still requests authoritative query refresh;
this change does not establish query freshness after a failed refetch.

## Local validation

The 138 client/provider/connection-status tests passed, as did TypeScript checking
and scoped frontend lint. Realtime and app race packages passed (5.191s and
14.403s); the separate log package passed (1.762s). Generated TypeScript and Go
API freshness checks passed. The replay helper extraction is covered by the
realtime race package and scoped Go lint on the final revision.
Independent source review found no blocker within this patch's scope.
