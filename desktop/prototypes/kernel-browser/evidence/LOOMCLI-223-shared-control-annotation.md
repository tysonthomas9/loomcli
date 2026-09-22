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
- A follow-up regression found that iframe-relative coordinates incorrectly
  treated Neko letterboxing and Chromium's 143 px window chrome as page
  content. The corrected transform maps the visible fixture button to page
  coordinate `(678, 298)` and the button activates in both browser apps.
- Annotation selection is now one-shot. Its capture listener removes itself
  after selecting one element, so subsequent human clicks are no longer
  prevented. The regression sequence selected `section.target`, captured it,
  and then activated `Confirm workflow` without reloading the page.
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

## 2026-09-22 regression hardening

- Queued actions now retain both `appId` and epoch when the visible tab changes.
  A live UI sequence queued on Research, took human control, switched to
  Operations, and correctly rejected the stale Research action.
- The epoch is checked inside the CDP operation immediately before dispatch,
  closing the race between the HTTP check and browser mutation.
- CDP calls now time out and reject all pending commands when the socket closes.
- Held-button state and modifiers survive coalesced pointer movement; drag and
  release events outside the page clamp to its edge rather than being dropped.
- A live pointer dispatch mapped stream coordinate `(328.5, 258.1)` to fixture
  coordinate `(677, 297)` and changed `#action-status` to
  `Workflow confirmed by lead agent`.
- A live annotation dispatch selected the fixture paragraph through the same
  pointer bridge. Capture returned current viewport bounds, document bounds,
  DOM context, and a cropped screenshot.
- The focused regression suite passes 19 tests. The corrected ownership-aware
  health probe kept both live browsers' CDP and X11 paths healthy for a further
  20-second run. Clipboard, IME,
  accessibility-input, and resize quality remain unverified for this POC.
