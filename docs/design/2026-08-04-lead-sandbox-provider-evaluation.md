# Lead Sandbox: Provider Evaluation

**Status:** research, informs `2026-08-04-lead-in-daytona-sandbox.md`
**Date:** 2026-08-04
**Goal:** decide which cloud sandbox provider should host a least-privileged,
long-lived, interactive lead agent — and record the two findings that change the
Daytona design doc.

Related: `docs/design/2026-08-04-lead-in-daytona-sandbox.md` (the design this
evaluates), `docs/loom-glossary.md` (Lead, daytona).

---

## 1. What this doc decides

This evaluation originally found two incorrect assumptions in the lead design:

- **§4.1** picked a Node PTY-bridge sidecar because "the Daytona SDK is
  TypeScript-only". That premise is false (§3 below).
- **§3** framed the trust model as a binary fork: either the sandbox holds
  credentials, or the feature is not buildable. There is a third branch (§4).

The companion design now incorporates both findings: it selects the Go SDK,
uses scoped credentials for the Daytona phases, and records external HTTPS
credential brokering as the provider-changing escape hatch. Everything else in
the design survives this evaluation unchanged.

## 2. Requirements this evaluation scores against

Derived from the lead design, in rough order of how much they constrain choice:

| # | Requirement | Source |
|---|---|---|
| R1 | Interactive PTY with resize, and **reattach to an existing PTY by session id** | §4.2 `daytonaAttachment`, §4.5 reattach across `serve` restart |
| R2 | Long-lived session; a lead is not a one-shot task run | §4.5 |
| R3 | Go client — the terminal layer is Go | §4.1 |
| R4 | Custom image carrying `loom` + backend CLI + Node | §4.4 |
| R5 | Egress allowlist (fleet-db, model API, git host) | §3.3 |
| R6 | Credential posture that does not simply invert today's leak-probe stance | §3 |
| R7 | Idle cost control — a long-lived lead must not bill like a busy one | §4.5 |
| R8 | Labels + list API for the orphan reaper | §4.5 |

R1+R3 together are the sharp filter. Most providers in this market are built for
one-shot `exec`, not for an attached terminal that outlives the process that
opened it.

## 3. Finding A — the Daytona Go SDK exists, and covers the whole surface

`@daytona/sdk` (TypeScript, 0.179.0) is no longer the only client. There is an
official Go module:

```
github.com/daytonaio/daytona/libs/sdk-go   // latest v0.190.0, requires Go 1.25+
```

Verified by inspecting the module source in the module cache, not from vendor
docs. Every method `daytonaAttachment` needs is present.

**`PtyHandle`** (`pkg/daytona/pty_handle.go`):

| Method | Serves |
|---|---|
| `DataChan() <-chan []byte` | `Output()` |
| `SendInput([]byte)` / `Write` | `WriteInput()` |
| `Resize(ctx, cols, rows)` | `Resize()` — the outer hop of §4.6's two-hop resize |
| `ExitCode() *int`, `Error() *string` | `ExitReason()` |
| `Wait(ctx) (*types.PtyResult, error)` | close-code mapping |
| `Disconnect()`, `Kill(ctx)`, `IsConnected()`, `WaitForConnection(ctx)` | lifecycle |

It also implements `io.ReadWriter` directly.

**`ProcessService`** (`pkg/daytona/process.go`):

- `CreatePty(ctx, id, opts...) (*PtyHandle, error)`
- **`ConnectPty(ctx, sessionID) (*PtyHandle, error)`** — this is reattach. §4.5's
  "restarted serve reattaches instead of orphaning" is a supported operation,
  not something to build.
- **`ListPtySessions(ctx)`**, `GetPtySessionInfo`, `KillPtySession`,
  `ResizePtySession` — the reaper's reconciliation primitives.

**`SandboxBaseParams`** (`pkg/types/types.go:79`) carries everything §4.4/§4.5
needs at create time:

```go
Labels              map[string]string   // §4.5 reaper: labels{loom:"lead"}
AutoStopInterval    *int
AutoArchiveInterval *int                // see §7 — archived sandboxes are unbilled
AutoDeleteInterval  *int
NetworkBlockAll     bool
NetworkAllowList    *string             // comma-separated CIDRs
DomainAllowList     *string             // comma-separated domains, wildcards ok
EnvVars             map[string]string
LinkedSandbox       string              // see §8, open question 3
```

`SnapshotParams{Snapshot string}` covers the §4.4 prebuilt-snapshot strategy, and
`Sandbox.UpdateNetworkSettings(ctx, …)` changes policy on a running sandbox.

> Note: pkg.go.dev's rendered summary for this package omits the network fields.
> They are present in the source at `pkg/daytona/client.go:460,491-495`. Do not
> conclude from the rendered docs that Go lacks them.

**Consequence for the design doc:** §4.1's former Option A (Node sidecar) was
not justified by the SDK-language constraint, and the former Option B did not
require hand-rolling runner-URL resolution and preview-token auth—the Go SDK
hides exactly that. The companion design now selects the Go SDK and omits
`daytona_bridge.go` and sidecar-framing-codec tests. The Go SDK (v0.190.0) is
*ahead* of the pinned TS SDK (0.179.0), so this is not a maturity downgrade.

## 4. Finding B — §3's fork has a third branch

§3 concludes: put scoped credentials in the sandbox, or don't build it. A third
option has become standard in this market since that framing was written —
**terminate egress at a proxy outside the sandbox and inject credentials there.**

Cloudflare's Outbound Workers are the most complete implementation. Every
HTTP/HTTPS request leaving the sandbox is handled by a Worker running outside it,
which injects auth headers from bindings the sandbox cannot read. Supporting
pieces: `allowedHosts` is deny-by-default with glob matching, each instance gets
a unique ephemeral CA (private key stays in the sidecar, never shared across
instances) so TLS is interceptable, and `setOutboundHandler()` changes policy on
a *running* sandbox — open egress for `git clone` and dependency install, then
lock down to fleet-db only. E2B ships a narrower version: selective TLS MITM with
per-domain transform rules for credential injection.

Why this matters here specifically: **all three of the lead's credential needs —
fleet-db, model API, git — are HTTPS.** That is exactly the protocol class these
proxies cover. Under this shape the leak probe's assertion stays "zero
credentials in the sandbox" rather than being replaced by a weaker one, which is
what §3.1 was trying to avoid.

Limitations, stated plainly:

- **HTTP/HTTPS only.** Raw TCP bypasses the proxy. Fine for the lead's three
  needs today; a constraint to remember before adding a fourth.
- **Daytona does not offer this.** Its controls are allow/deny at the network
  layer, not a credential-brokering proxy. Choosing this branch means changing
  provider, and on Cloudflare it costs the Go SDK from §3.
- It moves the secret from the sandbox to the Worker, not out of existence. The
  gain is blast radius: a compromised lead cannot read the credential, only spend
  it against allowlisted hosts while running.

**Recommendation:** do not adopt this now, but stop treating §3 as a binary. Note
it as the escape hatch if the scoped-credential minting in §3.2 proves too costly
to build, and record that it constrains provider choice if chosen later.

## 5. The landscape

| | Isolation | R1 PTY + reattach | R3 Go | R5 egress | R6 creds | R7 idle | R4 image |
|---|---|---|---|---|---|---|---|
| **Daytona** | Container (shared kernel) | ✅ `ConnectPty` | ✅ | 10 CIDR + 20 domains, live-updatable, block-all | in-sandbox | archived unbilled | snapshots |
| **Modal** | gVisor | ✅ `exec(pty:true)` | ✅ beta | block/CIDR/domain, `updateNetworkPolicy()` live | in-sandbox | ≤24h cap | ✅ |
| **Cloudflare Sandbox** | Container on Workers | ✅ WS + auto-reconnect | ❌ TS | **Outbound Workers** | **brokered** | `sleepAfter` 10m default | Dockerfile |
| **E2B** | Firecracker | ✅ | ❌ JS/Py | allow/deny live, TLS MITM | brokered (partial) | **pause keeps memory** | templates; self-host |
| **Vercel Sandbox** | Firecracker | ❌ undocumented | ❌ JS/Py | `deny-all`, SNI + CIDR | in-sandbox | FS-only snapshot | OCI (beta) |
| **Fly Sprites** | Firecracker | raw VM | Machines API | DIY | in-sandbox | idle billing, ~300ms restore | ✅ |
| **AWS AgentCore** | microVM/session | ❌ HTTP invoke | AWS SDK | IAM | IAM-native | ≤8h, 15m idle | ✅ |

**Ruled out:**

- **Vercel Sandbox** — no documented PTY (fails R1), and its persistence
  snapshots the filesystem only, so a long-lived `loom lead` process does not
  survive stop/resume.
- **Cloudflare Dynamic Workers** — the "100× faster than containers" headline in
  every comparison post refers to V8 isolates. They cannot run the `loom` binary
  or the backend CLI. Only the container-backed Sandbox SDK is in scope.
- **AWS AgentCore Runtime** — models an agent as an HTTP-invoked endpoint, not a
  PTY you attach a terminal tab to. Fails R1.
- **Fly Machines (raw)** — R1, R5, R8 are all DIY. Sprites is the productized
  form and is credible, but offers no advantage over Daytona given R3.

## 6. Recommendation

**Stay on Daytona for phases 1–3.** It is the only provider that is
simultaneously already integrated, Go-native, PTY-with-reattach, and
domain-allowlist capable. Finding A removes the ugliest component in the design.

Two adjustments to make while there:

1. **Use `AutoArchiveInterval`, not just `AutoStopInterval`.** §4.5 treats
   auto-stop as the cost backstop. Stopped sandboxes still bill for reserved
   disk; archived ones are not billed at all. For a lead that may idle overnight,
   archive is the correct backstop and stop is the intermediate state.
2. **Keep the provider seam at `PTYSource`, not below it.** §4.3's
   `RoutingPTYSource` is already the right shape. If R6 later forces a move to
   credential brokering (§4), the blast radius should be one `PTYSource`
   implementation.

**Fallbacks, in order:**

- **Modal**, if the Daytona Go PTY path disappoints in practice. Same Go + PTY
  properties, plus gVisor over shared-kernel containers and sandboxes that are
  explicitly not authorized to reach other workspace resources — a real blast
  radius reduction for a credential-holding sandbox. Cost: a second integration
  and a 24h lifetime cap.
- **E2B**, if idle-lead economics dominate. Its pause preserves **memory as well
  as filesystem**, so a paused lead resumes with `loom lead`, `leadcontrol`, and
  the backend CLI's PTY all still running, and paused sandboxes persist
  indefinitely with the continuous-runtime clock reset on resume. Nobody else
  offers this, and it maps directly onto the `PTYManager` grace-timer mismatch in
  §4.5. Cost: no Go SDK, so the §4.1 sidecar comes back.

## 7. What this does not change

**§5 (fleet-db reachability) stands unmodified.** Every provider evaluated needs
a fleet-db reachable from its cloud. Option 1 — a deployed fleet-db — remains the
only one that delivers the feature, and remains a prerequisite rather than an
implementation detail.

Open question 3 in the design doc ("one sandbox or many?") gains one input:
Daytona's `LinkedSandbox` schedules a new sandbox on the same runner as an
existing one so a local network can be established between them. That makes
"lead plus the workers it dispatches" cheaper to coordinate than the doc assumes
— though linked sandboxes must be ephemeral (`AutoDeleteInterval=0`), which
conflicts with a long-lived lead. Worth a closer look before settling §4.5.

## 8. Verification status

Verified by reading module source at
`github.com/daytonaio/daytona/libs/sdk-go@v0.190.0`: all of §3 — PTY surface,
`ConnectPty`, `ListPtySessions`, create-time network and lifecycle params.

Taken from vendor documentation, not independently verified: Cloudflare Outbound
Worker semantics, E2B pause/memory-state behaviour and TLS MITM, Modal Go SDK PTY
and network-policy parity with Python, all pricing.

Not verified and worth a spike before Phase 1 commits: Daytona Go PTY behaviour
against a **live** sandbox — specifically whether `ConnectPty` replays buffered
output on reattach or starts from the connection point. §4.2 plans to reuse the
existing `ringBuffer` for scrollback, which is the right call either way, but the
answer determines whether the ring buffer is the only source of replay or a
second one.
