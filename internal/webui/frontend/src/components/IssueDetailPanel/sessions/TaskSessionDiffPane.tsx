/**
 * TaskSessionDiffPane — full-width diff from the latest task run session.
 * Used when an agent worktree has no merge-base diff (e.g. reviewer assigned
 * after the ephemeral worker finished).
 */

import { useMemo } from "react";

import { useSessionDiff, useTaskSessions } from "@/hooks/terminal";

import styles from "./SessionsTab.module.css";
import { SessionDiffViewer } from "./SessionDiffViewer";

export interface TaskSessionDiffPaneProps {
  taskId: string;
  /** Shown in empty states when the worktree diff was also empty. */
  worktreeAgentName?: string | undefined;
}

export function TaskSessionDiffPane({
  taskId,
  worktreeAgentName,
}: TaskSessionDiffPaneProps): JSX.Element {
  const {
    sessions,
    isLoading: sessionsLoading,
    error: sessionsError,
  } = useTaskSessions(taskId);

  const session = useMemo(
    () => sessions.find((s) => s.has_diff) ?? sessions[0],
    [sessions],
  );

  const {
    diff,
    isLoading: diffLoading,
    error: diffError,
  } = useSessionDiff(
    taskId,
    session?.session_id ?? null,
    Boolean(session?.has_diff),
  );

  if (sessionsLoading || (session?.has_diff && diffLoading)) {
    return (
      <div className={styles.diffContainer} data-testid="task-session-diff">
        <div className={styles.emptyState}>Loading diff…</div>
      </div>
    );
  }

  if (sessionsError) {
    return (
      <div className={styles.diffContainer} data-testid="task-session-diff">
        <div className={styles.errorText}>
          Failed to load runs: {sessionsError.message}
        </div>
      </div>
    );
  }

  if (diffError) {
    return (
      <div className={styles.diffContainer} data-testid="task-session-diff">
        <div className={styles.errorText}>
          Failed to load diff: {diffError.message}
        </div>
      </div>
    );
  }

  if (diff) {
    return (
      <div className={styles.diffContainer} data-testid="task-session-diff">
        {session ? (
          <div className={styles.diffMeta}>
            Run diff from {session.agent_name}
            {session.backend ? ` · ${session.backend}` : ""}
          </div>
        ) : null}
        <SessionDiffViewer diff={diff} />
      </div>
    );
  }

  return (
    <div className={styles.diffContainer} data-testid="task-session-diff">
      <div className={styles.diffEmpty}>
        {worktreeAgentName
          ? `No changes on ${worktreeAgentName}'s branch and no saved run diff for this task yet.`
          : "No saved run diff for this task yet."}
      </div>
    </div>
  );
}
