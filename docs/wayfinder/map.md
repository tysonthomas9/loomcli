# Wayfinder map — Per-repo execution environments across sandbox providers

**Canonical artifact:** loom **`LOOMCLI-137`** (workspace `LOOMCLI`, local desktop loom).
This file mirrors it for bootstrapping a fresh session — the loom map is authoritative.

**Resume:** in a new session run `/wayfinder` with `LOOMCLI-137`. It loads the map, picks the first
frontier ticket, claims it (assign to you), resolves ONE decision. Never resolve >1 ticket/session
(research excepted).

---

## Destination
A locked architecture spec for per-repo execution environments that works across **all** sandbox
providers (Daytona, local, Docker, extensible to future). Every repo resolves to the right,
**validated** execution environment (image/snapshot + resources) with no per-repo hardcoding.
**Plan-only** — done when nothing's left to decide and an implementer/agent can build from the spec.

## Notes / standing givens
- **Snapshot = env, not code.** Snapshot holds OS + toolchain only; code cloned fresh at task time;
  snapshot hash = env-spec hash; a code change rebuilds only when it touches `.loom/execution.yaml`.
- **Warming is a materialize-time bake, not a volume** (R1+R3): the portable dep-warming strategy across
  providers is bake-deps-into-the-artifact (warm snapshot / E2B template / Codespaces prebuild), NOT a
  live shared volume (Daytona volumes unfit for write-heavy caches; E2B/Codespaces have none).
- **`materialize()` is polymorphic + capability-flagged** (R2+R3): Daytona builds a cloud image, E2B a
  template, local does verify/version-manager (no artifact). Advertise capabilities (persistentVolume,
  pauseResume, memorySnapshot, docker, browser); `validate()` gates feasibility; `resources` = a request.
- **Fail-open never skips validation** (D1): it substitutes the Loom-shipped, versioned **built-in universal
  EnvSpec** (same resolve→validate→materialize pipeline, flagged `env_mode=fallback|stale`); any
  declared-but-unsatisfiable spec fails CLOSED (`repo_environment_not_ready` = blocked, non-terminal).
- Two independent agent designs (Fable + Codex) converged ~80%; divergences = the open decisions.
- Skills: `/grilling` + `/domain-modeling`, `/research`, `/prototype`.

## Tickets

| Key | ID | Type | Status | Blocked by | Question (gist) |
|---|---|---|---|---|---|
| R1 | `LOOMCLI-138` | research | **closed** ✓ | — | Daytona caps → asset `research/R1-daytona-capabilities.md` |
| R2 | `LOOMCLI-139` | research | **closed** ✓ | — | Local-runtime isolation → asset `.../R2-local-runtime.md` |
| R3 | `LOOMCLI-140` | research | **closed** ✓ | — | Provider landscape → asset `.../R3-provider-landscape.md` |
| F2 | `LOOMCLI-141` | grilling | **closed** ✓ | — | Fail-open vs fail-closed when env unresolved/unvalidated |
| F1 | `LOOMCLI-142` | grilling | **FRONTIER** | — (unblocked) | EnvSpec schema + capability model (`.loom/execution.yaml`) |
| A | `LOOMCLI-143` | grilling | blocked | 142 | ProviderAdapter interface contract |
| WARM | `LOOMCLI-144` | grilling | blocked | 142 | Startup-warming: bootstrap vs Volume vs warm-code snapshot |
| REV | `LOOMCLI-145` | grilling | blocked | 142 | Revision/hash addressing, artifact naming, drift & rebuild/GC |
| AUTH | `LOOMCLI-146` | grilling | blocked | 142 | Env-authoring agent contract + validate-feedback loop |
| RES | `LOOMCLI-147` | grilling | blocked | 142 | Resource sizing model + secrets boundary |
| PADAPT | `LOOMCLI-148` | grilling | blocked | 143 | Per-provider adapter design (daytona/local/docker) |
| RESOLVE | `LOOMCLI-149` | grilling | blocked | 143 (141 ✓) | Resolver + trigger/lifecycle + multi-repo/cross-repo |

## Decisions so far
- **R1 Daytona capabilities** (`LOOMCLI-138`) — named snapshots = durable warm handle (build cache only 24h/per-runner); Volumes FUSE/S3, unfit for write-heavy caches; resources cgroup-enforced (host `/proc` lies); amd64-only. → warm-code snapshot > dep-volume.
- **R2 Local-runtime** (`LOOMCLI-139`) — local runtime has ZERO toolchain isolation (host process/PATH); no `Repo` toolchain field; ship verify-only + version-manager (mise/nix); local containers later; **security:** repo code runs on host as root w/ real creds.
- **R3 Provider landscape** (`LOOMCLI-140`) — one adapter generalizes IF resources=request + capability-advertisement + mandatory materialize-time dep-bake fallback for volume-less providers.
- **D1 Fail-open vs fail-closed** (`LOOMCLI-141`) — hybrid per-state: spec-less repos fail-OPEN onto the validated built-in universal EnvSpec (flagged + deduped per-repo onboarding issue); validate/materialize failures fail-CLOSED (`repo_environment_not_ready` = blocked/non-terminal, build errors feed AUTH); stale-artifact runs allowed + flagged. **Never run unvalidated.** Flag surfaces: run metadata + task-issue comment only (no PR-body requirement).

## Not yet specified (fog)
- Per-provider capability matrices (graduates after F1 + A + PADAPT).
- Exact reconciler event/queue wiring (graduates after RESOLVE) — now includes env-ready unblock events
  for blocked tasks, materialize retry policy, and provider-rerouting on validate failure (from D1).
- Rollout/migration of existing repos onto the model.
- **Local-execution trust boundary** (from R2): is a non-isolating local provider acceptable for untrusted repos, or must local require containerization? Graduates with PADAPT (`LOOMCLI-148`).
- **Supervisor leniency for degraded runs** (from D1): should env-caused thrash on fallback/stale runs count toward task no-progress quarantine, or be attributed to the env?

## Out of scope
- Implementing the feature (plan-only) — tracked in `LOOMCLI-108`/`131`.
- GitHub-cred / token-write tickets (`LOOMCLI-112`/`123`) unless a decision depends on them.
- Non-env improvements in `LOOMCLI-108`.
