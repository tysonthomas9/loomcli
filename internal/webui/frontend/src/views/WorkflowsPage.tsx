import { useEffect, useMemo, useState } from "react";

import {
  isWorkflowRunLive,
  useCancelWorkflowRun,
  useStartWorkflowRun,
  useWorkflowDefinitions,
  useWorkflowRunEvents,
  useWorkflowRuns,
  type WorkflowDefinition,
  type WorkflowRun,
  type WorkflowRunEvent,
  type WorkflowRunListItem,
  type WorkflowRunStatus,
} from "@/hooks/workflows";
import { ErrorBoundary } from "@/components";
import { useRouteView } from "@/hooks";

import styles from "./WorkflowsPage.module.css";

type StatusFilter = "" | WorkflowRunStatus;

export function WorkflowsPage(): JSX.Element {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <WorkflowsSurface />
    </ErrorBoundary>
  );
}

function WorkflowsSurface(): JSX.Element {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const workflowRunsParams = useMemo(
    () =>
      statusFilter ? { status: statusFilter, limit: 100 } : { limit: 100 },
    [statusFilter],
  );
  const { runs, isLoading, error, refetch } =
    useWorkflowRuns(workflowRunsParams);
  const {
    definitions,
    isLoading: definitionsLoading,
    error: definitionsError,
  } = useWorkflowDefinitions();
  const startWorkflowRun = useStartWorkflowRun();
  const cancelWorkflowRun = useCancelWorkflowRun();
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [selectedWorkflowName, setSelectedWorkflowName] = useState("");
  const [workflowInput, setWorkflowInput] = useState("{}");
  const [isStarting, setIsStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [cancelingRunId, setCancelingRunId] = useState<string | null>(null);
  const [cancelError, setCancelError] = useState<string | null>(null);

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

  const handleStart = async () => {
    if (!selectedWorkflowName) return;
    setStartError(null);
    setIsStarting(true);
    try {
      const input = JSON.parse(workflowInput.trim() || "{}") as unknown;
      const response = await startWorkflowRun(selectedWorkflowName, {
        input,
        once: true,
        wait: false,
      });
      if (response.run?.run_id) setSelectedRunId(response.run.run_id);
      refetch();
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsStarting(false);
    }
  };

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

  const visibleErrors = [
    error?.message,
    definitionsError?.message,
    eventsError && events.length === 0 ? eventsError.message : null,
    startError,
    cancelError,
  ].filter((message): message is string => Boolean(message));

  return (
    <section className={styles.page} data-testid="workflows-page">
      <WorkflowToolbar
        definitions={definitions}
        definitionsLoading={definitionsLoading}
        selectedWorkflowName={selectedWorkflowName}
        workflowInput={workflowInput}
        statusFilter={statusFilter}
        isStarting={isStarting}
        onSelectWorkflow={setSelectedWorkflowName}
        onInputChange={setWorkflowInput}
        onStatusFilterChange={setStatusFilter}
        onStart={() => void handleStart()}
        onRefresh={refetch}
      />
      {visibleErrors.map((message) => (
        <div key={message} className={styles.error} role="alert">
          {message}
        </div>
      ))}
      <div className={styles.grid}>
        <WorkflowRunList
          runs={runs}
          isLoading={isLoading}
          selectedRunId={selectedRunId}
          onSelectRun={setSelectedRunId}
        />
        <WorkflowRunDetail
          run={selectedRun}
          status={selectedDisplayStatus}
          finishedAt={selectedDisplayFinishedAt}
          events={events}
          isLoadingEvents={eventsLoading}
          streamError={eventsError && events.length > 0 ? eventsError : null}
          cancelingRunId={cancelingRunId}
          onCancel={(run) => void handleCancel(run)}
        />
      </div>
    </section>
  );
}

function WorkflowToolbar({
  definitions,
  definitionsLoading,
  selectedWorkflowName,
  workflowInput,
  statusFilter,
  isStarting,
  onSelectWorkflow,
  onInputChange,
  onStatusFilterChange,
  onStart,
  onRefresh,
}: {
  definitions: WorkflowDefinition[];
  definitionsLoading: boolean;
  selectedWorkflowName: string;
  workflowInput: string;
  statusFilter: StatusFilter;
  isStarting: boolean;
  onSelectWorkflow: (name: string) => void;
  onInputChange: (value: string) => void;
  onStatusFilterChange: (status: StatusFilter) => void;
  onStart: () => void;
  onRefresh: () => void;
}): JSX.Element {
  return (
    <div className={styles.toolbar}>
      <select
        className={styles.select}
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
        className={styles.input}
        aria-label="Workflow input JSON"
        rows={1}
        value={workflowInput}
        onChange={(event) => onInputChange(event.target.value)}
      />
      <select
        className={styles.select}
        aria-label="Workflow status filter"
        value={statusFilter}
        onChange={(event) =>
          onStatusFilterChange(event.target.value as StatusFilter)
        }
      >
        <option value="">All statuses</option>
        <option value="queued">Queued</option>
        <option value="running">Running</option>
        <option value="waiting">Waiting</option>
        <option value="completed">Completed</option>
        <option value="failed">Failed</option>
        <option value="cancelled">Cancelled</option>
      </select>
      <button
        type="button"
        className={styles.button}
        onClick={onStart}
        disabled={!selectedWorkflowName || isStarting}
      >
        {isStarting ? "Starting" : "Start"}
      </button>
      <button
        type="button"
        className={styles.secondaryButton}
        onClick={onRefresh}
      >
        Refresh
      </button>
    </div>
  );
}

function WorkflowRunList({
  runs,
  isLoading,
  selectedRunId,
  onSelectRun,
}: {
  runs: WorkflowRunListItem[];
  isLoading: boolean;
  selectedRunId: string | null;
  onSelectRun: (runId: string) => void;
}): JSX.Element {
  if (isLoading && runs.length === 0) {
    return <div className={styles.runList} />;
  }
  if (runs.length === 0) {
    return <div className={styles.empty}>No workflow runs</div>;
  }
  return (
    <div className={styles.runList}>
      {runs.map((item) => (
        <WorkflowRunRow
          key={item.run.run_id}
          item={item}
          selected={item.run.run_id === selectedRunId}
          onSelect={() => onSelectRun(item.run.run_id)}
        />
      ))}
    </div>
  );
}

function WorkflowRunRow({
  item,
  selected,
  onSelect,
}: {
  item: WorkflowRunListItem;
  selected: boolean;
  onSelect: () => void;
}): JSX.Element {
  const run = item.run;
  return (
    <button
      type="button"
      className={styles.runRow}
      data-selected={selected}
      data-testid={`workflow-run-row-${run.run_id}`}
      onClick={onSelect}
      aria-label={`${run.workflow_name} workflow run, ${formatStatus(run.status)}`}
    >
      <span className={styles.statusDot} data-status={run.status} />
      <span className={styles.runMain}>
        <span className={styles.runTop}>
          <span className={styles.runName}>{run.workflow_name}</span>
          <span className={styles.status}>{formatStatus(run.status)}</span>
        </span>
        <span className={styles.runMeta}>
          <span>{shortRunID(run.run_id)}</span>
          <span>{formatTimestamp(run.created_at)}</span>
          {item.task_runs?.length ? (
            <span>
              {item.task_runs.length}{" "}
              {item.task_runs.length === 1 ? "task run" : "task runs"}
            </span>
          ) : null}
        </span>
      </span>
    </button>
  );
}

function WorkflowRunDetail({
  run,
  status,
  finishedAt,
  events,
  isLoadingEvents,
  streamError,
  cancelingRunId,
  onCancel,
}: {
  run: WorkflowRun | null;
  status: WorkflowRunStatus | null;
  finishedAt: string | null | undefined;
  events: WorkflowRunEvent[];
  isLoadingEvents: boolean;
  streamError: Error | null;
  cancelingRunId: string | null;
  onCancel: (run: WorkflowRun) => void;
}): JSX.Element {
  if (!run) {
    return <div className={styles.empty}>Select a workflow run</div>;
  }
  const displayStatus = status ?? run.status;
  const isLive = isWorkflowRunLive(displayStatus);
  return (
    <div className={styles.eventPanel}>
      <div className={styles.detailHeader}>
        <div className={styles.detailTitle}>
          <div className={styles.detailTitleLine}>
            <span className={styles.statusDot} data-status={displayStatus} />
            <span className={styles.runName}>{run.workflow_name}</span>
            <span className={styles.status}>{formatStatus(displayStatus)}</span>
          </div>
          <div className={styles.detailMeta}>
            <span>{shortRunID(run.run_id)}</span>
            <span>{formatTimestamp(run.created_at)}</span>
            {finishedAt ? (
              <span>Finished {formatTimestamp(finishedAt)}</span>
            ) : null}
          </div>
        </div>
        {isLive ? (
          <button
            type="button"
            className={styles.dangerButton}
            onClick={() => onCancel(run)}
            disabled={cancelingRunId === run.run_id}
          >
            {cancelingRunId === run.run_id ? "Canceling" : "Cancel"}
          </button>
        ) : null}
      </div>
      <WorkflowEventList
        events={events}
        isLoading={isLoadingEvents}
        streamError={streamError}
      />
    </div>
  );
}

function WorkflowEventList({
  events,
  isLoading,
  streamError,
}: {
  events: WorkflowRunEvent[];
  isLoading: boolean;
  streamError: Error | null;
}): JSX.Element {
  if (isLoading && events.length === 0) {
    return <div className={styles.empty}>Loading events</div>;
  }
  if (events.length === 0) {
    return <div className={styles.empty}>No events recorded</div>;
  }
  return (
    <div className={styles.eventList}>
      {streamError ? (
        <div className={styles.error} role="alert">
          {streamError.message}
        </div>
      ) : null}
      {events.map((event) => (
        <div
          key={event.event_id}
          className={styles.eventRow}
          data-testid={`workflow-event-${event.type}`}
        >
          <div className={styles.eventTop}>
            <span className={styles.eventType}>{event.type}</span>
            <span className={styles.eventTime}>
              {formatTimestamp(event.created_at)}
            </span>
          </div>
          {event.message ? (
            <div className={styles.eventMessage}>{event.message}</div>
          ) : null}
          {event.data != null ? (
            <pre className={styles.eventData}>
              {formatEventData(event.data)}
            </pre>
          ) : null}
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

function formatTimestamp(ts: string | undefined | null): string {
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
