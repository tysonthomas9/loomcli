# Agent-observability session fixtures (LOOMCLI-54)

Real session transcripts captured from the local-mode **codex** stack
(`make local-mode-codex-up`, workspace `LOCALMODE`, real Codex CLI backend)
on 2026-07-16, for judge-rubric and transcript-condensation prototyping
(wayfinder map LOOMCLI-52; tickets LOOMCLI-61 / LOOMCLI-62).

Nothing here is fabricated: every session was produced by the real daemon /
supervisor / Codex CLI workflow. Tasks LOCALMODE-7/8/9/10 were created through
the loom API the same way the stack's seeder creates its dogfood tasks.

## Layout

- `sessions/<session-id>/` — verbatim copy of the on-disk session dir from the
  loom container (`/root/.loom/workspaces/LOCALMODE/sessions/<id>/`):
  - `agent_transcript.jsonl` — **raw** transcript (codex-native event JSONL:
    `session_meta`, `event_msg`, `response_item`, `world_state`, `turn_context`).
  - `metadata.json` — SessionRecord (`transcript_format: "raw"` on every one).
  - `prompt.txt` — the composed workflow prompt handed to the backend.
  - `diff.patch` — present only when the session produced a diff.
  - `canonical_transcript.json` — same session served by
    `GET /api/workspaces/LOCALMODE/tasks/<task>/sessions/<id>/transcript`;
    the server converts raw → **canonical** entries
    (`{seq, timestamp, role, type, text…}` with roles system/user/assistant/tool)
    at read time. Only task-attached sessions have this file — the transcript
    API route is task-scoped, so sessions without a `task_id` have **no API
    transcript surface today**.
- `fleetdb-agent-sessions.json` — the fleet-db `agent-sessions` records
  (GET `/api/v1/LOCALMODE/agent-sessions`) at capture time.

## Sessions

| session | kind | status | task | raw bytes/events | canonical bytes/entries | notes |
|---|---|---|---|---|---|---|
| 20260716-193040-codex-coder--2d920713 | task | completed | LOCALMODE-3 | 100,294 / 97 | 40,976 / 49 | happy-path coder run, 1-file diff |
| 20260716-193040-codex-planner--3660c4d1 | task | completed | LOCALMODE-2 | 107,621 / 73 | 61,189 / 38 | planner run (design written) |
| 20260716-193345-codex-planner--3d4537ef | task | completed | LOCALMODE-4 | 183,211 / 101 | 126,790 / 51 | largest planner run |
| 20260716-193728-codex-planner--5541b5e0 | task | completed | LOCALMODE-5 | 159,534 / 77 | 112,257 / 38 | planner run |
| 20260716-200552-codex-coder--6832dd35 | task | stuck `running` | (none) | — (no transcript) | — | orphaned: metadata+prompt only, never finalized, **not in fleet-db** |
| 20260716-200554-codex-coder--9152880d | task | completed | (none) | 67,194 / 26 | — (no task ⇒ no API route) | no-task run, **not in fleet-db** |
| 20260716-230149-fixture-child-coder--81dd921c | task | **failed** | LOCALMODE-10 | 80,755 / 79 | 28,741 / — | genuine supervisor watchdog no-progress kill (`silent_duration=15m26s threshold_sec=900`, exit −1, `error_class: Unknown`); transcript survives, ends mid-run; also `parent_session_id` → lead |
| 20260716-225541-codex-coder--9748ccfa | task | completed | LOCALMODE-7 | 137,145 / — | 51,738 / — | completed but pathological: agent obediently spent 15 min in a foreground `sleep 900` — good low-efficiency rubric case |
| 20260716-225956-fixture-child-coder--ed284335 | task | completed | LOCALMODE-9 | 113,400 / — | 54,316 / — | **subagent**: `parent_session_id` = lead orchestration session (spawned via `loom agentdef add --task --orchestrator`) |
| lead-df02e7c2-a04a-4f87-83fe-909078805382 | orchestration | running | — | — | — | PR-222 reviewer lead; terminal-backed, **no transcript exists on this path** |

## Findings that shape the eval/traces design

1. **`transcript_ref` is never stamped on the local-daemon path.** Session
   metadata carries `transcript_path` (a node-local container path) +
   `log_path`; `transcript_ref` (artifact URI) is only stamped by the
   driver/TaskRun path (`internal/driver/task_bridge_artifacts.go`). None of
   the captured fleet-db records have it. (Evidence for LOOMCLI-60.)
2. **Raw vs canonical:** on-disk transcripts are codex-native (`raw`);
   the canonical form exists only as an API-time conversion. Canonical is
   ~45–70% of raw bytes, ~half the entry count.
3. **Sessions without a task have no transcript API surface** (route is
   `/tasks/{taskId}/sessions/{sid}/transcript`) and do not register in
   fleet-db agent-sessions — invisible to a fleet-db-driven eval batch or
   Traces list even when a transcript file exists on disk.
4. **Orchestration sessions are terminal-backed and never produce their own
   transcript**; subagent linkage is `AgentSession.parent_session_id`
   (stamped when a child is spawned with `--orchestrator` / from a lead).
5. **Local-mode cannot run Flue workflows** (`epic run` fails: no `@loom/sdk`,
   no flue runtime in the image) — the planned eval agent (a builtin Flue
   workflow) has no dogfood surface in local-mode today.
6. **Watchdog kills keep their transcript but lose their taxonomy:** the
   no-progress kill finalizes the session with `exit_code: -1` and
   `error_class: "Unknown"` — the supervisor's own kill accounting
   (`kill=watchdog/Unknown`, quarantine counter 1/3) never reaches the
   session record. A `sleep`-killed transcript just ends mid-run with
   `token_count` events; nothing in transcript or record says "watchdog".
   Failed-session retry creates a **new session id** for the same task
   (attempt 2 captured in fleet-db as 20260716-231744-…), `attempt_num`
   stays 0 on both.
