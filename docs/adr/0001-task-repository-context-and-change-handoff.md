---
status: accepted
---

# Model task repository context separately from access and hand off pushed commits

The first implementation task is documented in
[Task repository context implementation plan](../design/task-repository-context-implementation-plan.md).

A Task has an optional Primary Repository and an exact set of Selected Repositories that provide preferred working context, not authorization. Groups are editing shortcuts that expand to exact names. An active AI backend may ask Loom to acquire another registered repository; Loom adds it to both the active TaskRun and the Task, provisions it in the existing composite TaskRun Root, and continues the same native backend session. Repository access remains governed independently by role, tool, and sandbox policy.

Each TaskRun owns a separate composite root, while scheduler retries of that same run reuse its root and backend session. At most one implementation or correction run may write a Task's branches at a time; concurrent review runs may consume immutable results. Every changed repository has a stable Task Branch. The AI backend authors commits, Loom pushes and verifies them through a credential-bearing Git Push Proxy, and normal Task Branch history is not force-pushed. A clean no-change result is valid, but dirty state continues the backend instead of allowing completion. Exhausted push failure fails the TaskRun and leaves the Task blocked with a delivery reason.

Successful publication creates an immutable, versioned Task Change Set containing the confirmed base and head of every changed repository. Review runs use separate roots pinned to one version. A multi-repository Task may produce one pull request per changed repository and passes aggregate review only when every required repository result passes; corrections append commits and produce a new version. Successful roots may be removed after remote commits and required artifacts are durable, while failed roots use a longer diagnostic retention policy.

## TaskRun Root lifecycle ownership

FleetDB records the logical TaskRun Root lifecycle: owning TaskRun and node, repository membership, lifecycle state, retention deadline, and fencing identity. The node-local **TaskRun Root Manager** owns the physical root, its TaskRun Root Manifest, all contained Git worktrees or sandbox clones, and the actual cleanup operation. FleetDB does not manipulate node-local paths, and Git's per-repository worktree metadata is not Loom's lifecycle source of truth.

Worktree totals are derived from durable manifests rather than maintained as a separate counter: root count is the number of active or retained manifests, and worktree count is the sum of their repository entries. The node reports this inventory for capacity and observability. A reconciler compares FleetDB lifecycle state, local manifests, and provider state such as `git worktree list --porcelain`; it repairs or removes orphaned physical state after crashes and restarts.

Provisioning rollback, cancellation, lost leases, terminal retention expiry, Workspace removal, and orphan reconciliation all invoke the same idempotent, fencing-aware cleanup path. The local Git adapter removes each child through its owning repository with `git worktree remove` before deleting the composite root, then prunes stale registrations. A stale worker may not clean a newer fenced root. Remote adapters apply the same lifecycle contract by deleting their sandbox or clones. Successful roots are eligible for cleanup only after confirmed remote commits and required artifacts are durable; failed roots use the longer diagnostic retention policy.

## Considered options

- Running from the global Workspace was rejected because it gives the backend a noisy shared starting location and cannot represent a deliberate subset.
- Treating the Repository Set as an authorization boundary was rejected because access policy already belongs to the role, tool, and sandbox layers.
- Sharing a mutable directory between implementation and review was rejected because a pushed, immutable change set supports local and remote runners consistently.
- Letting Loom create commits was rejected because commit authorship and completion belong to the AI backend; Loom owns credentialed publication and verification.
- Force-pushing or rolling back already-published repositories after a partial multi-repository push was rejected in favor of retaining confirmed history and retrying only incomplete publication.
- Relying only on directory scans or Git's worktree registry was rejected because neither records TaskRun retention, fencing, remote sandboxes, or the intended repository set; a durable manifest plus reconciliation is required.

