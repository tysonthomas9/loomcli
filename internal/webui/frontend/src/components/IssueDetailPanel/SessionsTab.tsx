/**
 * SessionsTab - Container component for the Sessions tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useState } from "react";

import { useTaskSessions } from "@/hooks/useTaskSessions";

import { SessionTimeline } from "./SessionTimeline";
import { SessionDetailView } from "./SessionDetailView";
import styles from "./SessionsTab.module.css";

export interface SessionsTabProps {
  taskId: string;
}

export function SessionsTab({ taskId }: SessionsTabProps): JSX.Element {
  const { sessions, isLoading, error } = useTaskSessions(taskId);
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(
    null,
  );

  const selectedSession =
    selectedSessionId != null
      ? (sessions.find((s) => s.id === selectedSessionId) ?? null)
      : null;

  // Loading state with no data yet
  if (isLoading && sessions.length === 0) {
    return (
      <div className={styles.loadingContainer}>
        <div className={styles.spinner} />
      </div>
    );
  }

  // Error state
  if (error && sessions.length === 0) {
    return (
      <div className={styles.emptyState}>
        Failed to load sessions: {error.message}
      </div>
    );
  }

  // Empty state
  if (!isLoading && sessions.length === 0) {
    return (
      <div className={styles.emptyState} data-testid="sessions-empty">
        No sessions recorded yet
      </div>
    );
  }

  return (
    <div className={styles.container} data-testid="sessions-tab">
      <SessionTimeline
        sessions={sessions}
        selectedId={selectedSessionId}
        onSelect={setSelectedSessionId}
        isLoading={isLoading}
      />
      {selectedSession ? (
        <SessionDetailView taskId={taskId} session={selectedSession} />
      ) : (
        <div className={styles.detailEmpty}>
          Select a session to view details
        </div>
      )}
    </div>
  );
}
