import { useEffect, useMemo, useState } from "react";

import type {
  DriverRunDTO,
  TaskRunDTO,
  TaskRunStatus,
} from "@/api/agentServices";
import { useDriverRunLog, useTaskRunLog } from "@/hooks/workspace";
import { ApiError } from "@/types/common";
import { formatStatusLabel } from "@/utils/issue";
import { parseTranscript } from "@/utils/transcript";

import styles from "./AgentServiceDetail.module.css";
import { TranscriptView } from "./TranscriptView";

interface TaskLogsSectionProps {
  workspaceId: string;
  tasks: TaskRunDTO[];
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  liveRefreshTick: number;
}

function isLiveTask(status: TaskRunStatus): boolean {
  return status === "queued" || status === "running";
}

function duration(startedAt?: string, finishedAt?: string | null): string {
  if (!startedAt) return "Unknown";
  const start = new Date(startedAt).getTime();
  const finish = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  if (Number.isNaN(start) || Number.isNaN(finish) || finish < start) {
    return "Unknown";
  }
  const seconds = Math.floor((finish - start) / 1000);
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return minutes > 0 ? minutes + "m " + remainder + "s" : seconds + "s";
}

function TaskLogRow({
  workspaceId,
  task,
  liveRefreshTick,
}: {
  workspaceId: string;
  task: TaskRunDTO;
  liveRefreshTick: number;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const [logView, setLogView] = useState<"pretty" | "raw">("pretty");
  // A settled task whose listing already says no log exists skips the fetch
  // entirely; live tasks still fetch, since their log lands at settle time.
  const logCouldExist = task.logsAvailable || isLiveTask(task.status);
  const { log, loading, initialized, error, refresh } = useTaskRunLog(
    workspaceId,
    task.taskRunId,
    { enabled: expanded && logCouldExist },
  );
  const taskDuration = duration(task.startedAt, task.finishedAt);
  const transcript = useMemo(
    () => parseTranscript(log?.content ?? ""),
    [log?.content],
  );

  useEffect(() => {
    if (expanded && liveRefreshTick > 0 && isLiveTask(task.status)) {
      void refresh();
    }
  }, [expanded, liveRefreshTick, refresh, task.status]);

  return (
    <article className={styles.taskLogRow}>
      <button
        type="button"
        className={styles.taskLogToggle}
        aria-expanded={expanded}
        aria-label={
          (task.runner || task.taskRunId) +
          " " +
          formatStatusLabel(task.status) +
          " " +
          taskDuration
        }
        onClick={() => setExpanded((value) => !value)}
      >
        <span className={styles.rowMain}>
          <strong>{task.runner || "Unknown runner"}</strong>
          <span>{task.taskId || task.taskRunId}</span>
        </span>
        <span className={styles.taskLogMeta}>
          <span>{formatStatusLabel(task.status)}</span>
          <span>{taskDuration}</span>
        </span>
        <span className={styles.chevron} aria-hidden="true">
          {expanded ? "▾" : "▸"}
        </span>
      </button>
      {expanded ? (
        <div className={styles.logBody}>
          {!logCouldExist && !log ? (
            <p className={styles.emptyText} data-testid="task-log-empty">
              No AI log is available for this task yet.
            </p>
          ) : loading && !initialized ? (
            <p className={styles.emptyText}>Loading AI log…</p>
          ) : error instanceof ApiError && error.status === 404 ? (
            <p className={styles.emptyText} data-testid="task-log-empty">
              No AI log is available for this task yet.
            </p>
          ) : error ? (
            <p className={styles.errorText} role="alert">
              AI log unavailable: {error.message}
            </p>
          ) : log ? (
            <>
              {log.truncated ? (
                <p className={styles.truncationNotice} role="status">
                  Showing the last 1 MiB of this AI log.
                </p>
              ) : null}
              {transcript.codexEventCount > 0 ? (
                <>
                  <div
                    className={styles.taskLogViewControls}
                    data-testid="task-log-view-toggle"
                    role="group"
                    aria-label="Task log view"
                  >
                    <button
                      type="button"
                      aria-pressed={logView === "pretty"}
                      onClick={() => setLogView("pretty")}
                    >
                      Pretty
                    </button>
                    <button
                      type="button"
                      aria-pressed={logView === "raw"}
                      onClick={() => setLogView("raw")}
                    >
                      Raw
                    </button>
                  </div>
                  {logView === "pretty" ? (
                    <TranscriptView transcript={transcript} />
                  ) : (
                    <pre data-testid={`task-log-content-${task.taskRunId}`}>
                      {log.content}
                    </pre>
                  )}
                </>
              ) : (
                <pre data-testid={`task-log-content-${task.taskRunId}`}>
                  {log.content}
                </pre>
              )}
            </>
          ) : (
            <p className={styles.emptyText}>No AI log content.</p>
          )}
        </div>
      ) : null}
    </article>
  );
}

export function TaskLogsSection({
  workspaceId,
  tasks,
  loading,
  initialized,
  error,
  liveRefreshTick,
}: TaskLogsSectionProps): JSX.Element {
  return (
    <section className={styles.taskLogsSection} data-testid="task-logs-section">
      <h3>Task logs</h3>
      {error ? (
        <p className={styles.errorText} role="alert">
          Task logs unavailable: {error.message}
        </p>
      ) : loading && !initialized ? (
        <p className={styles.emptyText}>Loading task logs…</p>
      ) : tasks.length === 0 ? (
        <p className={styles.emptyText}>No task runs were recorded.</p>
      ) : (
        <div className={styles.taskLogList}>
          {tasks.map((task) => (
            <TaskLogRow
              key={task.taskRunId}
              workspaceId={workspaceId}
              task={task}
              liveRefreshTick={liveRefreshTick}
            />
          ))}
        </div>
      )}
    </section>
  );
}

export function HarnessLog({
  workspaceId,
  run,
}: {
  workspaceId: string;
  run: DriverRunDTO;
}): JSX.Element | null {
  const [expanded, setExpanded] = useState(false);
  const { log, loading, initialized, error } = useDriverRunLog(
    workspaceId,
    run.runId,
    { enabled: expanded },
  );
  const logsRef = run.output?.logs_ref;
  if (!logsRef) return null;
  const absent = error instanceof ApiError && error.status === 404;

  return (
    <section className={styles.harnessLog} data-testid="harness-log">
      <button
        type="button"
        className={styles.logDisclosureButton}
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        Harness log
        <span className={styles.chevron} aria-hidden="true">
          {expanded ? "▾" : "▸"}
        </span>
      </button>
      {!log ? (
        <p className={styles.logsRef}>
          <strong>Logs:</strong> {logsRef}
        </p>
      ) : null}
      {expanded ? (
        <div className={styles.logBody}>
          {loading && !initialized ? (
            <p className={styles.emptyText}>Loading harness log…</p>
          ) : absent ? null : error ? (
            <p className={styles.errorText} role="alert">
              Harness log unavailable: {error.message}
            </p>
          ) : log ? (
            <>
              {log.truncated ? (
                <p className={styles.truncationNotice} role="status">
                  Showing the last 1 MiB of this harness log.
                </p>
              ) : null}
              <pre data-testid="harness-log-content">{log.content}</pre>
            </>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
