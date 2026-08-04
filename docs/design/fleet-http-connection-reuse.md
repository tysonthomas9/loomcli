# loom-fleet → fleet-db HTTP connection reuse

## Symptom

fleet-db's redis pool is exhausting under load. Every loom-fleet → fleet-db
request takes 5–7 s and ultimately fails with `context canceled` or
`redis: connection pool timeout`. The cumulative effect makes the whole
fleet UI hang.

## Verified facts

### MaxIdleConnsPerHost = 2 (Go default) is the critical bottleneck

`fleet.New` (`internal/backend/fleet/fleet.go:61-62`):
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

### Two construction paths, both leaking idle conns

- **Path A** — `cli/issue_backend_resolve.go:DefaultIssueBackend()` →
  `createFleetIssueBackend()` → `fleet.New(...)` once at startup. Singleton.
- **Path B** — `webui/hooks/fleet_backend.go:OnRegister()` → `fleet.New(...)`
  per workspace. N instances. Each `BackendMutationSubscriber` goroutine
  long-polls on this client.

Both paths fall through to `DefaultTransport`. Connection pool is shared
in principle but capped at 2 idle/host.

### 30s client Timeout races 30s server long-poll timeout

`fleet.go:62`: `Timeout: 30 * time.Second`
`backend_subscriber.go:14`: `backendWaitTimeout = 30 * time.Second`
`backend_subscriber.go:131`: `b.WaitForMutations(ctx, since, 30000)`

Server takes ~30 s to write the empty `{events:[]}` response when no
mutations arrive. Client timeout fires at the same instant. Network
latency tips the race toward client-cancel — manifests as `context canceled`.

### Subscriber spins on early-empty 200

`backend_subscriber.go:144-148`:
```go
if len(muts) == 0 {
    continue   // no client-side delay
}
```
Comment claims "the backend's own timeout is the rate limit." Only true
when fleet-db honors its full 30s. Under pool pressure fleet-db returns
early 200-with-empty-body, and the loop spins.

## Recommendations (ranked)

### Fix 1 — Share a tuned `*http.Transport` (highest impact)

Introduce package-level `var sharedFleetTransport = &http.Transport{...}`
in `internal/backend/fleet/`:
- `MaxIdleConnsPerHost: 128`
- `MaxIdleConns: 256`
- `IdleConnTimeout: 90 * time.Second`
- `TLSHandshakeTimeout: 10 * time.Second`

`fleet.New` uses it when `cfg.HTTPClient` is nil. Both Path A and Path B
benefit. ~30 LOC + a `SharedHTTPClient()` getter.

**Observable**: `lsof -p <loom-fleet-pid> | grep -c TCP` drops from
O(N×M) to O(N+M); `loom-fleet` log of new TCP dials drops to ~1 per
host:port lifetime instead of per request.

### Fix 2 — Decouple client Timeout from server long-poll

`fleet.go:62`: bump to `65 * time.Second` (30 s server poll + 30 s slack
+ 5 s response slack). OR set `Timeout: 0` and use per-call
`context.WithTimeout(ctx, backendWaitTimeout + 10*time.Second)` in
`WaitForMutations`. ~5 LOC.

**Observable**: `backend WaitForMutations error: context canceled` log
spam disappears.

### Fix 3 — Add 250 ms back-off on empty long-poll response

`backend_subscriber.go:144-148`:
```go
if len(muts) == 0 {
    s.waitWithCancel(250 * time.Millisecond)
    continue
}
```
~3 LOC. Caps re-entry at 4/s when fleet-db returns early empties.

**Observable**: subscriber goroutine CPU drops to near-zero in steady
state.

## Skipped

- Inject `*http.Client` per FleetBackendHook construction — Fix 1's
  shared transport is the same effect with simpler wiring
- Document `Config.HTTPClient` contract — defer until after Fix 1 lands

## Implementation order

1. Fix 1 (sharedFleetTransport) — primary
2. Fix 2 (timeout decoupling) — small, ships with Fix 1
3. Fix 3 (back-off) — small, ships with Fix 1
