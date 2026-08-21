# agentflow — mechanical tool reference

([O]-classified: mechanics only, no strategy. Source-verified against
agentflow-go main @ 92ed64c.)

## CLI

- `agentflow validate <pipeline.yaml>` — schema + DAG validation.
- `agentflow run <pipeline.yaml> --work-dir=<dir> [--pipeline-root=<dir>]
  [--resume] [--dry-run]` — execute. `--dry-run` prints the plan without
  running. `--work-dir` is the cwd every step runs in.

## Pipeline YAML schema

```yaml
name: <string>
description: <string, optional>
shared_context_file: <path, optional>   # injected into `type: prompt` steps only
steps:
  - id: <string>
    type: shell | prompt
    command: <string>        # shell steps: run via `sh -c` in the work dir
    prompt: <path>           # prompt steps: file relative to --pipeline-root
    after: [<step-id>, ...]  # DAG edges
    when: <passed|failed|always, optional>
    goal_gate: <bool>        # failing blocks dependents
    retry: <int>             # per-step retries
    retry_check: <string>    # shell command deciding retry success
    output: <path, optional>
    executor: <name, optional>
eval_loop:                   # optional outer cycle, runs after the main pass
  gate: <step-id>            # gate step (skipped steps in fix_steps run only here)
  fix_steps: [<step-id>, ...]
  max_passes: <int>
```

## Step semantics

- **`type: shell`**: `command` runs via `sh -c` with cwd = the work dir. The
  step passes iff exit code 0; a literal `EXIT_CODE=<n>` in the step's output
  overrides the exit code. Output is captured to
  `step-<id>-output.txt` in the run output directory.
- To delegate a step's work to a fresh coding-agent session, use a shell step:

  ```yaml
  - id: build_x
    type: shell
    command: codex exec --json --dangerously-bypass-approvals-and-sandbox --model gpt-5.5 -- "$(cat briefs/build_x.md)"
  ```

  You write the brief files yourself (any path; `$(cat ...)` resolves relative
  to the work dir). Each such step is a fresh session with no memory of other
  steps — put everything the step needs in its brief and/or files on disk.
- **`type: prompt`** composes `prompt`-file + assembled context for the
  claude-cli executor. Do NOT combine `type: prompt` with codex.
- Steps listed in `eval_loop.fix_steps` are SKIPPED during the main pass and
  run only when the gate step fails, up to `max_passes` re-evaluations.
- `--resume` skips steps recorded as completed in the run checkpoint. After
  EDITING the yaml, prefer a fresh `agentflow run` (the checkpoint predates
  the edit); use `when:` conditions and `eval_loop` for iteration instead.
