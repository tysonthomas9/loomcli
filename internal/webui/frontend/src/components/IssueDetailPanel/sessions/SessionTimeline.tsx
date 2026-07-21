/**
 * SessionTimeline - Vertical list of session rows, sorted newest-first.
 */

import type { SessionRecord } from "@/types/agent";
import type { TaskWorkflowRun } from "@/api/workflows";

import { SessionTimelineRow } from "./SessionTimelineRow";
import { WorkflowRunTimelineRow } from "./WorkflowRunTimelineRow";
import styles from "./SessionsTab.module.css";

export interface SessionTimelineProps {
  sessions: SessionRecord[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  isLoading: boolean;
  workflowRuns?: TaskWorkflowRun[];
  selectedWorkflowRunId?: string | null;
  onSelectWorkflowRun?: (id: string) => void;
}

export function SessionTimeline({
  sessions,
  selectedId,
  onSelect,
  isLoading,
  workflowRuns = [],
  selectedWorkflowRunId = null,
  onSelectWorkflowRun,
}: SessionTimelineProps): JSX.Element {
  if (isLoading && sessions.length === 0 && workflowRuns.length === 0) {
    return (
      <div className={styles.timeline}>
        <div className={styles.timelineSkeleton}>
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
        </div>
      </div>
    );
  }

  const sorted = [
    ...sessions.map((session) => ({
      kind: "session" as const,
      id: session.session_id,
      timestamp: session.started_at,
      session,
    })),
    ...workflowRuns.map((run) => ({
      kind: "workflow" as const,
      id: run.run_id,
      timestamp: run.started_at || run.created_at,
      run,
    })),
  ].sort(
    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime(),
  );

  return (
    <div className={styles.timeline} data-testid="session-timeline">
      {sorted.map((item) =>
        item.kind === "session" ? (
          <SessionTimelineRow
            key={`session:${item.id}`}
            session={item.session}
            isSelected={selectedId === item.id}
            onClick={() => onSelect(item.id)}
          />
        ) : (
          <WorkflowRunTimelineRow
            key={`workflow:${item.id}`}
            run={item.run}
            isSelected={selectedWorkflowRunId === item.id}
            onClick={() => onSelectWorkflowRun?.(item.id)}
          />
        ),
      )}
    </div>
  );
}
