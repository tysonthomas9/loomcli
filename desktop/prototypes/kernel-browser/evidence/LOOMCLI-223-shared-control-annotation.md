# LOOMCLI-223 Shared Control and Annotation

Date: 2026-09-21 (America/Los_Angeles)

Status: **passed**.

- The lead attached to app-a's existing page target through the browser-level
  CDP socket and clicked `Confirm selected workflow`. The shared fixture
  visibly changed to `Workflow confirmed by lead agent`.
- A lead action queued at epoch 0 was rejected with HTTP 409 after human
  takeover incremented that browser to epoch 1. The UI rendered `Stale lead
  action rejected after human control changed.`
- Human takeover sent `KERNEL_SET_READ_ONLY` to the selected live view. Kernel
  replied with `KERNEL_READ_ONLY_CHANGED` and `readOnly=false`.
- Annotation mode was injected through CDP into the same page. Selecting the
  fixture paragraph produced selector, tag, text, URL, title, note, bounding
  rectangle, viewport, scale factor, and timestamp. CDP captured the exact
  rectangle as a PNG.

Artifacts:

- [`artifacts/annotation-app-a.json`](artifacts/annotation-app-a.json)
- [`artifacts/annotation-app-a.png`](artifacts/annotation-app-a.png)
- [`artifacts/lead-browser-ui-connected.png`](artifacts/lead-browser-ui-connected.png)

The DOM descriptor is canonical; no attempt is made to infer an element from
streamed pixels.
