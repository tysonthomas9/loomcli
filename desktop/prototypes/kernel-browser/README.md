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

Exit status `2` means the required Podman runtime is not safely available: no
dedicated machine was selected, it does not exist or is stopped, or its API is
unreachable. That is a provisioning boundary, not a failed browser verdict.

The preflight requires an explicit dedicated machine and refuses the shared
default machine:

```sh
LOOM_KERNEL_PODMAN_MACHINE=loom-kernel-browser-<runtime-id> \
  desktop/prototypes/kernel-browser/scripts/preflight.sh
```

No machine name is inferred from Podman's default connection. Creating or
starting a machine is intentionally outside this script because both operations
mutate host state.

## Safety boundary

Every runtime resource must carry a generated prototype runtime ID and explicit
ownership labels. The prototype must inventory shared host state before startup
and may stop or remove only resources whose labels match that exact runtime ID.

The upstream Kernel `run-docker.sh` is intentionally not called directly. It
uses fixed ports, a fixed container name, `--privileged`, and removes the named
container before launch. The Loom prototype must allocate ports and identify
resources itself.

## Current provisioning decision

Use a dedicated task-owned Podman machine for the Kernel prototype. Kernel's
upstream headful launcher runs an 8 GiB privileged container; sharing the
existing default machine would mix that trust and resource boundary with
foreign containers.

The initial capacity blocker was cleared when the macOS data volume reached
70 GiB free. A dedicated AppleHV machine was then created, but Fedora CoreOS
Ignition failed on first boot and left the guest in emergency mode. The failed
task-owned machine and all of its residual files were removed. No Kernel image
or container was created. Continuing now requires a supported Podman runtime
path rather than another identical AppleHV retry. See
[`evidence/LOOMCLI-221-preflight.md`](evidence/LOOMCLI-221-preflight.md).
