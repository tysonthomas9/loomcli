import { useEffect, useMemo, useState } from "react";

import { SessionRunDetail } from "@/components/SessionRunDetail";
import { useAgentHistory } from "@/hooks/agents";
import { useTaskSessions } from "@/hooks/terminal";
import type { AgentHistorySession } from "@/api/agents";
import type { SessionRecord } from "@/types/agent";

import { TaskLink } from "./TaskLink";
import styles from "./WorkflowAgentDetail.module.css";

const ACTIVE_SESSION_STATUSES = new Set([
  "queued",
  "leased",
  "starting",
  "running",
  "idle",
  "yielded",
]);

function usableTime(value: string | null | undefined): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return null;
  return value;
}

function sessionStartedAt(session: AgentHistorySession): string {
  return usableTime(session.started_at) ?? session.created_at;
}

function displayTime(value: string | null | undefined): string {
  const usable = usableTime(value);
  if (!usable) return "—";
  return new Date(usable).toLocaleString();
}

function sessionStatus(session: AgentHistorySession): SessionRecord["status"] {
  switch (session.status) {
    case "completed":
      return "completed";
    case "failed":
    case "expired":
      return "failed";
    case "cancelled":
      return "aborted";
    default:
      return "running";
  }
}

function sessionDotColor(session: AgentHistorySession): string {
  switch (sessionStatus(session)) {
    case "completed":
      return "var(--color-success, #3aa76d)";
    case "failed":
    case "aborted":
      return "var(--color-danger, #d14545)";
    default:
      return "var(--color-warning, #d99700)";
  }
}

function fallbackSession(session: AgentHistorySession): SessionRecord {
  const status = sessionStatus(session);
  const startedAt = sessionStartedAt(session);
  const endedAt = usableTime(session.finished_at);
  const started = new Date(startedAt).getTime();
  const ended = endedAt ? new Date(endedAt).getTime() : Number.NaN;
  const duration =
    Number.isFinite(started) && Number.isFinite(ended)
      ? Math.max(0, (ended - started) / 1000)
      : 0;
  const metadata = session.metadata ?? {};
  const isFailure = status === "failed" || status === "aborted";
  const lastError = isFailure
    ? session.summary || session.error_class
    : undefined;
  return {
    session_id: session.session_id,
    task_id: session.task_id ?? "",
    agent_name: session.agent_id,
    backend: metadata.backend || metadata.runtime_strategy || "agent",
    started_at: startedAt,
    ended_at: endedAt,
    duration_s: duration,
    status,
    // SessionRecord's generated presentation shape requires numeric telemetry.
    // Callers mark these placeholders unknown until canonical evidence arrives.
    exit_code: session.exit_code ?? 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: [],
    attempt_num: session.attempt ?? 0,
    is_active: ACTIVE_SESSION_STATUSES.has(session.status),
    // Every durable agent session has a generic transcript endpoint. Task
    // sessions may also resolve through their existing task-owned route.
    has_transcript: true,
    has_diff: false,
    ...(session.phase ? { phase: session.phase } : {}),
    ...(session.error_class ? { error_class: session.error_class } : {}),
    ...(lastError ? { last_error: lastError } : {}),
    ...(metadata.runtime_strategy
      ? { runtime_strategy: metadata.runtime_strategy }
      : {}),
    ...(metadata.delivery ? { delivery: metadata.delivery } : {}),
    ...(metadata.patch_back_status
      ? { patch_back_status: metadata.patch_back_status }
      : {}),
    ...(metadata.local_branch ? { local_branch: metadata.local_branch } : {}),
    ...(metadata.head_sha ||
    metadata.patch_back_head_sha ||
    metadata.github_head_sha
      ? {
          head_sha:
            metadata.head_sha ||
            metadata.patch_back_head_sha ||
            metadata.github_head_sha,
        }
      : {}),
    ...(metadata.github_branch
      ? { github_branch: metadata.github_branch }
      : {}),
    ...(metadata.github_pr_url
      ? { github_pr_url: metadata.github_pr_url }
      : {}),
  };
}

function AgentSessionDetail({
  historySession,
}: {
  historySession: AgentHistorySession;
}): JSX.Element {
  const taskId = historySession.task_id ?? null;
  const { sessions, isLoading, error } = useTaskSessions(taskId);
  const canonical = useMemo(
    () =>
      sessions.find(
        (session) => session.session_id === historySession.session_id,
      ) ?? null,
    [historySession.session_id, sessions],
  );

  if (!taskId) {
    return (
      <SessionRunDetail
        taskId=""
        agentId={historySession.agent_id}
        session={fallbackSession(historySession)}
        retryTranscriptUnavailable
        exitCodeKnown={historySession.exit_code != null}
        telemetryKnown={false}
      />
    );
  }
  if (error && !canonical) {
    return (
      <div className={styles.transcriptEmpty}>
        Failed to load task-session evidence: {error.message}
      </div>
    );
  }
  if (isLoading && !canonical) {
    return <div className={styles.transcriptEmpty}>Loading transcript…</div>;
  }
  return (
    <SessionRunDetail
      taskId={taskId}
      session={canonical ?? fallbackSession(historySession)}
      retryTranscriptUnavailable={canonical == null}
      exitCodeKnown={canonical != null || historySession.exit_code != null}
      telemetryKnown={canonical != null}
    />
  );
}

/** Run-history tab for daemon-supervised and interactive agent assignments. */
export function AgentSessionRunsPane({
  workspaceId,
  agentName,
  active = true,
  onOpenTask,
}: {
  workspaceId: string;
  agentName: string;
  active?: boolean;
  onOpenTask?: ((taskId: string) => void) | undefined;
}): JSX.Element {
  const { sessions, isLoading, error } = useAgentHistory(
    workspaceId,
    agentName,
    active,
  );
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(
    null,
  );
  useEffect(() => setSelectedSessionId(null), [agentName, workspaceId]);
  const selected =
    sessions.find((session) => session.session_id === selectedSessionId) ??
    sessions[0] ??
    null;

  return (
    <div className={styles.scroll}>
      {selected ? (
        <section
          className={styles.card}
          data-testid="supervised-agent-session-detail"
        >
          <div className={styles.runDetailHead}>
            <span
              className={styles.runDot}
              style={{ background: sessionDotColor(selected) }}
              data-live={
                ACTIVE_SESSION_STATUSES.has(selected.status) || undefined
              }
              aria-hidden="true"
            />
            <span className={styles.runDetailStatus}>{selected.status}</span>
            <code className={styles.runId}>{selected.session_id}</code>
          </div>
          <dl className={styles.detailGrid}>
            <div>
              <dt>Task</dt>
              <dd>
                {selected.task_id ? (
                  <TaskLink
                    workspaceId={workspaceId}
                    taskId={selected.task_id}
                    className={styles.taskLink}
                    onOpenTask={onOpenTask}
                  />
                ) : (
                  "—"
                )}
              </dd>
            </div>
            <div>
              <dt>Started</dt>
              <dd>{displayTime(sessionStartedAt(selected))}</dd>
            </div>
            <div>
              <dt>Finished</dt>
              <dd>{displayTime(selected.finished_at)}</dd>
            </div>
            <div>
              <dt>Kind</dt>
              <dd>{selected.kind}</dd>
            </div>
          </dl>
          {selected.summary ? (
            <p className={styles.detailSummary}>{selected.summary}</p>
          ) : null}
          <div className={styles.runTranscript}>
            <AgentSessionDetail historySession={selected} />
          </div>
        </section>
      ) : null}

      <section className={styles.card}>
        <h2 className={styles.cardLabel}>Run history</h2>
        {error ? (
          <div className={styles.errorText} role="alert">
            {error.message}
          </div>
        ) : isLoading && sessions.length === 0 ? (
          <div className={styles.emptyText}>Loading runs…</div>
        ) : sessions.length === 0 ? (
          <div
            className={styles.emptyText}
            data-testid="supervised-agent-no-runs"
          >
            No sessions yet. This agent will appear here after it starts work.
          </div>
        ) : (
          <ul
            className={styles.runList}
            data-testid="supervised-agent-session-list"
          >
            {sessions.map((session) => (
              <li
                key={session.session_id}
                className={styles.runRow}
                data-selected={
                  session.session_id === selected?.session_id || undefined
                }
              >
                <button
                  type="button"
                  className={styles.runRowSelect}
                  data-testid={`supervised-agent-session-${session.session_id}`}
                  onClick={() => setSelectedSessionId(session.session_id)}
                >
                  <span
                    className={styles.runDot}
                    style={{ background: sessionDotColor(session) }}
                    data-live={
                      ACTIVE_SESSION_STATUSES.has(session.status) || undefined
                    }
                    aria-hidden="true"
                  />
                  <span className={styles.runMain}>
                    <span className={styles.runStatus}>{session.status}</span>
                    <span className={styles.runMeta}>
                      {session.task_id ? "" : `${session.kind} · `}
                      {displayTime(sessionStartedAt(session))}
                    </span>
                    {session.summary ? (
                      <span className={styles.runSummary}>
                        {session.summary}
                      </span>
                    ) : null}
                    {session.error_class ? (
                      <span className={styles.runErr}>
                        {session.error_class}
                      </span>
                    ) : null}
                  </span>
                </button>
                {session.task_id ? (
                  <span className={styles.runTaskLinks} aria-label="Run task">
                    <span className={styles.runTaskLabel}>Task</span>
                    <TaskLink
                      workspaceId={workspaceId}
                      taskId={session.task_id}
                      className={styles.taskLink}
                      onOpenTask={onOpenTask}
                    />
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
