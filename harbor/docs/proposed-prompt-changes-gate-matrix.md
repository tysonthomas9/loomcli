# Proposed prompt changes — spec-coverage ledger (post-mortem of team-cursor-full-151445)

Status: PROPOSED rev 2, not applied.

Leakage rule (quality-eval-plan.md "Leakage posture"): no agent sees any metric,
threshold, tool name, measurement artifact, or the grader's structure. Rev 1 of this
proposal failed that rule: its "gate" taxonomy (protocol/port, API contract family,
failure/recovery behaviour, UI journey) was the five correctness gates with the nouns
removed, and "gate"/all-or-nothing urgency leaked the binary scoring shape. Rev 2 uses
only the engineering practice a good lead applies to any spec: enumerate the
instruction's own requirements, own each one, verify as the external client would,
slice before breadth, manage the deadline.

Also found while checking: `prompts-generic/lead-persistent-verifier-tasks-dual.md:51`
and `prompts-generic/qa-backend-persistent-tasks.md:37` name "WebSocket and IRC
contracts" — existing leaks, not used by the team arm; any experiment that used them
needs a contamination footnote.

## 1. `lead-persistent-team.md`

### 1a. Seeding — insert after the decomposition paragraph (after line 23)

```
Decompose from the instruction's own requirements, not from your sense of a
typical product. Before creating child tasks, list every requirement in the
product instruction that an external user or client could observe failing —
anything it says a user does, a client connects to, a response contains, or
the system keeps doing after something goes wrong — each as one line, in the
instruction's words. Put that list in the epic description under
`REQUIREMENTS:` with one line per requirement.

Every requirement line must be covered by at least one implementation task
carrying a lane label (`frontend` or `backend`). Create those tasks first with
`--priority 0` or `1`. Tasks that are not needed for any requirement line
(polish, settings, conveniences the instruction does not ask for) get
`--priority 2` or lower and may depend on requirement tasks, never the
reverse. Order work so that the shortest path through the product — a user
gets in, does the main thing, sees the result — is integrable early, before
breadth.

A requirement may not be covered only by a task whose deliverable is a design.
Write each task's acceptance criteria as what a user or client observes, not
as implementation steps.
```

### 1b. Orchestrate pass — insert before "Review routing (exact protocol):" (line 36)

```
Coverage ledger, first on every pass: compare the epic's `REQUIREMENTS:` list
against `loom data list --output json`. Mark each requirement INTEGRATED
(its implementation tasks are closed), IN PROGRESS (claimed), or UNOWNED (its
tasks are all open and unclaimed, or it has no task). Post the ledger as one
comment on the epic: `COVERAGE: <requirement>=<state>; …`.

- A requirement UNOWNED on two consecutive passes: raise its tasks to
  `--priority 0`; if the only thing holding it is a design not yet written
  (`architect` label, no design), remove the `architect` label from the
  smallest such task and note on it that the implementer designs as they go.
- Closed means integrated, not working. Treat a requirement as demonstrated
  only when a QA task has exercised it on the integrated head the way the
  external user or client would.

The pass message says how many minutes remain. Manage the deadline:
- 60 minutes or less: create no new priority-2-or-lower tasks; do not approve
  designs for them (reject with `FEEDBACK: deferred — deadline`).
- 30 minutes or less: set every open, unclaimed task of priority 2 or lower to
  `--status deferred` so remaining effort goes to requirements. Deferring is
  not closing.
- 10 minutes or less: file one QA task, "Verify: walk the product end to end
  on the integrated head", listing every requirement line for a black-box
  PASS/FAIL.
```

### 1c. Verification duty — replace "Each description must say exactly what to verify …" (lines 66–68)

```
Each verification task names the requirement line(s) it checks and tells QA
to exercise them the way the external user or client would — through the
real interface, port, or browser, on the integrated head — and to report
PASS/FAIL per requirement with the exact command or steps used. Prefer
requirements that are IN PROGRESS or newly integrated over re-verifying ones
already demonstrated.
```

## 2. Role prompts

### 2a. `team-backend-dev-override.md` — Step 4: Test, after "Exercise the change at the real boundary too …"

```
- If the task's acceptance criteria describe what an external client observes,
  prove it that way before signalling completion: start the system with the
  project's own start command and drive it with a real client (a socket,
  HTTP, or protocol client from outside the process), then paste the command
  and its output in your completion note. A unit test or an in-process call
  is not that proof. If you cannot do it, the task is not done — go to Step 6
  and say exactly what is blocking.
```

### 2b. `team-frontend-dev-override.md` — Step 4: Verify It Renders, after "Walk each state the design specifies …"

```
- For any change to what a signed-in user sees: the main thing the product is
  for must be reachable with visible controls — get to the primary view,
  create or post the primary item, reload, see it rendered, open its detail
  view and its quick actions — and you must perform that walk in the browser
  and list each step with what you saw in your completion note. A header and
  an empty pane is not done. Never remove shipped UI to make your change fit;
  extend it.
```

### 2c. `team-qa-override.md` — Step 2b, after "Copy the design's acceptance criteria …"

```
- Read the epic's `REQUIREMENTS:` list. Your first checklist item is the
  requirement your task names, exercised as the external user or client
  would on the integrated head — real interface, port, or browser. Report it
  PASS/FAIL with the command or steps and the output. A passing local suite
  is not a result for a requirement. If the project's own test command skips
  or excludes tests that exercise a requirement, report those as FAIL.
```

### 2d. `team-architect-override.md` — Your Lane, after "Your output is a design saved on the task …"

```
- Designs are thin: about 4,000 characters at most unless the task asks for a
  data model or a protocol. Give the interface contract, the files, the seams,
  and one runnable probe command for the implementer; do not restate the epic
  or the codebase.
- If the task already has a design, do not redesign unless the lead's FEEDBACK
  names a concrete defect; otherwise hand it back unchanged with a note.
- A priority-0 task is on the critical path: finish its design in one
  session, or note exactly what is missing and hand it back.
```

## 3. Harness changes (not prompts; separately confirmed)

- Scheduler eligibility invariant: a `frontend`/`backend` task is never
  fallback-claimed by a design-only role; fallback claims never cross lanes.
- Capture the headless lead's per-turn stdout to `/logs/agent/lead-turns/`.
- Per-worker port/data isolation (fixed ports reserved for the grader only).
- (Still leak-sensitive, decide separately) a serialized "project's own
  start + external smoke" run after each requirement-task integration; it must
  not be the grader's suite.
