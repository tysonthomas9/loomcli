import { useEffect, useMemo, useState } from "react";

import {
  isWorkflowRunLive,
  useCancelWorkflowRun,
  useStartWorkflowRun,
  useTaskWorkflowRuns,
  useWorkflowDefinitions,
  useWorkflowRunEvents,
  type WorkflowDefinition,
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
  const {
    definitions,
    isLoading: definitionsLoading,
    error: definitionsError,
  } = useWorkflowDefinitions();
  const cancelWorkflowRun = useCancelWorkflowRun();
  const startWorkflowRun = useStartWorkflowRun();
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [selectedWorkflowName, setSelectedWorkflowName] = useState("");
  const [workflowInput, setWorkflowInput] = useState(
    defaultWorkflowInput(taskId),
  );
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [cancelingRunId, setCancelingRunId] = useState<string | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [isStarting, setIsStarting] = useState(false);

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
    streamCompletion,
  } = useWorkflowRunEvents(selectedRunId, selectedIsLive);
  const selectedTerminalRun = useMemo(
    () =>
      streamCompletion?.runs.find((run) => run.run_id === selectedRunId) ??
      null,
    [streamCompletion, selectedRunId],
  );
  const selectedDisplayStatus =
    selectedTerminalRun?.status ?? selectedRun?.status ?? null;
  const selectedDisplayFinishedAt =
    selectedTerminalRun?.finished_at ?? selectedRun?.finished_at ?? null;
  const selectedDisplayIsLive = selectedDisplayStatus
    ? isWorkflowRunLive(selectedDisplayStatus)
    : false;

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

  useEffect(() => {
    setWorkflowInput(defaultWorkflowInput(taskId));
  }, [taskId]);

  useEffect(() => {
    if (definitions.length === 0) {
      setSelectedWorkflowName("");
      return;
    }
    if (
      !selectedWorkflowName ||
      !definitions.some(
        (definition) => definition.name === selectedWorkflowName,
      )
    ) {
      setSelectedWorkflowName(definitions[0]?.name ?? "");
    }
  }, [definitions, selectedWorkflowName]);

  useEffect(() => {
    if (streamCompletion) {
      refetch();
    }
  }, [streamCompletion, refetch]);

  const handleStartWorkflow = async () => {
    if (!selectedWorkflowName) return;
    setStartError(null);
    setIsStarting(true);
    try {
      const input = JSON.parse(workflowInput.trim() || "{}") as unknown;
      const result = await startWorkflowRun(selectedWorkflowName, {
        input,
        once: true,
        wait: false,
      });
      if (result.run?.run_id) setSelectedRunId(result.run.run_id);
      refetch();
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsStarting(false);
    }
  };

  const header = (
    <WorkflowRunHeader
      runs={runs}
      isLoading={isLoading}
      definitions={definitions}
      definitionsLoading={definitionsLoading}
      selectedWorkflowName={selectedWorkflowName}
      workflowInput={workflowInput}
      isStarting={isStarting}
      onSelectWorkflow={setSelectedWorkflowName}
      onInputChange={setWorkflowInput}
      onStart={() => void handleStartWorkflow()}
      errors={[
        error?.message,
        definitionsError?.message,
        cancelError,
        startError,
      ]}
    />
  );

  if (isLoading && runs.length === 0) {
    return (
      <section
        className={styles.workflowRuns}
        data-testid="workflow-run-history"
      >
        {header}
      </section>
    );
  }

  if (error && runs.length === 0) {
    return (
      <section
        className={styles.workflowRuns}
        data-testid="workflow-run-history"
      >
        {header}
      </section>
    );
  }

  if (runs.length === 0) {
    return (
      <section
        className={styles.workflowRuns}
        data-testid="workflow-run-history"
      >
        {header}
        <div className={styles.workflowEventEmpty}>No workflow runs</div>
      </section>
    );
  }

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
      {header}
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
                    data-status={selectedDisplayStatus ?? selectedRun.status}
                  />
                  <span>{selectedRun.workflow_name}</span>
                  <span
                    className={styles.workflowStatus}
                    data-status={selectedDisplayStatus ?? selectedRun.status}
                  >
                    {formatStatus(selectedDisplayStatus ?? selectedRun.status)}
                  </span>
                </div>
                {selectedDisplayIsLive && (
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
                {selectedTerminalRun && selectedDisplayFinishedAt ? (
                  <span>
                    Finished {formatTimestamp(selectedDisplayFinishedAt)}
                  </span>
                ) : null}
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

function WorkflowRunHeader({
  runs,
  isLoading,
  definitions,
  definitionsLoading,
  selectedWorkflowName,
  workflowInput,
  isStarting,
  errors,
  onSelectWorkflow,
  onInputChange,
  onStart,
}: {
  runs: WorkflowRunListItem[];
  isLoading: boolean;
  definitions: WorkflowDefinition[];
  definitionsLoading: boolean;
  selectedWorkflowName: string;
  workflowInput: string;
  isStarting: boolean;
  errors: Array<string | null | undefined>;
  onSelectWorkflow: (name: string) => void;
  onInputChange: (value: string) => void;
  onStart: () => void;
}): JSX.Element {
  const status = isLoading
    ? "Loading"
    : `${runs.length} ${runs.length === 1 ? "run" : "runs"}`;
  const visibleErrors = errors.filter((message): message is string =>
    Boolean(message),
  );
  return (
    <div className={styles.workflowHeader}>
      <span className={styles.workflowTitle}>Workflow runs</span>
      <span className={styles.workflowMeta}>{status}</span>
      <div className={styles.workflowActions}>
        <select
          className={styles.workflowSelect}
          aria-label="Workflow"
          value={selectedWorkflowName}
          onChange={(event) => onSelectWorkflow(event.target.value)}
          disabled={definitions.length === 0 || definitionsLoading}
        >
          {definitions.length === 0 ? (
            <option value="">
              {definitionsLoading ? "Loading workflows" : "No workflows"}
            </option>
          ) : (
            definitions.map((definition) => (
              <option key={definition.name} value={definition.name}>
                {definition.name}
              </option>
            ))
          )}
        </select>
        <textarea
          className={styles.workflowPayloadInput}
          aria-label="Workflow input JSON"
          value={workflowInput}
          onChange={(event) => onInputChange(event.target.value)}
          rows={1}
        />
        <button
          type="button"
          className={styles.workflowRunButton}
          onClick={onStart}
          disabled={!selectedWorkflowName || isStarting}
        >
          {isStarting ? "Starting" : "Start"}
        </button>
      </div>
      {visibleErrors.map((message) => (
        <span key={message} className={styles.workflowError}>
          {message}
        </span>
      ))}
    </div>
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
      {error ? (
        <div className={styles.workflowError}>{error.message}</div>
      ) : null}
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

function defaultWorkflowInput(taskId: string): string {
  return JSON.stringify({ parentId: taskId }, null, 2);
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
