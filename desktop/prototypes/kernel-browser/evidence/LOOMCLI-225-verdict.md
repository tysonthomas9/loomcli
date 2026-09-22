# LOOMCLI-225 Architecture Verdict

Date: 2026-09-21 (America/Los_Angeles)

## Verdict

**Keep Tauri. Continue the browser feature only as a production runtime design;
do not ship this prototype or migrate Loom to Electron.**

Kernel's architecture answers the core product question: a remote/headful
Chromium session can be shown inside the lead-agent view while the lead drives
the exact same page through CDP, and Loom can layer explicit human takeover and
DOM-backed annotations over it. Multiple browser apps should mean multiple
isolated browser profiles/containers, not multiple desktop processes. The lead
view can show one active stream at a time and keep background profiles available
to automation.

## Comparison

| Option | Shared human/agent page | Isolation | Loom impact | Verdict |
|---|---|---|---|---|
| Kernel-style local browser | Yes: WebRTC + CDP | One container/profile per app | Add runtime/session broker; retain Tauri | Proven, needs production rewrite |
| Tauri child webview alone | Human view only; no equivalent isolated CDP browser | App webview cookies/process model | Smallest UI change | Insufficient for shared agent automation |
| Electron | Chromium/CDP integration is direct | BrowserContext/partition available | Replace desktop shell and packaging | Unnecessary migration |

## Production boundary

The prototype must not become the production module unchanged:

- Kernel's tested image is amd64-only and needed Rosetta on Apple silicon.
- The container is privileged, has an 8 GiB limit, and uses roughly
  1.25-1.87 GiB resident memory per idle/active browser in this run.
- An active stream cost roughly 45-49% CPU per container in point samples.
- Colima, Lima, Docker compatibility, Rosetta, and a separately provisioned VM
  are not an acceptable invisible end-user dependency chain.
- The tested Rosetta path requires a non-default Neko X11 capture pipeline;
  playback events alone did not detect black-frame failure.
- TCP media and all CDP/live/control endpoints need a lease-based allocator,
  authentication, origin checks, and crash reconciliation.
- A packaged Tauri page uses a secure custom origin, so production browser
  media and control must be available over authenticated HTTPS/WSS.
- Control epochs currently live in one Node process. Production ownership must
  be durable and scoped to a browser page/session.
- The locked macOS session prevented native pointer, keyboard, clipboard,
  resize, and IME quality checks even though WKWebView connection/playback was
  proven.

## Suggested production shape

Add a browser-runtime provider behind Loom's lead-session capability model.
FleetDB continues to own scheduling, roles, issues, TaskRuns, leases, and
placement. Loom owns browser-session lifecycle, capability discovery, control
leases/epochs, and annotation events. Keep browser identity distinct from the
conversation, execution attempt, provider-native agent session, and visible
viewer connection. A browser app is a durable profile/session; viewers and CDP
clients reconnect to it.

The next slice should therefore specify the broker contract and native-arm64
runtime/packaging choices before touching the real lead-agent view.
