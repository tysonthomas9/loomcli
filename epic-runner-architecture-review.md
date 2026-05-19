# loom architecture review — v2 (post-vetting)

**Repo:** `loomcli-epic-runner` (branch `feature/epic-runner-direction-j`) talking to `fleet-db` (separate repo at `../../fleet-db`).
**Context:** Podman E2E (build a Slack-clone epic, lead `nova`, real Codex with mounted auth) surfaced 5+ bugs that resolve to three architectural questions. v1 of this document was vetted by another agent and three prescriptions changed. This is v2.

---

## Bug evidence (unchanged from v1)

| Bug | Where | Shape |
|---|---|---|
| A | `internal/infra/fleetdb/control_plane.go:658` sends `"status"` in agent-command POST; fleet-db's `internal/api/control_plane.go:657` schema has no such field; rejected via strict decode at `fleet-db/internal/api/request.go:98`. Every spawn fails 400. | Wire-schema drift |
| B | Commit 9aef2ae5 dropped `OrchestratorSessionID` from FleetDB Create/Update body (`internal/infra/fleetdb/agent.go:175`). Callers still read `updated.OrchestratorSessionID` at `internal/cli/epic/run.go:336`. Always empty on FleetDB. | Half-deprecation |
| D | UI handler passes repo name (`"Hello-World"`) where fleet-db expects a workspace key. `fleet-db/internal/storage/keys.go:24` **panics** (recovered) on invalid keys instead of returning 400. | Wire/validation drift, both sides |
| E | `AgentsPage.tsx:82-97` builds a shell command string and types it into the terminal via `pendingTerminalInput`. No backend endpoint. Silently no-ops when terminal runs Codex. | UI assumes terminal is a shell |
| H | No HTTP endpoint exposes `PTYManager.WriteToSession` (`internal/webui/terminal/pty_manager.go:336`). Only the WS path can deliver keystrokes. | Missing input channel |

---

## Three architectural claims — with vetted prescriptions

### Claim 1 — Diagnosis: schema drift at the loomcli ↔ fleet-db boundary. Prescription: typed contract, NOT collapsed boundary.

**Diagnosis (confirmed):**
- One consumer (loomcli) and 4933 lines of hand-maintained OpenAPI in `fleet-db/api/openapi.yaml`.
- loomcli's `internal/infra/fleetdb/` is entirely hand-rolled `map[string]any` and anonymous structs — no codegen headers.
- Three independent wire-format bugs (A, B, D) in two weeks.

**v1 prescription (wrong):** Import fleet-db as a Go module in local mode; keep HTTP only for distributed.

**Why that prescription failed vetting:** `docs/product/local-mode-product-spec.md:31` is an explicit product contract: *"Local mode and distributed mode must use the same persistence and API path."* Collapsing the boundary in local mode would also **hide exactly the class of bugs this dogfood found** — the integration drift would only surface in distributed deploys. The boundary is painful because it's manually duplicated and strict, not because it's inherently wrong.

**v2 prescription:** Keep the boundary. Codegen the `fleetdb` client from `fleet-db/api/openapi.yaml` using the same oapi-codegen pattern loomcli already uses for `internal/backend/api/gen/types.gen.go`. Add a real integration test that runs against fleet-db in a container — the dev-container harness from this dogfood already does this. Bug A becomes a compile error; Bug B becomes a "field not in schema" lint; Bug D becomes a typed mismatch on the call site.

---

### Claim 2 — The spec contradicts itself on "is Codex the orchestrator or a UI to one?"

**Evidence (sharpened during vetting):**
- `docs/product/lead-agent-epic-runner-spec.md:37` says *"Both paths use the same backend command path."* → architecture B (backend owns orchestration; UI/terminal/CLI are clients).
- Same spec, `lead-agent-epic-runner-spec.md:379` says *"It should execute inside the selected lead terminal, not through a separate hidden service path."* → architecture A (terminal owns orchestration).
- Implementation follows line 379: `AgentsPage.tsx:82-97` types the command into the terminal.
- In the E2E, Codex received the user's message, ran `loom epic run`, saw it fail (Bug A), and **unilaterally killed the orchestrator process** before investigating. Authority breach under A's reading; reasonable behavior under B's reading.

**v2 prescription:** Resolve the **spec contradiction first**, before any code change. Someone with product authority needs to delete one of the two contradicting paragraphs and update the other to be consistent.
- If line 37 wins (architecture B): introduce a backend `OrchestratorService` with action endpoints; UI button and Codex tool-call both target it.
- If line 379 wins (architecture A): accept that the Run-Epic button only works when the lead terminal is a shell, and either restrict lead role to shell-backed sessions or document the limitation.

This is the **load-bearing decision** for everything downstream — Bug E's fix shape depends entirely on which paragraph wins.

---

### Claim 3 — `Agent.OrchestratorSessionID` is a cache for a model that already exists; migrate to AgentSession join in three steps.

**Diagnosis (confirmed):**
- `domain.AgentSession` has `Kind=orchestration` (`control_plane.go:60`) and `ParentSessionID` (line 89).
- `domain.Agent.OrchestratorSessionID`'s own comment says it *"links to AgentSession{Kind=orchestration} that spawned this agent."* The intent is documented; the field is a denormalized cache.
- 9aef2ae5 made the cache unwritable on FleetDB but readers still depend on it. Textbook half-deprecation.

**v1 prescription (premature):** Delete `Agent.OrchestratorSessionID` and have callers do the join via AgentSession.

**Why that failed vetting:** `internal/store/control_plane_store.go:59` — `AgentSessionFilter` only supports `AgentID/NodeID/TaskID/Status/Limit`. No `Kind`, no `ParentSessionID`. You can't actually do the join via the store interface today; callers would have to filter client-side. Deleting the field before the join path exists swaps one bad pattern for another.

**v2 prescription:** Three steps in order.
1. Add `Kind` and `ParentSessionID` to `AgentSessionFilter` (and the matching index/query in fleetdb + memstore).
2. Add a helper on the store interface (e.g. `Agents().OrchestratorSession(ctx, ws, name)`) that does the join; migrate all four current readers off the cache.
3. Delete `domain.Agent.OrchestratorSessionID` and the wire field on both sides. Bug B disappears with the field.

---

## Revised sequencing (replaces v1)

1. **Resolve the spec contradiction** (Claim 2). Pick architecture A or B. Amend `lead-agent-epic-runner-spec.md` to be internally consistent. No code change makes sense until this is settled.
2. **Unblock today's dogfood (concrete blockers):**
   - Drop `"status"` from agent-command POST body in `internal/infra/fleetdb/control_plane.go:658` (Bug A — one-line hotfix).
   - Fix the workspace-key-vs-repo-name path on the loomcli side (Bug D, client). Separately, file against fleet-db: input validation should return 400, not panic (Bug D, server).
   - Give the Run Epic button a real execution path consistent with whatever won in step 1 (Bug E).
3. **Land Claim 3's three-step migration.** Add filters → migrate readers → delete field. Bug B closes here.
4. **Codegen + contract tests** (Claim 1 reframed). Repoint oapi-codegen at `fleet-db/api/openapi.yaml`. Wire the dev-container into CI as a contract-test harness. Future wire-format drift becomes a compile error.

**Why this order:** Step 1 is load-bearing for step 2's button fix. Steps 2 and 3 are independent and can run in parallel. Step 4 only pays off after the codebase has stopped reshaping its data model — doing it before step 3 would just codegen a schema that's about to change.

---

## What changed between v1 and v2

- **Claim 1 prescription reversed.** I'd reached for "collapse the boundary" — the vetter caught that this violates `local-mode-product-spec.md:31` and would hide future drift bugs in distributed deploys. Correct fix is typed contract on top of the existing boundary.
- **Claim 2 sharpened.** I'd characterized this as ambiguity in implementation. It's actually a contradiction in the spec itself (lines 37 vs 379). Code can't be fixed before the spec is fixed.
- **Claim 3 sequenced.** I'd proposed "just delete the field." The join path through `AgentSessionFilter` doesn't exist yet — you have to build it first.
- **Bias surfaced:** I keep reaching for "collapse layers / merge boundaries" when I see drift between two services. That's the wrong instinct in a product whose contract requires the boundary. The drift is a tooling problem (codegen, contract tests), not an architecture problem (merge).

---

## Asks for the next vetter

1. Is anything in v2 still wrong? Apply the same evidence-verification pass — open each cited line and check.
2. Does the sequencing assume too much? Specifically: can step 3 happen in parallel with step 2, or does the field migration need step 2 closed first?
3. Is there a fourth architectural question I'm missing? Suggest one.
4. Push back on my framing of step 1 as "load-bearing." Are there valid plays that ship step 2 fixes before settling step 1?

**Artifacts:**
- Full E2E report: `epic-runner-e2e-report.html` (same directory)
- v1 of this document is preserved in git history at the previous commit.
- Live state in `loomcli-dev` container on `http://localhost:8091` (workspace SLACK, epic SLACK-1, nova agent).
