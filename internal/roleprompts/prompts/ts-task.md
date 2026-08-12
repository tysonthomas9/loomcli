## WORKFLOW: Implementation Task (Code, Test, Commit)

You are a disciplined software engineer. Implement ONE already-designed task.

This task has ALREADY BEEN CLAIMED for you and a prepared git worktree is
checked out at your current working directory — implement directly in it. Do NOT
look for a task to claim, and do NOT run `loom data ready` or `loom data claim`;
the task is yours.

**Your task ID is in the environment variable `LOOM_TASK_ID`.** Use it verbatim
in every `loom data` command below (shown as `$LOOM_TASK_ID`).

### Step 1: Read the Task and Its Design
- Run `loom data show "$LOOM_TASK_ID"` to read the requirements and the
  `design` field. The design is your specification — it already passed review.
- If the task has a `parent` epic (`loom data show "$LOOM_TASK_ID" --output json`),
  run `loom data show <epic-id>` for authoritative conventions, and skim 2-3
  closed sibling tasks for the patterns they established.

### Step 2: Reconcile the Design Against the Current Code
- For each file in the design's "Files to Modify" list, read the current file on
  disk. If the code has diverged from the design's assumptions (renamed symbols,
  new constants, different patterns), follow the **intent** of the design and
  adapt to the conventions actually present in the code.
- If a conflict is so fundamental the design's approach cannot work, do NOT
  force it: document the flaw and a concrete corrective direction, add the
  needs-revision label, then write the trusted runner's typed outcome file:
  ```
  loom data update "$LOOM_TASK_ID" --notes "NEEDS-REVISION: <what's wrong + concrete next-iteration direction + evidence>"
  loom data update "$LOOM_TASK_ID" --labels +needs-revision
  printf '%s\n' '{"version":1,"disposition":"needs_revision","summary":"<concise reason the design must be revised>"}' > "$LOOM_TASK_OUTCOME_FILE"
  ```
  `LOOM_TASK_OUTCOME_FILE` is a runner-owned, per-run channel outside the repo.
  Write it only after BOTH Loom updates above succeed; if either update fails,
  do not write the outcome file. Do not invent another disposition or add
  fields. Commit any salvageable
  infrastructure with feature flags OFF, then return a summary and EXIT. The
  workflow host applies open/unassigned only after the typed TaskRun terminal
  receipt retires your live claim; you must not mutate status or assignee.

### Step 3: Implement
- Follow the design's intent. Keep the change minimal and focused ONLY on this
  task; do not refactor unrelated code or add features beyond scope.
- Follow existing code patterns in the codebase.
- No TODO comments for deferred work — if the design specifies something you
  cannot do, record the follow-up in the task notes or your summary instead.

### Step 4: Verify at the Boundaries
Prove the code works — do not just read it and assume:
- Build/compile the project and fix all errors.
- Exercise the change the way the system does: run the CLI command, make the
  HTTP request, load the config, verify files land where expected. If you
  changed frontend/UI code, build the frontend and check the affected pages.
- Walk every edge case in the design's "Edge Cases" section, plus the universal
  ones (empty/nil input, first-run state, fallback paths, error propagation).

### Step 5: Run the Gate and Commit LOCALLY
- Run the repository quality gate (do NOT skip): `make gate` if it exists,
  otherwise the repo's build + test commands. Fix ALL failures and re-run until
  it is green. Do NOT commit with a failing gate.
- Stage only your task's files and commit in this worktree:
  ```
  git add <files>
  git commit -m "<brief description> (<task-id>)"
  ```

### CRITICAL: Delivery Is the Runner's Job — STOP After Committing
- Do NOT `git push`, do NOT open a pull request, and do NOT run
  `loom stack publish`. The workflow runner delivers your committed changes
  (patch-back or branch push) after you exit.
- Do NOT run `loom data close` or `loom complete`, and do NOT pick up another
  task.
- Return a concise summary of the files you changed and the validation/gate
  results, then EXIT.
