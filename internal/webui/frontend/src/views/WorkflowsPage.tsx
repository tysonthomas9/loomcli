import { useEffect, useMemo, useState } from "react";

import {
  isWorkflowRunLive,
  useCancelWorkflowRun,
  useStartWorkflowRun,
  useWorkflowDefinitions,
  useWorkflowRunEventSnapshots,
  useWorkflowRunEvents,
  useWorkflowRuns,
  type WorkflowDefinition,
  type WorkflowRun,
  type WorkflowRunEventStreamStatus,
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
  const [isBatchCanceling, setIsBatchCanceling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [comparedRunIds, setComparedRunIds] = useState<Set<string>>(
    () => new Set(),
  );

  const selectedItem = useMemo(
    () => runs.find((item) => item.run.run_id === selectedRunId) ?? null,
    [runs, selectedRunId],
  );
  const comparedItems = useMemo(
    () => runs.filter((item) => comparedRunIds.has(item.run.run_id)),
    [runs, comparedRunIds],
  );
  const comparedRuns = useMemo(
    () => comparedItems.map((item) => item.run),
    [comparedItems],
  );
  const comparedLiveItems = useMemo(
    () => comparedItems.filter((item) => isWorkflowRunLive(item.run.status)),
    [comparedItems],
  );
  const {
    eventsByRunId: comparisonEventsByRunId,
    isLoading: comparisonEventsLoading,
    error: comparisonEventsError,
    refetch: refetchComparisonEvents,
  } = useWorkflowRunEventSnapshots(comparedRuns);
  const selectedRun = selectedItem?.run ?? null;
  const selectedIsLive = selectedRun
    ? isWorkflowRunLive(selectedRun.status)
    : false;
  const {
    events,
    isLoading: eventsLoading,
    error: eventsError,
    refetch: refetchEvents,
    retryStream,
    streamStatus,
    streamCompletion,
    reconnectCount,
    lastEventIndex,
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
      setComparedRunIds(new Set());
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
    setComparedRunIds((current) => {
      const available = new Set(runs.map((item) => item.run.run_id));
      const next = new Set(
        [...current].filter((runId) => available.has(runId)),
      );
      return next.size === current.size ? current : next;
    });
  }, [runs]);

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
      refetchComparisonEvents();
    } catch (err) {
      setCancelError(err instanceof Error ? err.message : String(err));
    } finally {
      setCancelingRunId(null);
    }
  };

  const handleToggleComparedRun = (runId: string, checked: boolean) => {
    setComparedRunIds((current) => {
      const next = new Set(current);
      if (checked) next.add(runId);
      else next.delete(runId);
      return next;
    });
  };

  const handleBatchCancel = async () => {
    if (comparedLiveItems.length === 0) return;
    setCancelError(null);
    setIsBatchCanceling(true);
    const failures: string[] = [];
    try {
      for (const item of comparedLiveItems) {
        try {
          await cancelWorkflowRun(item.run.run_id);
        } catch (err) {
          failures.push(
            `${shortRunID(item.run.run_id)}: ${
              err instanceof Error ? err.message : String(err)
            }`,
          );
        }
      }
      if (failures.length > 0) {
        setCancelError(`Failed to cancel ${failures.join(", ")}`);
      }
      refetch();
      refetchEvents();
      refetchComparisonEvents();
    } finally {
      setIsBatchCanceling(false);
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
      <WorkflowComparisonBar
        comparedItems={comparedItems}
        eventsByRunId={comparisonEventsByRunId}
        liveCount={comparedLiveItems.length}
        isCanceling={isBatchCanceling}
        isLoadingEvents={comparisonEventsLoading}
        eventError={comparisonEventsError}
        onCancelSelected={() => void handleBatchCancel()}
        onClear={() => setComparedRunIds(new Set())}
      />
      <div className={styles.grid}>
        <WorkflowRunList
          runs={runs}
          isLoading={isLoading}
          selectedRunId={selectedRunId}
          comparedRunIds={comparedRunIds}
          onSelectRun={setSelectedRunId}
          onToggleComparedRun={handleToggleComparedRun}
        />
        <WorkflowRunDetail
          run={selectedRun}
          status={selectedDisplayStatus}
          finishedAt={selectedDisplayFinishedAt}
          events={events}
          isLoadingEvents={eventsLoading}
          streamError={eventsError && events.length > 0 ? eventsError : null}
          streamStatus={streamStatus}
          reconnectCount={reconnectCount}
          lastEventIndex={lastEventIndex}
          cancelingRunId={cancelingRunId}
          onCancel={(run) => void handleCancel(run)}
          onRetryStream={retryStream}
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
  comparedRunIds,
  onSelectRun,
  onToggleComparedRun,
}: {
  runs: WorkflowRunListItem[];
  isLoading: boolean;
  selectedRunId: string | null;
  comparedRunIds: Set<string>;
  onSelectRun: (runId: string) => void;
  onToggleComparedRun: (runId: string, checked: boolean) => void;
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
          compared={comparedRunIds.has(item.run.run_id)}
          onSelect={() => onSelectRun(item.run.run_id)}
          onToggleCompare={(checked) =>
            onToggleComparedRun(item.run.run_id, checked)
          }
        />
      ))}
    </div>
  );
}

function WorkflowRunRow({
  item,
  selected,
  compared,
  onSelect,
  onToggleCompare,
}: {
  item: WorkflowRunListItem;
  selected: boolean;
  compared: boolean;
  onSelect: () => void;
  onToggleCompare: (checked: boolean) => void;
}): JSX.Element {
  const run = item.run;
  return (
    <div
      className={styles.runRow}
      data-selected={selected}
      data-testid={`workflow-run-row-${run.run_id}`}
    >
      <input
        className={styles.runCheckbox}
        type="checkbox"
        aria-label={`Compare run ${run.run_id}`}
        checked={compared}
        onChange={(event) => onToggleCompare(event.target.checked)}
      />
      <button
        type="button"
        className={styles.runRowButton}
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
    </div>
  );
}

function WorkflowComparisonBar({
  comparedItems,
  eventsByRunId,
  liveCount,
  isCanceling,
  isLoadingEvents,
  eventError,
  onCancelSelected,
  onClear,
}: {
  comparedItems: WorkflowRunListItem[];
  eventsByRunId: Record<string, WorkflowRunEvent[]>;
  liveCount: number;
  isCanceling: boolean;
  isLoadingEvents: boolean;
  eventError: Error | null;
  onCancelSelected: () => void;
  onClear: () => void;
}): JSX.Element | null {
  if (comparedItems.length === 0) return null;
  return (
    <div className={styles.comparisonBar} data-testid="workflow-comparison-bar">
      <div className={styles.comparisonHeader}>
        <span className={styles.comparisonTitle}>
          Comparing {comparedItems.length}{" "}
          {comparedItems.length === 1 ? "run" : "runs"}
        </span>
        <span className={styles.comparisonMeta}>
          {liveCount} live {liveCount === 1 ? "run" : "runs"}
        </span>
        <button
          type="button"
          className={styles.secondaryButton}
          onClick={onClear}
        >
          Clear
        </button>
        <button
          type="button"
          className={styles.dangerButton}
          onClick={onCancelSelected}
          disabled={liveCount === 0 || isCanceling}
        >
          {isCanceling ? "Canceling selected" : "Cancel selected live"}
        </button>
      </div>
      {eventError ? (
        <div className={styles.comparisonError} role="alert">
          {eventError.message}
        </div>
      ) : null}
      <div className={styles.comparisonList}>
        {comparedItems.map((item) => (
          <div
            key={item.run.run_id}
            className={styles.comparisonCard}
            data-testid={`workflow-comparison-run-${item.run.run_id}`}
          >
            <div className={styles.comparisonCardTop}>
              <span
                className={styles.statusDot}
                data-status={item.run.status}
              />
              <span className={styles.runName}>{item.run.workflow_name}</span>
              <span className={styles.status}>
                {formatStatus(item.run.status)}
              </span>
            </div>
            <div className={styles.runMeta}>
              <span>{shortRunID(item.run.run_id)}</span>
              <span>{formatTimestamp(item.run.created_at)}</span>
              {item.task_runs?.length ? (
                <span>
                  {item.task_runs.length}{" "}
                  {item.task_runs.length === 1 ? "task run" : "task runs"}
                </span>
              ) : null}
            </div>
          </div>
        ))}
      </div>
      <div
        className={styles.timelineGrid}
        data-testid="workflow-comparison-timelines"
      >
        {comparedItems.map((item) => {
          const runId = item.run.run_id;
          const events = eventsByRunId[runId] ?? [];
          return (
            <div
              key={runId}
              className={styles.timelineColumn}
              data-testid={`workflow-comparison-timeline-${runId}`}
            >
              <div className={styles.timelineHeader}>
                <span>{shortRunID(runId)}</span>
                <span>
                  {events.length} {events.length === 1 ? "event" : "events"}
                </span>
              </div>
              {isLoadingEvents && events.length === 0 ? (
                <div className={styles.timelineEmpty}>Loading events</div>
              ) : events.length === 0 ? (
                <div className={styles.timelineEmpty}>No events recorded</div>
              ) : (
                <div className={styles.timelineEvents}>
                  {events.map((event) => (
                    <div
                      key={event.event_id}
                      className={styles.timelineEvent}
                      data-testid={`workflow-comparison-event-${runId}-${event.event_index}`}
                    >
                      <div className={styles.timelineEventTop}>
                        <span className={styles.timelineEventIndex}>
                          #{event.event_index}
                        </span>
                        <span className={styles.timelineEventType}>
                          {event.type}
                        </span>
                        <span className={styles.timelineEventTime}>
                          {formatTimestamp(event.created_at)}
                        </span>
                      </div>
                      {event.message ? (
                        <div className={styles.timelineEventMessage}>
                          {event.message}
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function WorkflowRunDetail({
  run,
  status,
  finishedAt,
  events,
  isLoadingEvents,
  streamError,
  streamStatus,
  reconnectCount,
  lastEventIndex,
  cancelingRunId,
  onCancel,
  onRetryStream,
}: {
  run: WorkflowRun | null;
  status: WorkflowRunStatus | null;
  finishedAt: string | null | undefined;
  events: WorkflowRunEvent[];
  isLoadingEvents: boolean;
  streamError: Error | null;
  streamStatus: WorkflowRunEventStreamStatus;
  reconnectCount: number;
  lastEventIndex: number | null;
  cancelingRunId: string | null;
  onCancel: (run: WorkflowRun) => void;
  onRetryStream: () => void;
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
            <WorkflowStreamState
              status={streamStatus}
              reconnectCount={reconnectCount}
              lastEventIndex={lastEventIndex}
              canRetry={
                isLive &&
                (streamStatus === "reconnecting" || streamStatus === "error")
              }
              onRetry={onRetryStream}
            />
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

function WorkflowStreamState({
  status,
  reconnectCount,
  lastEventIndex,
  canRetry,
  onRetry,
}: {
  status: WorkflowRunEventStreamStatus;
  reconnectCount: number;
  lastEventIndex: number | null;
  canRetry: boolean;
  onRetry: () => void;
}): JSX.Element {
  return (
    <span className={styles.streamStateGroup}>
      <span className={styles.streamState} data-status={status}>
        {streamStatusLabel(status, reconnectCount)}
        {lastEventIndex != null ? ` #${lastEventIndex}` : ""}
      </span>
      {canRetry ? (
        <button
          type="button"
          className={styles.streamRetryButton}
          onClick={onRetry}
        >
          Retry stream
        </button>
      ) : null}
    </span>
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

function streamStatusLabel(
  status: WorkflowRunEventStreamStatus,
  reconnectCount: number,
): string {
  switch (status) {
    case "connecting":
      return "Connecting";
    case "connected":
      return "Live stream";
    case "reconnecting":
      return reconnectCount > 0
        ? `Reconnecting ${reconnectCount}`
        : "Reconnecting";
    case "polling":
      return "Polling";
    case "complete":
      return "Stream complete";
    case "error":
      return "Stream error";
    case "idle":
    default:
      return "Event history";
  }
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
