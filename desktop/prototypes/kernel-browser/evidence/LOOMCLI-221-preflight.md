# LOOMCLI-221 Runtime Preflight Evidence

Date: 2026-09-21 (America/Los_Angeles)

Status: **blocked at provisioning; browser feasibility remains unverified**.

## Observed host state

- macOS data volume: 460 GiB total, 407 GiB used, 8.6 GiB available, 98% full.
- Existing machine: `podman-machine-default`.
- Existing machine state: stopped.
- Existing machine last up: 2026-09-06 07:07:14 -07:00.
- Existing machine allocation: 6 CPUs, 8 GiB memory, 60 GiB virtual disk.
- Existing machine runtime: AppleHV, rootless, user-mode networking, Rosetta enabled.

The inspection did not start the machine or enumerate/mutate its containers.

## Upstream Kernel boundary

Kernel's current `images/chromium-headful/run-docker.sh` uses:

- a fixed container name;
- `--privileged`;
- an 8 GiB container memory limit;
- fixed host ports for CDP and supporting services;
- a fixed UDP range when WebRTC is enabled; and
- `docker rm -f "$NAME"` before launch.

The script is useful as upstream implementation evidence but is not safe to run
directly on this shared host. Source:
<https://github.com/kernel/kernel-images/blob/main/images/chromium-headful/run-docker.sh>.

## Decision

Do not start `podman-machine-default` for this prototype. A privileged Kernel
container in the shared VM would weaken ownership evidence and could compete
with foreign containers, networks, images, and storage.

Do not create the dedicated machine yet either. With only 8.6 GiB available,
there is insufficient headroom for a second VM, the Kernel build context/image,
and runtime artifacts. Free-space remediation or an explicitly approved use of
the shared machine is required before the runtime proof can proceed.

The prototype preflight now requires `LOOM_KERNEL_PODMAN_MACHINE` and refuses
`podman-machine-default`, so it cannot silently cross this boundary.
