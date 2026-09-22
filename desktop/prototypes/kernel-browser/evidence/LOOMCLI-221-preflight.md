# LOOMCLI-221 Runtime Preflight Evidence

Date: 2026-09-21 (America/Los_Angeles)

Status: **Podman path rejected; task-owned Colima fallback passed lifecycle**.

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

## Dedicated-machine attempt

After the user freed space, the data volume had 70 GiB available. The prototype
created `loom-kernel-browser-221` with these explicit limits:

- AppleHV, rootless, user-mode networking;
- 4 CPUs;
- 10 GiB memory and 2 GiB swap; and
- 40 GiB maximum virtual disk.

No shared machine was started. Podman 5.8.2 reported that the dedicated machine
started successfully, but it never became reachable. Its serial log showed
Fedora CoreOS 44.20260829.3.1 entering emergency mode during Ignition:

```text
creating or modifying user "core": exit status 10
useradd: cannot lock /etc/group; try again later.
Ignition failed: failed to create users/groups
```

This matches the failure shape tracked in upstream Podman issue #28439, where
AppleHV reports startup success but the guest remains unusable after Ignition
or root-filesystem failures:
<https://github.com/containers/podman/issues/28439>.

The installed Homebrew Podman 5.8.2 had no usable fallback provider: QEMU was
unsupported and `libkrun` had no installed `krunkit` binary.

The task-owned VM was stopped by terminating only its exact stuck Podman/vfkit
processes after normal `podman machine stop` hung. `podman machine rm -f`
removed the managed VM, and the three exact residual task files (lock, EFI
variable store, and Ignition socket) were deleted. Final verification showed:

- no managed prototype machine;
- no prototype connection;
- no prototype process or listener;
- 70 GiB free; and
- no Kernel image or container created.

## Homebrew Podman 6.1.2 retry

With explicit user approval, Homebrew Podman was upgraded from 5.8.2 to 6.1.2.
Homebrew removed the old keg, and the new client selected AppleHV. It did not
install `krunkit`, so `libkrun` remained unavailable.

A fresh `loom-kernel-browser-221` machine used the same bounded resources and
Podman's 6.1 machine OS. This materially improved first boot:

- Ignition completed successfully;
- the guest reached `multi-user.target`;
- `sshd.service`, `podman.socket`, and the Podman `ready.service` started; and
- the prior `/etc/group` lock failure did not recur.

The machine still did not become a stable Podman endpoint. A normal start
reported success, but AppleHV exited with the launch command and the machine
immediately returned to `stopped`. Keeping a managed launch session open kept
AppleHV alive, but Podman remained indefinitely in `Starting`; SSH returned
`Connection reset by peer`. In the host network log, gvproxy repeatedly sent
ARP requests to the guest and received zero bytes back.

Podman accepted `machine set --user-mode-networking=false` without error but
left `UserModeNetworking` set to `true`. Recreating the machine without the flag
also selected user-mode networking, so the supported CLI exposed no alternate
AppleHV network path for this build.

The second task-owned machine, its two zero-byte temporary start-log
directories, and its exact residual lock and EFI-variable files were removed.
Final verification again showed no prototype machine, connection, process,
listener, or Kernel image/container. The pre-existing
`podman-machine-default` connection records remain untouched.

The runtime lifecycle proof remains blocked, not failed: Kernel itself has not
run. The next safe option is a Podman distribution that includes the supported
`libkrun`/`krunkit` provider, followed by a new task-owned machine and the same
preflight.

## Final fallback and lifecycle result

The reviewed Homebrew `krunkit`/libkrun path reproduced the same gvproxy
reachability class and was removed. A dedicated Colima 0.10.3 profile then
provided the safe fallback:

- profile: `loom-kernel-browser-221`;
- VZ, 4 CPUs, 10 GiB RAM, 40 GiB sparse disk, Docker runtime;
- Rosetta enabled because Kernel's published Chromium image is amd64-only;
- no activation of the global Docker context and no SSH-config mutation; and
- exact image digest
  `sha256:7aa6dc616440fbe3f8886cec700dc7533aa2a0ec29b999102aa6cad4ac3e6f50`.

Two containers reached CDP readiness with the runtime harness. After a VM
process loss, both persisted as `Exited (255)`. A new start correctly refused
their occupied names. `runtime.sh stop` selected only containers carrying both
`io.loom.prototype=local-kernel-browser` and
`io.loom.runtime-id=LOOMCLI-221`, removed them, and a following start recreated
both successfully. This closes the container lifecycle and ownership check;
the external VM lifecycle remains a packaging concern for production.
