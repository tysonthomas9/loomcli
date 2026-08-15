# ADR 0001: Store Semantics Land as Conformance Suites

## Status

Accepted — 2026-08-15

## Context

Loom exposes one Store seam with memstore and fleet-db adapters. Callers rely on
behavioral contracts that Go interfaces cannot express: optimistic role-update
CAS, immutable creates and retries, referential delete ordering, and attribution
snapshots.

Arc A exposed the cost of leaving those contracts backend-local. Memstore silently
overwrote an artifact when the same ID was created twice, while fleet-db rejected
the duplicate. Artifact-backed logs therefore behaved differently depending on the
selected store.

## Decision

Every behavioral contract owned by the Store seam must be encoded as a shared
`internal/store/storetest` conformance suite. A suite exposes a `RunXxx` entrypoint,
accepts a backend factory, and observes behavior only through store interfaces.

Every suite runs against both memstore and the fleet-db client backed by an embedded
fleet-db process. The `make conformance` lane requires the fleet-db binary and fails
if it is unavailable; ordinary development tests may keep the explicit embedded-test
skip. Transport details such as HTTP status mapping remain in client wire tests.

## Consequences

- Store semantic changes require an atomic suite and adapter update.
- Backend drift fails at the owning Store seam instead of surfacing in callers.
- Fleet-db is required in the conformance lane, increasing that lane's setup and run
  time.
- Backend-specific implementation and transport tests remain useful, but they do not
  substitute for shared conformance.
