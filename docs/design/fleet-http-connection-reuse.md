# loom-fleet → fleet-db HTTP connection reuse

> **Status:** Implemented — all three fixes shipped. *audited 2026-07-23*
>
> This file is the rationale a reader lands on from the code comments at
> `internal/backend/fleet/transport.go:19` and `internal/backend/fleet/fleet.go:72`.
> The "Symptom" and "Verified facts" sections are the historical incident
> record and describe the code as it was *before* the fix. "Fixes (all
> shipped)" maps each one onto its implementation site.

## Symptom

fleet-db's redis pool is exhausting under load. Every loom-fleet → fleet-db
request takes 5–7 s and ultimately fails with `context canceled` or
`redis: connection pool timeout`. The cumulative effect makes the whole
fleet UI hang.

## Verified facts (historical — pre-fix state)

### MaxIdleConnsPerHost = 2 (Go default) is the critical bottleneck

`fleet.New` used to do:
```go
if httpClient == nil {
    httpClient = &http.Client{Timeout: 30 * time.Second}
}
```
No `Transport` set → falls through to `http.DefaultTransport` (process-wide
singleton, so the connection pool IS shared across all FleetBackend
instances — one saving grace). But `DefaultTransport.MaxIdleConnsPerHost`
defaults to **2**. With N long-poll goroutines + M concurrent UI polls
hitting one fleet-db host:port, only 2 connections are ever cached;
every other connection closes without returning to the idle pool, forcing
fleet-db to accept a fresh TCP connection (and fresh Redis pool checkout)
on each request.

Fixed at `internal/backend/fleet/fleet.go:66-73`, which now assigns
`SharedHTTPClient()` when `cfg.HTTPClient` is nil.

### Many construction paths, all falling through to DefaultTransport

The original write-up claimed two construction paths. There are in fact
eight non-test `fleet.New` call sites, none of which pass an
`HTTPClient`, so all eight now default through `SharedHTTPClient()`:

- `internal/cli/deps.go:274`
- `internal/cli/fleet_mode.go:83` (`createFleetIssueBackend`; reached from
  `DefaultIssueBackend`, `internal/cli/issue_backend_resolve.go:87`, only
  indirectly via `resolveDirectIssueBackend`)
- `internal/cli/issue_backend_workspace.go:57`
- `internal/cli/driver/run_context.go:109`
- `internal/cli/epic/epic_stack.go:309`
- `internal/webui/hooks/fleet_backend.go:58` (one per workspace; each
  `BackendMutationSubscriber` goroutine long-polls on this client)
- `internal/webui/handlers/driverapi/module.go:174`
- `internal/webui/handlers/taskrunapi/module.go:128`

One additional site injects the shared client explicitly rather than
relying on the nil default: `internal/bootstrap/openstore.go:97` passes
`fleet.SharedHTTPClient()` into the Store client.

### Client Timeout raced the server long-poll timeout

The 30 s client `Timeout` and the 30 s `backendWaitTimeout` fired at the
same instant; network latency tipped the race toward client-cancel, which
manifested as `context canceled`.

Both numbers have since moved (client 65 s, server wait 10 s), so the race
as described no longer exists. See Fix 2.

### Subscriber spins on early-empty 200

The empty-response branch used to `continue` with no client-side delay.
Its comment claimed "the backend's own timeout is the rate limit", which
only held while fleet-db honored its full wait window. Under pool pressure
fleet-db returns early 200-with-empty-body, and the loop spun.

## Fixes (all shipped)

### Fix 1 — Share a tuned `*http.Transport` (highest impact)

Shipped as `sharedTransport` in `internal/backend/fleet/transport.go:21-27`
with the proposed values:
- `MaxIdleConnsPerHost: 128`
- `MaxIdleConns: 256`
- `IdleConnTimeout: 90 * time.Second`
- `TLSHandshakeTimeout: 10 * time.Second`

`SharedHTTPClient()` (`transport.go:46-54`) is the singleton getter; it
wraps `sharedTransport` in `otelhttp.NewTransport`. `fleet.New` consumes
it at `internal/backend/fleet/fleet.go:66-73`.

**Observable**: `lsof -p <loom-fleet-pid> | grep -c TCP` drops from
O(N×M) to O(N+M); `loom-fleet` log of new TCP dials drops to ~1 per
host:port lifetime instead of per request.

### Fix 2 — Decouple client Timeout from server long-poll

Shipped as both halves of the original either/or:
- `internal/backend/fleet/transport.go:50` sets `Timeout: 65 * time.Second`
  on the shared client.
- The subscriber wraps every long-poll in its own deadline:
  `internal/webui/subscription/backend_subscriber.go:218-220`,
  `context.WithTimeout(s.ctx, backendWaitTimeout+10*time.Second)`. That
  per-call context, not the client `Timeout`, is the dominant exit path.

`backendWaitTimeout` is now **10 s**, not 30 s
(`backend_subscriber.go:23`). The reason is recorded at
`backend_subscriber.go:14-22`: fleet-db caps the server-side timeout at
10 s (`mutationsMaxTimeout` in fleet-db's `internal/api/mutations.go`) to
bound how long `XREAD BLOCK 0` holds a Redis pool connection; anything
larger is rejected as a validation error. The value passed on the wire is
computed at `backend_subscriber.go:153`
(`int64(backendWaitTimeout / time.Millisecond)`), not hardcoded.

> **Open contradiction — needs a human.** `transport.go:40-45` justifies
> the 65 s client timeout with "fleet-db's `WaitForMutations` long-poll
> runs for up to 30s server-side", and the empty-poll comment at
> `backend_subscriber.go:174-176` also says "well before its 30s timeout".
> `backend_subscriber.go:14-22` says the server cap is 10 s. At most one
> of these is right. The 65 s timeout is harmless either way (it is not
> the binding deadline), but the comments should be reconciled.

**Observable**: `backend WaitForMutations error: context canceled` log
spam disappears.

### Fix 3 — Back-off on empty long-poll response

Shipped as `backendEmptyPollDelay` (`backend_subscriber.go:34`), applied
at `backend_subscriber.go:180`. The shipped value is **1 second**, not the
proposed 250 ms — so re-entry is capped at 1/s, not 4/s, when fleet-db
returns early empties.

**Observable**: subscriber goroutine CPU drops to near-zero in steady
state.

## Skipped

- Inject `*http.Client` per FleetBackendHook construction — Fix 1's
  shared transport is the same effect with simpler wiring.
- ~~Document `Config.HTTPClient` contract — defer until after Fix 1 lands~~
  — done anyway: `internal/backend/fleet/config.go:26-31` documents that a
  nil `HTTPClient` means the `SharedHTTPClient()` singleton.

## Related

- `docs/design/2026-07-23-control-plane-as-built.md` — where the shipped
  control plane lives, including the fleet-db-backed store the tuned
  client fronts.
