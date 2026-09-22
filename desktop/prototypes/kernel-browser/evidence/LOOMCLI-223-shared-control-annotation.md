# LOOMCLI-223 Shared Control and Annotation

Date: 2026-09-21 (America/Los_Angeles)

Status: **passed**.

- The lead attached to app-a's existing page target through the browser-level
  CDP socket and clicked `Confirm selected workflow`. The shared fixture
  visibly changed to `Workflow confirmed by lead agent`.
- A lead action queued at epoch 0 was rejected with HTTP 409 after human
  takeover incremented that browser to epoch 1. The UI rendered `Stale lead
  action rejected after human control changed.`
- Human takeover keeps Kernel's Neko viewer read-only and activates a
  Loom-owned input surface. Validated, normalized pointer and keyboard events
  are dispatched to the same Chromium page through CDP. Neko desktop input is
  disabled because its amd64 Xorg input path deadlocks under Rosetta after
  sustained pointer activity.
- The actual desktop UI survived 40 pointer interactions over 19 seconds in
  app-a and 24 interactions over 11 seconds in app-b. The independent health
  probe then kept both CDP and X11 responsive for 30 seconds (24 checks), plus
  a final 10-second run (8 checks) after app-b activity.
- A click dispatched through the bridge changed the fixture from `Waiting for
  lead action` to `Workflow confirmed by lead agent`. Text typed through the
  actual Loom overlay produced `Hi there` in the focused remote input.
- Annotation mode was injected through CDP into the same page. Selecting the
  fixture paragraph produced selector, tag, text, URL, title, note, bounding
  rectangle, viewport, scale factor, and timestamp. CDP captured the exact
  rectangle as a PNG.

Artifacts:

- [`artifacts/annotation-app-a.json`](artifacts/annotation-app-a.json)
- [`artifacts/annotation-app-a.png`](artifacts/annotation-app-a.png)
- [`artifacts/annotation-control-bridge-app-a.json`](artifacts/annotation-control-bridge-app-a.json)
- [`artifacts/annotation-control-bridge-app-a.png`](artifacts/annotation-control-bridge-app-a.png)
- [`artifacts/lead-browser-ui-connected.png`](artifacts/lead-browser-ui-connected.png)

The DOM descriptor is canonical; no attempt is made to infer an element from
streamed pixels.
