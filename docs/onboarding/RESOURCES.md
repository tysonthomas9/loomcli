# Loom Resources

Curated sources for this course. Lessons draw their claims from here, not from
general knowledge about Go, Redis, or agent frameworks.

**A note on what "trusted source" means for an internal codebase.** For most
topics you would go find a book. Here the highest-trust sources are the repo's
own checked-in docs, the code, and the git history — because they are the only
sources that describe *this* system. External references appear below only for
genuinely external technology. When a repo doc and the code disagree, the code
wins and the disagreement is worth a lesson.

Every entry below was confirmed to exist at loomcli `ce4df56fb` / fleet-db
`000793b`. Paths are relative to each repo root.

---

## Knowledge — loomcli

### Start here, in this order

- **`docs/loom-glossary.md`** (176 lines)
  The shared dictionary and concept map. Ordinary words used as domain terms —
  `loom`, `skill`, `blocked`, `stage`, `phase`, `sandbox`, `trust level`. Read
  before reasoning about any overloaded term. Compressed into
  [reference/glossary.html](./reference/glossary.html).
- **`CONTEXT.md`** (91 lines)
  Ubiquitous language for task execution and streaming, in define-then-forbid
  form (each term lists the aliases to *avoid*). This is where `TaskRun Root`,
  `Task Change Set`, `Task Branch`, and the SSE frame taxonomy are pinned down.
- **`AGENTS.md`** (195 lines)
  The operating manual: shared runbooks, the terminology handshake, the local
  gate environment, driver runtime auth, the workflow sandbox (SB3) and egress
  modes (SB4), and the mandatory "Landing the Plane" session-completion sequence.
  Denser and more current than the README — read it twice.
- **`README.md`**
  Product-level orientation: what Loom is, install, quick start, the two valid
  control-plane paths (local mode vs cloud mode), `loom serve`, fleet mode.

### Product and architecture

- **`docs/product/README.md`**
  An ordered reading list covering 14 of the 15 product specs, and the only place
  that says what order to read them in. Two caveats worth knowing before you trust
  it: its numbering repeats `12.` (so it visibly ends at 13), and it omits
  `orchestrator-worker-model.md` entirely. Use it as the index, then run
  `ls docs/product/` and check for stragglers.
- **`docs/product/daemon-agent-runtime-architecture.md`** — daemon, agent runner,
  local vs cloud mode, ownership leases, decentralized task claiming.
- **`docs/product/agent-lifecycle-state-machine.md`** — canonical agent, run, and
  task states plus allowed transitions. The reference when a status confuses you.
- **`README.md:304-306`** (Configuration → post-run completion hooks) and fleet-db
  **`docs/architecture.md:357`** (§ Agent Completion Hooks — Write-Before-Stamp) —
  the daemon, not the agent's prompt, does the bookkeeping after a successful run,
  and a failed write reopens the task. There is no single `lifecycle-hooks` doc;
  this behaviour is documented once on each side of the split.
- **`docs/product/session-artifact-contract.md`** — the evidence every run must
  leave behind. Read before claiming a run "worked".
- **`docs/design/execution-isolation.md`** — the three execution levels, what is
  containerized, and an explicit list of what is *not* isolation. Required before
  treating any sandbox knob as a security boundary.
- **`docs/adr/`** — architecture decision records: the *why* behind current shape.
- **`docs/arch/`**, **`docs/design/`**, **`docs/epics/`**, **`docs/observability/`**,
  **`docs/security.md`**, **`docs/api.md`** — the rest of the doc tree.

### Testing

- **`docs/testing/README.md`** — index of the testing docs.
- **`docs/testing/test-patterns.md`** (~17k) and **`test-infrastructure.md`** (~11k)
  — how tests are actually written and wired here.
- **`docs/testing/go-backend-tests.md`**, **`frontend-tests.md`** (~42k, the largest
  testing doc), **`e2e-cli.md`**, **`e2e-ui.md`**, **`e2e-preflight.md`**.
- **`docs/testing/local-mode-podman-e2e.md`** — the real-runtime local-mode stack.
- **`docs/testing/known-issues.md`**, **`coverage-gaps.md`** — read before assuming
  a failure is yours.
- **`.agent-skills/loom-pr-test/SKILL.md`** — the runbook for real Loom PR runtime
  testing: local-mode stacks, browser validation, FleetDB compatibility checks.

## Knowledge — fleet-db

- **`docs/architecture.md`** — canonical. Layers, data model, event sourcing,
  Redis key schema. (`ARCHITECTURE.md` at the root is a 13-line retired pointer
  holding no architecture content at all — `AGENTS.md:36` lists it as "Retired
  pointer (do not use)". The pre-migration text survives only in that file's own
  revision history.)
- **`docs/agents/domain-primer.md`** and **`docs/agents/codebase-map.md`** — written
  for exactly this audience: an agent or person arriving cold. Start here.
- **`docs/agents/index.md`**, **`docs/agents/worktree-agents-template.md`**.
- **`docs/agents/contracts/task-contract.md`** and **`pr-contract.md`** — what a
  task and a PR must satisfy in this repo.
- **`docs/rpc-spec.md`** — JSON-RPC 2.0 wire protocol and method reference.
- **`docs/api-governance.md`** — versioning, deprecation, stability guarantees.
  Read before adding or changing an endpoint.
- **`docs/auth.md`**, **`docs/pubsub.md`**, **`docs/deployment-gcp.md`**,
  **`docs/migrations/`**, **`docs/roadmap.md`**, **`docs/adr/`**.
- **`README.md`** — the layered diagram plus the write-path/read-path summary, and
  a doc table that is a good map of the rest.
- **`MANUAL_TESTING.md`** (~37k) — exhaustive manual scenarios. A reference to
  search, not to read front to back.
- **`loom.yaml`**, **`prompts/`**, **`harness/policy.yaml`**, **`scripts/harness/`**
  — fleet-db is itself an agent-first repo driven by Loom. These define how work
  is planned, implemented, reviewed, and gated in it.

## Knowledge — the code as a source

- **`git log` and `git blame` on a file you are about to change.** This codebase
  moves fast and the reason for a shape is usually in the commit message rather
  than a doc. Treat blame as a primary source, not a last resort.
- **`Makefile`** (loomcli, ~41k) — the real interface to build, test, lint, gate,
  and the local-mode stack. When a doc and the Makefile disagree, the Makefile is
  what CI runs.
- **`.golangci.yml`** (loomcli, ~28k) — the lint contract, and by far the most
  precise statement of this repo's Go conventions.

## Wisdom — where the answers that are not written down live

- **Code review on your own PRs.** The highest-bandwidth feedback loop available,
  and the only one that corrects judgment rather than facts. Open PRs early and
  small.
- **The ADR trail** (`docs/adr/` in both repos). Reading a decision record next to
  the code it produced is how you learn what this team considers a good tradeoff.
- **Existing PRs and issues.** `loom data ready`, `loom data show <id>`. Reading
  how a task was scoped, argued, and closed teaches the working norms faster than
  any doc.
- **The terminology handshake** (`AGENTS.md`). Practising it out loud with a
  colleague before a slow or irreversible run is the fastest way to internalise
  the trap words.

### Gaps

These are unfilled and drive future work on this course:

- **`docs/testing-terminology.md` does not exist.** `AGENTS.md` cites it twice as
  the canonical map of the four-axis testing vocabulary and the trap words. A
  repo-wide search for *realness* matches `AGENTS.md` and nothing else. The axis
  names are reliable; the matrix is unwritten. Worth filing.
- **No team-communication channels are recorded here.** Ask which channels,
  standups, and review rotations exist, and add them — for an internal codebase
  they are the real "community" and this section is thin without them.
- **No external references yet** for the genuinely external pieces (Tauri, Vite,
  Redis Lua scripting, SSE semantics, Cobra). Add them only where a lesson
  actually needs one; the learner already knows the general concepts, so most
  external links would be noise.
- **No production/on-call material.** Undecided whether this course should cover
  incident response — flagged in [NOTES.md](./NOTES.md).
