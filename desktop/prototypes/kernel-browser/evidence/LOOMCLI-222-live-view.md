# LOOMCLI-222 Host Reachability and Native Live View

Date: 2026-09-21 (America/Los_Angeles)

Status: **passed with a locked-screen visual-capture limitation**.

## Host reachability

The Node control/fixture server listened only on `127.0.0.1:61300`. From the
dedicated VM, `http://host.lima.internal:61300/healthz` returned the expected
JSON. Both Chromium identities then navigated by CDP to distinct query forms of
`http://host.lima.internal:61300/fixture` and reported title
`Loom host reachability fixture`.

This is the important security result: Colima forwarded the VM's special host
name to the explicit macOS loopback listener. The earlier wildcard bind was not
needed and is not part of the final procedure.

## Native Tauri proof

The nested Tauri 2 app launched `target/debug/loom-kernel-browser-poc`. Its
actual WKWebView posted these allowlisted events back to the loopback control
server:

```text
app-a KERNEL_CONNECTED AppleWebKit/605.1.15
app-a KERNEL_PLAYING   AppleWebKit/605.1.15
```

At the same time Kernel logged WebRTC ICE `checking` to `connected`, peer
connection `connected`, and `set webrtc connected=true` over TCP mux port
56000. A separate Chromium-based UI check emitted a distinguishable
`HeadlessChrome/151` user agent, so the AppleWebKit evidence is attributable to
the native shell rather than the test browser.

The macOS session was locked, so Computer Use could not capture the native
window or manually verify keyboard, clipboard, resize, and IME behavior.
Connection and playback in the real WKWebView are verified; those finer input
qualities remain unverified and belong in a production integration test.

## Hands-on correction

A later visible-window check exposed a false-positive boundary in the initial
evidence: `KERNEL_PLAYING` only proved an active media track. Kernel's default
`ximagesrc use-damage=false` pipeline deadlocked the amd64 Xorg server under
Rosetta, so the track contained black frames and new X clients timed out.

The runtime now supplies Neko's documented custom capture pipeline with
`use-damage=true`. With an active WKWebView stream, `xwd` completed rather than
timing out, and the resulting 1920x1080 display capture showed the fixture and
Chromium chrome. The WKWebView again emitted `KERNEL_CONNECTED` and
`KERNEL_PLAYING`; the app's takeover/stale-action flow passed afterward.

The macOS window-capture API still renders the WebRTC video layer as black, so
the source-display artifact is the committed pixel evidence:
[`artifacts/virtual-display-app-a.png`](artifacts/virtual-display-app-a.png).

## 2026-09-22 WebKit black-frame fallback

The user still saw a black browser surface in the Tauri WKWebView. A separate
task-scoped Chromium viewer proved the same Neko track was healthy: the video
was playing at 1920x1080 and a canvas readback found all 2,304 sampled pixels
non-black with mean luminance 240.7. The fault was therefore isolated to the
WKWebView media-compositing path rather than X11, Chromium, Neko encoding, or
transport.

For this POC, the desktop view now polls bounded JPEG frames from the same page
through CDP. The rendered pixels are visible to the user and macOS capture,
while the existing CDP input and annotation paths continue to operate on that
same browser identity. This is a POC fallback, not a production streaming
architecture.

A regular packaged Tauri build also exposed the expected mixed-content
boundary: its secure `tauri://` page cannot be the production host for plain
HTTP/WebSocket media. The interactive test app therefore uses the localhost
Vite origin. Production needs authenticated HTTPS/WSS endpoints.
