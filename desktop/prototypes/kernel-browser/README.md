# Local Kernel Browser Prototype

> Throwaway decision prototype. This is not a production Loom browser module.

This prototype proves that Loom can keep its Tauri desktop shell while a
task-owned local VM runs Kernel's headful Chromium image. Kernel supplies the
same Chromium identity to a human through WebRTC and to a lead agent through
CDP. The UI also demonstrates explicit human takeover, stale-agent-action
rejection, DOM-backed annotations, and two isolated browser apps.

The result is a **capability pass and production-readiness hold**. See
[`evidence/LOOMCLI-225-verdict.md`](evidence/LOOMCLI-225-verdict.md).

## What is implemented

- A nested Tauri 2 application with two browser-app tabs.
- One independently labelled Kernel container per isolated browser identity.
- Only the selected tab opens a live WebRTC stream; the background browser
  remains available to CDP without paying active video CPU cost.
- Per-browser control epochs. A queued lead action retains its browser identity
  and epoch across tab switches; taking human control invalidates that exact
  queued action. The epoch is checked again immediately before CDP dispatch.
- A Loom-owned pointer and keyboard surface that dispatches validated input
  through CDP while the embedded Neko viewer remains read-only. This bypasses
  the amd64 Xorg input driver, which deadlocks under Rosetta after sustained
  pointer activity. Pointer mapping accounts for Neko's aspect-fit letterbox
  and the remote Chromium window chrome before producing page coordinates.
- CDP-injected element selection that records DOM context and a cropped PNG.
  Capture refreshes the selected element's geometry and records both viewport
  and document coordinates.
- Bounded CDP commands that reject on timeout or disconnect. Target selection
  prefers the visible page while still accepting an initial `chrome://newtab/`
  page so a fresh browser can be navigated.
- A control/fixture server bound to `127.0.0.1:61300`. Colima's
  `host.lima.internal` forwarding can reach that loopback listener, so no LAN
  wildcard bind is required.
- An allowlisted embed-event recorder that proves the native WKWebView reaches
  `KERNEL_CONNECTED` and `KERNEL_PLAYING`.
- Browser requests are restricted to the POC's loopback development origins.
  This is an origin guard, not authentication.
- A generated runtime ID is persisted under `.runtime/`, included in container
  names and ownership labels, and used for exact failed-start cleanup. Host
  ports remain configurable for parallel task-owned runs.
- A damage-based Neko X11 capture pipeline. The image default (`use-damage=false`)
  deadlocks Xorg under Rosetta and produces a connected but black stream.

## Reproduce

Prerequisites used for the recorded run:

- macOS on Apple silicon with Rosetta installed;
- Homebrew Colima 0.10.3 and Lima 2.1.4;
- Docker CLI; and
- the repository's existing desktop Node dependencies.

Create the dedicated runtime (never reuse the default Colima or Podman VM):

```sh
colima start --profile loom-kernel-browser-221 \
  --cpu 4 --memory 10 --disk 40 --runtime docker \
  --vm-type vz --vz-rosetta --activate=false --ssh-config=false
```

Start the loopback-only control server, then the two labelled browsers:

```sh
cd desktop/prototypes/kernel-browser
LOOM_KERNEL_CONTROL_HOST=127.0.0.1 node scripts/control-server.mjs

cd ../../..
desktop/prototypes/kernel-browser/scripts/runtime.sh start
```

The harness uses the pinned image digest
`sha256:7aa6dc616440fbe3f8886cec700dc7533aa2a0ec29b999102aa6cad4ac3e6f50`.
On a new VM Docker pulls it on first use. Navigate the isolated browsers to the
task fixture:

```sh
curl -sS -X POST -H 'content-type: application/json' \
  -d '{"url":"http://host.lima.internal:61300/fixture?app=a"}' \
  http://127.0.0.1:61300/api/navigate/app-a

curl -sS -X POST -H 'content-type: application/json' \
  -d '{"url":"http://host.lima.internal:61300/fixture?app=b"}' \
  http://127.0.0.1:61300/api/navigate/app-b
```

Run the native shell:

```sh
cd desktop/prototypes/kernel-browser
../../node_modules/.bin/tauri dev
```

After exercising human control, run the regression probe. It continuously
checks both CDP and X11 for the failure mode that previously appeared after
roughly 15-21 seconds of pointer activity:

```sh
desktop/prototypes/kernel-browser/scripts/verify-control-health.sh 30
```

Use the development shell for this local HTTP/WebSocket prototype. A normally
packaged Tauri app loads from the secure `tauri://` origin, so production must
serve the browser stream over authenticated HTTPS/WSS rather than relying on
plain loopback mixed content.

Inspect or stop only task-owned containers:

```sh
desktop/prototypes/kernel-browser/scripts/runtime.sh status
desktop/prototypes/kernel-browser/scripts/runtime.sh stop
colima stop --profile loom-kernel-browser-221
```

## Safety boundary

The upstream Kernel launcher is intentionally not used. It has fixed names and
ports, runs privileged, and deletes a fixed container name before launch. This
harness instead pins an immutable digest, uses explicit loopback ports, applies
both prototype and runtime ownership labels, and removes only containers that
match both labels. It mounts neither the repository nor the user's home.

The dedicated VM is still necessary because the Kernel container is
privileged and capped at 8 GiB. This POC does not make that runtime safe or
small enough to ship to Loom users. Clipboard, IME, accessibility input, and
resize behavior remain outside the demonstrated POC boundary.
