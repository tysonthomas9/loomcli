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
