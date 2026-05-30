import { useEffect, useMemo, useState } from "react";

import {
  isWorkflowRunLive,
  useCancelWorkflowRun,
  useTaskWorkflowRuns,
  useWorkflowRunEvents,
  type WorkflowRun,
  type WorkflowRunEvent,
  type WorkflowRunListItem,
  type WorkflowRunStatus,
} from "@/hooks/workflows";

import styles from "./WorkflowRunHistory.module.css";

export interface WorkflowRunHistoryProps {
  taskId: string;
}

export function WorkflowRunHistory({
  taskId,
}: WorkflowRunHistoryProps): JSX.Element | null {
  const { runs, isLoading, error, refetch } = useTaskWorkflowRuns(taskId);
  const cancelWorkflowRun = useCancelWorkflowRun();
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [cancelingRunId, setCancelingRunId] = useState<string | null>(null);

  const selectedItem = useMemo(
    () => runs.find((item) => item.run.run_id === selectedRunId) ?? null,
    [runs, selectedRunId],
  );
  const selectedRun = selectedItem?.run ?? null;
  const selectedIsLive = selectedRun
    ? isWorkflowRunLive(selectedRun.status)
    : false;
  const {
    events,
    isLoading: eventsLoading,
    error: eventsError,
    refetch: refetchEvents,
  } = useWorkflowRunEvents(selectedRunId, selectedIsLive);

  useEffect(() => {
    if (runs.length === 0) {
      setSelectedRunId(null);
      return;
    }
    const firstRun = runs[0]?.run;
    if (
      firstRun &&
      (!selectedRunId ||
        !runs.some((item) => item.run.run_id === selectedRunId))
    ) {
      setSelectedRunId(firstRun.run_id);
    }
  }, [runs, selectedRunId]);

  if (isLoading && runs.length === 0) {
    return (
      <section
        className={styles.workflowRuns}
        data-testid="workflow-run-history"
      >
        <div className={styles.workflowHeader}>
          <span className={styles.workflowTitle}>Workflow runs</span>
          <span className={styles.workflowMeta}>Loading</span>
        </div>
      </section>
    );
  }

  if (error && runs.length === 0) {
    return (
      <section
        className={styles.workflowRuns}
        data-testid="workflow-run-history"
      >
        <div className={styles.workflowHeader}>
          <span className={styles.workflowTitle}>Workflow runs</span>
          <span className={styles.workflowError}>{error.message}</span>
        </div>
      </section>
    );
  }

  if (runs.length === 0) return null;

  const handleCancel = async (run: WorkflowRun) => {
    setCancelError(null);
    setCancelingRunId(run.run_id);
    try {
      await cancelWorkflowRun(run.run_id);
      refetch();
      refetchEvents();
    } catch (err) {
      setCancelError(err instanceof Error ? err.message : String(err));
    } finally {
      setCancelingRunId(null);
    }
  };

  return (
    <section className={styles.workflowRuns} data-testid="workflow-run-history">
      <div className={styles.workflowHeader}>
        <span className={styles.workflowTitle}>Workflow runs</span>
        <span className={styles.workflowMeta}>
          {runs.length} {runs.length === 1 ? "run" : "runs"}
        </span>
        {error && <span className={styles.workflowError}>{error.message}</span>}
        {cancelError && (
          <span className={styles.workflowError}>{cancelError}</span>
        )}
      </div>
      <div className={styles.workflowGrid}>
        <div className={styles.workflowRunList}>
          {runs.map((item) => (
            <WorkflowRunRow
              key={item.run.run_id}
              item={item}
              isSelected={item.run.run_id === selectedRunId}
              onSelect={() => setSelectedRunId(item.run.run_id)}
            />
          ))}
        </div>
        <div className={styles.workflowEventPanel}>
          {selectedRun ? (
            <>
              <div className={styles.workflowDetailHeader}>
                <div className={styles.workflowDetailTitle}>
                  <span
                    className={styles.statusDot}
                    data-status={selectedRun.status}
                  />
                  <span>{selectedRun.workflow_name}</span>
                  <span
                    className={styles.workflowStatus}
                    data-status={selectedRun.status}
                  >
                    {formatStatus(selectedRun.status)}
                  </span>
                </div>
                {selectedIsLive && (
                  <button
                    type="button"
                    className={styles.workflowCancelButton}
                    onClick={() => void handleCancel(selectedRun)}
                    disabled={cancelingRunId === selectedRun.run_id}
                  >
                    {cancelingRunId === selectedRun.run_id
                      ? "Canceling"
                      : "Cancel"}
                  </button>
                )}
              </div>
              <div className={styles.workflowDetailMeta}>
                <span>{shortRunID(selectedRun.run_id)}</span>
                <span>{formatTimestamp(selectedRun.created_at)}</span>
                {selectedItem?.task_runs?.length ? (
                  <span>
                    {selectedItem.task_runs.length}{" "}
                    {selectedItem.task_runs.length === 1
                      ? "task run"
                      : "task runs"}
                  </span>
                ) : null}
              </div>
              <WorkflowEventList
                events={events}
                isLoading={eventsLoading}
                error={eventsError}
              />
            </>
          ) : (
            <div className={styles.workflowEventEmpty}>
              Select a workflow run
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function WorkflowRunRow({
  item,
  isSelected,
  onSelect,
}: {
  item: WorkflowRunListItem;
  isSelected: boolean;
  onSelect: () => void;
}): JSX.Element {
  const run = item.run;
  return (
    <button
      type="button"
      className={`${styles.workflowRunRow} ${
        isSelected ? styles.selectedWorkflowRunRow : ""
      }`}
      onClick={onSelect}
      aria-label={`${run.workflow_name} workflow run, ${formatStatus(run.status)}`}
      data-testid={`workflow-run-row-${run.run_id}`}
    >
      <span className={styles.statusDot} data-status={run.status} />
      <span className={styles.workflowRunRowMain}>
        <span className={styles.workflowRunRowTop}>
          <span className={styles.workflowRunName}>{run.workflow_name}</span>
          <span className={styles.workflowStatus} data-status={run.status}>
            {formatStatus(run.status)}
          </span>
        </span>
        <span className={styles.workflowRunRowBottom}>
          <span>{shortRunID(run.run_id)}</span>
          <span>{formatTimestamp(run.created_at)}</span>
        </span>
      </span>
    </button>
  );
}

function WorkflowEventList({
  events,
  isLoading,
  error,
}: {
  events: WorkflowRunEvent[];
  isLoading: boolean;
  error: Error | null;
}): JSX.Element {
  if (isLoading && events.length === 0) {
    return <div className={styles.workflowEventEmpty}>Loading events</div>;
  }
  if (error && events.length === 0) {
    return <div className={styles.workflowError}>{error.message}</div>;
  }
  if (events.length === 0) {
    return <div className={styles.workflowEventEmpty}>No events recorded</div>;
  }
  return (
    <div className={styles.workflowEventList}>
      {events.map((event) => (
        <div
          key={event.event_id}
          className={styles.workflowEventRow}
          data-testid={`workflow-event-${event.type}`}
        >
          <div className={styles.workflowEventTop}>
            <span className={styles.workflowEventType}>{event.type}</span>
            <span className={styles.workflowEventTime}>
              {formatTimestamp(event.created_at)}
            </span>
          </div>
          {event.message && (
            <div className={styles.workflowEventMessage}>{event.message}</div>
          )}
          {event.data != null && (
            <pre className={styles.workflowEventData}>
              {formatEventData(event.data)}
            </pre>
          )}
        </div>
      ))}
    </div>
  );
}

function formatStatus(status: WorkflowRunStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function shortRunID(runId: string): string {
  if (runId.length <= 12) return runId;
  return runId.slice(0, 12);
}

function formatTimestamp(ts: string | undefined): string {
  if (!ts) return "--";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return ts;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatEventData(data: unknown): string {
  if (typeof data === "string") return data;
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}
