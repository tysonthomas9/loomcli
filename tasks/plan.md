# Implementation Plan: Local Kernel Browser Feasibility Prototype

## Overview

Build a standalone, throwaway Tauri prototype that answers whether Loom can run
Kernel's headful Chromium image locally under Podman and use its streamed live
view plus CDP as one shared browser surface for a human and an interactive
agent. The prototype must produce decision evidence; it must not introduce a
production browser path into Loom.

The user requested both the plan and immediate startup on 2026-09-21. Work is
isolated on branch `prototype/local-kernel-browser` in its own worktree.

## Question and Decision Rule

Question: can a locally hosted Kernel browser provide acceptable interaction,
automation, annotations, multi-app isolation, and lifecycle ownership on the
macOS Loom desktop without migrating from Tauri to Electron?

Recommend adopting this architecture only if all critical checks pass:

- the container reaches an explicitly selected host development port without
  public exposure;
- the live view embeds in Tauri with usable focus, input, resize, and clipboard;
- CDP automation and the user visibly operate the same browser page;
- user input can invalidate stale agent actions;
- element annotations produce stable DOM context and a cropped screenshot;
- every container, port, profile directory, and process has exact ownership and
  deterministic cleanup;
- measured resource use is acceptable with two active browser identities.

Failure of a critical check produces a reject or continue-spike verdict rather
than a production implementation.

## Architecture Decisions

- Keep the prototype under `desktop/prototypes/kernel-browser/`; it is adjacent
  to the real desktop shell but unmistakably throwaway.
- Use Tauri 2 and a small browser-only frontend so results include the actual
  embedding, CSP, focus, and lifecycle constraints Loom would inherit.
- Use Podman first because it is Loom's existing preferred local container
  runtime. Docker compatibility is a later portability question.
- Treat Kernel's live view as presentation and CDP as the semantic automation
  channel. Both must resolve to the same browser identity.
- Bind control endpoints to loopback and expose only explicitly selected host
  development ports to the container.
- Use one browser container per isolated profile and multiple tabs for apps
  allowed to share identity.
- Give every resource a generated runtime ID plus Loom prototype labels. Never
  discover ownership from names or ports alone.
- Inject element annotation behavior through CDP. A React overlay over streamed
  pixels is insufficient as the canonical annotation source.
- Store no long-lived secrets and mount neither the repository nor the user's
  home directory into the browser container.

## Task List

Tasks are tracked in the `LOOMCLI` FleetDB workspace rather than duplicated in
`tasks/todo.md`.

### Phase 1: Runtime foundation

1. `LOOMCLI-221` — Prove local Kernel container lifecycle.
2. `LOOMCLI-222` — Prove host app reachability and embedded live view.

### Checkpoint: Runtime foundation

- Container and endpoint ownership are explicit and inspectable.
- Host routing is deterministic and does not expose an arbitrary port range.
- The actual Tauri surface accepts user input through the embedded live view.

### Phase 2: Shared interaction

3. `LOOMCLI-223` — Prove shared user and agent control with annotations.
4. `LOOMCLI-224` — Prove multiple browser apps and recovery.

### Checkpoint: Shared interaction

- User and CDP operations are observed in one browser session.
- User intervention invalidates stale automation on that page only.
- Annotation metadata and screenshot coordinates remain aligned after resize.
- Separate browser profiles maintain independent authentication state.

### Phase 3: Decision

5. `LOOMCLI-225` — Record the local Kernel browser architecture verdict.

### Checkpoint: Complete

- Exact commands and artifacts reproduce every claimed result.
- Results distinguish passed, failed, blocked, and unverified behavior.
- Local Kernel, Tauri child webviews, and Electron are compared against the
  same requirements.
- The verdict identifies which prototype artifacts stay on the throwaway branch
  and which design decisions, if any, should be rewritten into production code.

## Verification Matrix

| Capability | Evidence required |
|---|---|
| Runtime ownership | Container labels, runtime ID, loopback listeners, targeted stop |
| Host reachability | Request from Kernel Chromium to a task-owned host test server |
| Embedded interaction | Rendered Tauri live view plus pointer, keyboard, clipboard, resize |
| Shared control | User action and CDP action visibly affect the same page ID |
| Preemption | Stale command rejected after user-control epoch changes |
| Annotation | DOM descriptor, URL, bounds, note, and cropped screenshot |
| Multiple apps | Two active apps; shared and isolated profile cases |
| Recovery | Normal shutdown and forced-exit inventory/cleanup evidence |
| Resource cost | CPU, memory, startup time, and interaction latency observations |
| Security | Loopback-only control ports, no broad mounts, redacted endpoints |

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Podman VM cannot reach the macOS app reliably | High | Prove one explicit port before building UI; record runtime-specific translation |
| Stream focus or keyboard behavior is poor in Tauri | High | Test in the actual Tauri webview early, including clipboard and IME boundary |
| Pixel and DOM coordinates drift | High | Make CDP DOM bounds canonical and verify after resize and device-scale changes |
| CDP or live view is exposed beyond the host | High | Random loopback bindings, explicit endpoint checks, no LAN wildcard binding |
| Multiple containers consume excessive resources | Medium | Prefer tabs for shared identity; measure two isolated profiles before recommendation |
| Prototype grows into production code | Medium | Keep it in a named prototype directory and require a clean rewrite after a decision |
| Existing shared Podman resources are disturbed | High | Inventory first; use unique labels, names, ports, and profile paths; stop only owned resources |

## Explicit Non-Goals

- Production Loom API or OpenAPI changes.
- Integration into `AgentsPage` or the live lead view.
- Hosted Kernel support, billing, pools, or managed profiles.
- Full browser history, bookmarks, downloads, or permission UX.
- Packaging a container runtime for end users.
- Broad host networking or arbitrary local-port access.

## Initial Work Boundary

`LOOMCLI-221` begins with a runtime preflight and lifecycle harness. It may
inspect existing Podman state but must not reuse, stop, or mutate foreign
containers, machines, ports, or profile directories. Building or pulling a
Kernel image is a separate explicit step because it can consume substantial
time, bandwidth, and disk.

## Completed Result (2026-09-21)

All five spike tasks produced evidence. The critical interaction checks pass:
the native Tauri WKWebView plays Kernel's stream, CDP and the human operate the
same Chromium page, takeover rejects stale actions, DOM annotations produce
metadata plus a crop, two profiles remain isolated, and a forced VM loss is
recoverable using exact ownership labels.

A 2026-09-22 regression pass hardened queued-action identity, the final epoch
check, CDP disconnect/timeout handling, visible-page selection, sustained drag
state, refreshed annotation geometry, request-origin checks, and generated
runtime ownership. The automated suite passes 19 tests and the live pointer and
annotation paths were rechecked against app-a. Clipboard, IME, accessibility
input, and resize remain unverified; they are not required to answer this
throwaway POC's feasibility question and remain production follow-up work.

Podman was not the successful runtime. Homebrew Podman 6.1.2 repaired the
earlier Ignition failure, but AppleHV/gvproxy still could not keep a reachable
machine. The completed proof uses a dedicated Colima 0.10.3 VZ VM with Rosetta
and Docker compatibility. This is evidence about Kernel/Tauri feasibility, not
an endorsement of that dependency chain for production.

The decision is to preserve Tauri and continue only with a production runtime
design. Do not transplant this prototype directly: the current browser image
is amd64-only, privileged, about 1.25-1.87 GiB resident per browser in the
observed run, and an active stream consumed about 45-49% of one reported CPU
under Rosetta. The background-stream fix reduces an inactive browser to about
0.15% CPU but does not address baseline memory or packaging.

Evidence:

- [`LOOMCLI-221`](../desktop/prototypes/kernel-browser/evidence/LOOMCLI-221-preflight.md)
- [`LOOMCLI-222`](../desktop/prototypes/kernel-browser/evidence/LOOMCLI-222-live-view.md)
- [`LOOMCLI-223`](../desktop/prototypes/kernel-browser/evidence/LOOMCLI-223-shared-control-annotation.md)
- [`LOOMCLI-224`](../desktop/prototypes/kernel-browser/evidence/LOOMCLI-224-multiple-browsers-recovery.md)
- [`LOOMCLI-225`](../desktop/prototypes/kernel-browser/evidence/LOOMCLI-225-verdict.md)
