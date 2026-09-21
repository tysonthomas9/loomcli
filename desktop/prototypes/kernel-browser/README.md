# Local Kernel Browser Prototype

> Throwaway decision prototype. This is not a production Loom browser module.

This prototype answers whether Loom can keep its Tauri desktop shell while
running Kernel's headful Chromium image locally and sharing one browser session
between a human live view and agent automation over CDP.

The implementation plan and evidence requirements live in
[`tasks/plan.md`](../../../tasks/plan.md). Work is tracked under `LOOMCLI-220`,
starting with `LOOMCLI-221`.

## Current status

- Worktree and prototype branch created from `origin/v5` at `32811a698`.
- Architecture and pass/fail plan recorded.
- Runtime preflight records the current container-runtime boundary without
  starting a machine or touching existing containers.
- No container, image, listener, or profile directory has been created by the
  prototype yet.

Run the non-mutating preflight from the Loom repository root:

```sh
desktop/prototypes/kernel-browser/scripts/preflight.sh
```

Exit status `2` means Podman exists but its selected machine is not reachable.
That is a provisioning boundary, not a failed browser verdict.

## Safety boundary

Every runtime resource must carry a generated prototype runtime ID and explicit
ownership labels. The prototype must inventory shared host state before startup
and may stop or remove only resources whose labels match that exact runtime ID.

The upstream Kernel `run-docker.sh` is intentionally not called directly. It
uses fixed ports, a fixed container name, `--privileged`, and removes the named
container before launch. The Loom prototype must allocate ports and identify
resources itself.
