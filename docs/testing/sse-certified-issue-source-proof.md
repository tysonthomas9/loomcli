# Certified issue recovery client and captured source

This slice consumes FleetDB PR #274's fixed `fleet.issue-workspace.v1` manifest through Loom's existing captured mutation source. It does not authorize a browser checkpoint reset.

## Contract

`backend.IssueRecoveryBackend` is an optional read capability. Fleet sends one bodyless authenticated POST to the configured workspace's `/api/v2/{workspace}/issues/recovery-snapshot`. It requires HTTP 200, JSON content type, a complete native manifest, the configured workspace, and a canonical nonzero fixed cursor. Redirects, oversized responses (over 16 MiB), malformed or partial documents, and unsupported servers fail without ordinary-query fallback.

The client validates issue identity, required fields, typed collections, native repository aliases, derived-view membership and record consistency, and direct blocker details. It accepts the producer's exact parent-blocked sentinel and extensible issue status/type strings. The original JSON document is retained, including metadata and unknown future issue fields; the existing compatibility issue converter is not used.

The exact object returned by `OpenMutationSource` exposes this optional capability. Recovery uses the captured subscriber entry and backend. The entry must remain registered before and after the read; the returned workspace must match the captured workspace. Replacement entries are never selected, even when they report equal cursor strings. The subscriber combines caller and subscriber lifetime cancellation and discards late successful responses after cancellation. These checks share the existing head/page guards.

## Validation

- Fleet backend HTTP fixture tests cover complete/empty manifests, malformed certificates and issue fields, parent-blocked sentinels, size/status/content type, redirect rejection, and cancellation.
- Subscription tests cover valid captured reads, equal-entry-backend replacement with a new registration, in-flight retirement, manager closure, workspace mismatch, unsupported backends, caller cancellation and subscriber stop with a backend that returns late success.
- Full affected package race suites: `go test -race ./internal/webui/subscription ./internal/backend/fleet ./internal/webui/server/realtime`.
- Build of backend and Web UI Go packages, affected-package vet, and backend/subscription lint.

These are deterministic package and simulated HTTP proofs. FleetDB PR #274 separately documents its actual PostgreSQL-to-HTTP proof. This slice has no paired service or browser proof.

## Remaining protocol work

No Loom REST route or frontend consumer invokes this capability yet. A future recovery request must refer to the already-captured source through an authenticated, scoped registration handle, with explicit lifetime behavior when initial replay expires. It must not open another source by workspace or infer identity from equal cursors.

A process-local subscriber identity is not a durable backend incarnation. Cross-request storage incarnation, recovery attempt and cache generations, active and dormant view coverage, non-issue view certificates, acknowledgment and replay-after-acceptance remain unresolved. `Through` is a committed lower bound supplied by the producer, not permission to reset a client. Existing signal-only resync behavior remains unchanged.
