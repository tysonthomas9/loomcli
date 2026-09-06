# SSE-bound issue recovery handles

Expired authoritative replay can now attach an optional recovery handle to its no-ID `resync` frame. The handle identifies the exact source captured by that SSE connection and permits a certified issue-manifest HTTP read after the stream closes. It does not authorize checkpoint acknowledgment.

## Request and lifetime

The SSE handler preserves the principal returned by its single-use token validator. On an expired cursor, it registers that principal, workspace, subscription repository filters and captured source, then sends:

```json
{"reason":"expired","recovery":{"handle":"<opaque random handle>","workspace":"WS","source_repos":[],"expires_at":"<UTC timestamp>","manifest":"fleet.issue-workspace.v1"}}
```

There is no `id` field in this SSE frame and no successful `connected` frame before initial replay completes. Open authentication mode has no verified principal and cannot issue these handles. Sources without the optional recovery interface, registration capacity exhaustion and other replay errors retain ordinary signal-only resync behavior. A captured wrapper can expose that interface even when its underlying backend lacks support; it may issue a handle, but the subsequent read fails with 503 without fallback.

The registry retains the source independently of the failed SSE request for an absolute 60-second lifetime. Retrying does not extend expiry. It allows at most 256 retained handles, 8 per principal, and one active read per handle. Expired entries are removed lazily during access/registration; storage remains bounded, and hub shutdown closes the registry, releases retained references and cancels active reads. Source retirement still invalidates reads before or after I/O.

`POST /api/workspaces/{ws}/events/recovery/issues` requires normal authenticated user identity and exactly one `X-Loom-Recovery-Handle` header. It rejects query parameters, body framing and repository overrides. The principal and workspace must match the captured registration. It calls that source directly, under caller cancellation, a 15-second request deadline and handle expiry; it never opens a new workspace source. Success preserves Fleet's validated native JSON and echoes the handle header. All responses use `Cache-Control: no-store`.

Missing/expired/foreign handles return 410, concurrent use returns 409, invalid input returns 400, absent authentication returns 401, source failures return 503 and canceled/timed-out reads return 504. Failed reads do not contain a replacement cursor or certified document.

Repository filters are subscription metadata, not authorization. The certificate covers the complete workspace. A client changing filters does not revoke an old handle automatically; any frontend consumer must match the response to its current connection, scope and recovery attempt before accepting it. The endpoint makes no claim of repository-restricted data access.

## Evidence

- Registry tests: owner/workspace isolation, fixed expiry and capacity, retry behavior, one active read, no mutex across source I/O, caller/expiry/shutdown cancellation and rejection of late success.
- HTTP handler tests: input/auth boundaries, raw response preservation, failed reads and registry error mapping.
- Module integration test: the actual SSE handler consumes a signed token, encounters expired replay, emits a no-ID handle before `connected`, and returns. A separate authenticated-context HTTP request can then read the captured source. A different principal/workspace is denied; replacement with the very same backend object under a new subscriber entry fails without another backend read.
- Full affected realtime/subscription race suites, Go build/vet, and scoped lint.

The integration uses backend fixtures and injected REST identity. It is not an actual JWT/JWKS, FleetDB storage, or browser proof. The integration also runs the real JWT middleware without a Bearer token: SSE reaches its own token validator, while the recovery POST returns 401 despite a valid handle. No public-route exception was added.

## Remaining work

The browser does not consume the handle yet. Frontend scope/attempt/cache-generation checks, active and dormant view coverage, certificates for non-issue views, durable Fleet incarnation across HTTP requests, acknowledgment and replay after accepted recovery remain required. Successful issue recovery alone must not advance the SSE checkpoint. Capacity limits and unsupported modes fail closed; they do not trigger an ordinary-query fallback or transport migration.
