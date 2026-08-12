# WebUI is a delivery-only module

Status: accepted

`internal/webui` is limited to HTTP assembly, generated transport DTO mapping,
middleware, SSE and WebSocket protocols, short-lived transport tokens,
presentation state, and frontend assets. Product policy, coordination,
persistence, machine-local runtime control, and cross-capability Read
Projections belong behind their owning capability or application seams; the
`internal/app/serve` composition root injects those interfaces into WebUI. This
decision favors locality and enforceable ownership over the convenience of
screen-oriented coordinator modules or a composite Store, and it prevents a
second task-claim authority beside FleetDB.

## Consequences

- The intended direct children are `app`, `frontend`, `handlers`, and `server`.
- WebUI must not import `internal/store`, `internal/ops`, `internal/bootstrap`,
  persistence adapters, FleetDB transports, PTY implementations, or local
  filesystem state.
- Run Capture is a Read Projection over Execution or Interaction ownership and
  Artifacts evidence; it is not a new mutable aggregate or store.
- Legacy local session archives receive no compatibility reader, migration, or
  dual-write path. Loom stops reading and writing them but does not delete them
  automatically.
- Migration replaces each shallow module and its tests at the owning seam; it
  does not preserve forwarding packages, aliases, or a renamed service locator.
