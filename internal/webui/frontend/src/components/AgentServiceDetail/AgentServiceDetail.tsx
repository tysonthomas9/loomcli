import { useEffect, useRef, useState } from "react";

import type {
  AgentServiceDTO,
  DriverRunDTO,
  DriverRunStatus,
  RunEventDTO,
} from "@/api/agentServices";
import { MarkdownRenderer } from "@/components/IssueDetailPanel";
import { RolePromptCard } from "@/components/RolePromptCard";
import {
  useAgentServiceJournal,
  useAgentServiceRunEvents,
  useAgentServiceRunTasks,
  useAgentServiceRuns,
} from "@/hooks/workspace";
import { ApiError } from "@/types/common";
import {
  agentServiceDotState,
  agentServiceHealthLabel,
  bindingCadenceLabel,
  firstEnabledCronBinding,
  formatFireTime,
} from "@/utils/bindingDisplay";
import { formatStatusLabel } from "@/utils/issue";

import styles from "./AgentServiceDetail.module.css";
import { HarnessLog, TaskLogsSection } from "./RunLogs";
import { AgentServiceSettings } from "./AgentServiceSettings";

export interface AgentServiceDetailProps {
  workspaceId: string;
  service: AgentServiceDTO;
  onRemoved?: () => void;
}

function behaviorLabel(service: AgentServiceDTO): string {
  if (service.behavior.scripted) {
    const role = service.behavior.roleDisplayName?.trim();
    return role ? `${role} scripted role` : "Scripted autonomous agent";
  }
  if (service.behavior.roleName?.trim()) return "Prompt autonomous agent";
  return "Autonomous agent";
}

function runTimestamp(run: DriverRunDTO): string {
  return formatFireTime(run.startedAt ?? run.createdAt);
}

const HEARTBEAT_ACTION = "driver_run.heartbeat";

export type TimelineRow =
  | { kind: "event"; event: RunEventDTO }
  | {
      kind: "heartbeats";
      id: string;
      actor: string;
      count: number;
      first: RunEventDTO;
      last: RunEventDTO;
    };

// Executor heartbeats arrive every ~2s; a completed run buries its lifecycle
// events under dozens of them, so consecutive same-actor heartbeats fold into
// one summary row.
export function foldHeartbeats(events: RunEventDTO[]): TimelineRow[] {
  const rows: TimelineRow[] = [];
  for (const event of events) {
    const previous = rows[rows.length - 1];
    if (event.action !== HEARTBEAT_ACTION) {
      rows.push({ kind: "event", event });
      continue;
    }
    if (previous?.kind === "heartbeats" && previous.actor === event.actor) {
      previous.count += 1;
      previous.last = event;
      continue;
    }
    rows.push({
      kind: "heartbeats",
      id: `heartbeats-${event.id}`,
      actor: event.actor,
      count: 1,
      first: event,
      last: event,
    });
  }
  return rows;
}

function heartbeatRangeLabel(
  row: Extract<TimelineRow, { kind: "heartbeats" }>,
): string {
  if (row.count === 1) return HEARTBEAT_ACTION;
  const first = formatFireTime(row.first.timestamp) || row.first.timestamp;
  const last = formatFireTime(row.last.timestamp) || row.last.timestamp;
  const range = first === last ? first : `${first} – ${last}`;
  return `${row.count} heartbeats (${range})`;
}

function runSummary(run: DriverRunDTO): string {
  return run.summary?.trim() || run.errorClass?.trim() || "No run summary";
}

function isLiveRun(status: DriverRunStatus): boolean {
  return status === "queued" || status === "running";
}

function isTerminalRun(status: DriverRunStatus): boolean {
  return !isLiveRun(status) && status !== "suspended_awaiting_event";
}

function runDuration(run: DriverRunDTO): string {
  const startedAt = new Date(run.startedAt ?? run.createdAt).getTime();
  const finishedAt = run.finishedAt
    ? new Date(run.finishedAt).getTime()
    : isTerminalRun(run.status)
      ? new Date(run.updatedAt).getTime()
      : Date.now();
  if (
    Number.isNaN(startedAt) ||
    Number.isNaN(finishedAt) ||
    finishedAt < startedAt
  ) {
    return "Unknown";
  }
  const totalSeconds = Math.floor((finishedAt - startedAt) / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function oldestFirst(events: RunEventDTO[]): RunEventDTO[] {
  return [...events].sort((left, right) => {
    const leftTime = new Date(left.timestamp).getTime();
    const rightTime = new Date(right.timestamp).getTime();
    if (!Number.isNaN(leftTime) && !Number.isNaN(rightTime)) {
      const byTime = leftTime - rightTime;
      if (byTime !== 0) return byTime;
    }
    return left.id.localeCompare(right.id);
  });
}

export function AgentServiceDetail({
  workspaceId,
  service: initialService,
  onRemoved,
}: AgentServiceDetailProps): JSX.Element {
  const [service, setService] = useState(initialService);
  const {
    runs,
    total,
    loading,
    initialized,
    error,
    notFound,
    refresh: refreshRuns,
  } = useAgentServiceRuns(workspaceId, service.id);
  const [activeTab, setActiveTab] = useState<"overview" | "journal">(
    "overview",
  );
  const [expandedRunId, setExpandedRunId] = useState<string | null>(null);
  const [liveLogRefreshTick, setLiveLogRefreshTick] = useState(0);
  const previousExpandedRunRef = useRef<{
    runId: string;
    status: DriverRunStatus;
  } | null>(null);
  const {
    events: unsortedEvents,
    loading: eventsLoading,
    error: eventsError,
    refresh: refreshEvents,
  } = useAgentServiceRunEvents(workspaceId, expandedRunId);
  const {
    tasks,
    loading: tasksLoading,
    initialized: tasksInitialized,
    error: tasksError,
    refresh: refreshTasks,
  } = useAgentServiceRunTasks(workspaceId, service.id, expandedRunId);
  const {
    journal,
    loading: journalLoading,
    initialized: journalInitialized,
    error: journalError,
    refresh: refreshJournal,
  } = useAgentServiceJournal(workspaceId, service.id, {
    enabled: activeTab === "journal",
  });
  const events = oldestFirst(unsortedEvents);
  const nextFire = formatFireTime(service.nextFireAt);
  const nextFireBinding = firstEnabledCronBinding(service);
  const healthLabel = agentServiceHealthLabel(service);
  const expandedRun = expandedRunId
    ? (runs.find((run) => run.runId === expandedRunId) ?? null)
    : null;

  useEffect(() => {
    setService(initialService);
  }, [initialService]);

  useEffect(() => {
    setActiveTab("overview");
    setExpandedRunId(null);
    setLiveLogRefreshTick(0);
    previousExpandedRunRef.current = null;
  }, [service.id, workspaceId]);

  useEffect(() => {
    if (!expandedRun || !isLiveRun(expandedRun.status)) return;
    const interval = window.setInterval(() => {
      void Promise.allSettled([refreshEvents(), refreshRuns(), refreshTasks()]);
      setLiveLogRefreshTick((tick) => tick + 1);
    }, 5_000);
    return () => window.clearInterval(interval);
  }, [expandedRun, refreshEvents, refreshRuns, refreshTasks]);

  useEffect(() => {
    const previous = previousExpandedRunRef.current;
    if (
      expandedRun &&
      previous?.runId === expandedRun.runId &&
      isLiveRun(previous.status) &&
      isTerminalRun(expandedRun.status)
    ) {
      void refreshJournal();
    }
    previousExpandedRunRef.current = expandedRun
      ? { runId: expandedRun.runId, status: expandedRun.status }
      : null;
  }, [expandedRun, refreshJournal]);

  const toggleRun = (runId: string): void => {
    setExpandedRunId((current) => (current === runId ? null : runId));
  };

  const selectJournal = (): void => {
    setActiveTab("journal");
  };

  if (notFound) {
    return (
      <div className={styles.empty} data-testid="agent-service-not-found">
        This autonomous agent no longer exists.
      </div>
    );
  }

  return (
    <div className={styles.detail} data-testid="agent-service-detail">
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Autonomous</p>
          <h1 className={styles.name}>{service.name.trim() || service.id}</h1>
          <p className={styles.subtitle}>{behaviorLabel(service)}</p>
        </div>
        <span
          className={styles.healthPill}
          data-state={agentServiceDotState(service)}
        >
          <span className={styles.healthDot} aria-hidden="true" />
          {healthLabel}
        </span>
      </header>

      <div className={styles.tabs} role="tablist" aria-label="Agent sections">
        <button
          type="button"
          role="tab"
          id="agent-service-overview-tab"
          aria-controls="agent-service-overview-panel"
          aria-selected={activeTab === "overview"}
          className={styles.tab}
          onClick={() => setActiveTab("overview")}
        >
          Overview
        </button>
        <button
          type="button"
          role="tab"
          id="agent-service-journal-tab"
          aria-controls="agent-service-journal-panel"
          aria-selected={activeTab === "journal"}
          className={styles.tab}
          onClick={selectJournal}
        >
          Journal
        </button>
      </div>

      {activeTab === "overview" ? (
        <div
          className={styles.scrollArea}
          role="tabpanel"
          id="agent-service-overview-panel"
          aria-labelledby="agent-service-overview-tab"
        >
          {service.errors.length > 0 ? (
            <section
              className={`${styles.card} ${styles.warningCard}`}
              role="alert"
              data-testid="agent-service-health-errors"
            >
              <h2 className={styles.cardTitle}>Health unavailable</h2>
              <ul className={styles.errorList}>
                {service.errors.map((message) => (
                  <li key={message}>{message}</li>
                ))}
              </ul>
            </section>
          ) : null}

          {service.behavior.scripted ? (
            <AgentServiceSettings
              workspaceId={workspaceId}
              service={service}
              onChange={setService}
              {...(onRemoved ? { onRemoved } : {})}
            />
          ) : null}

          <section className={styles.card}>
            <h2 className={styles.cardTitle}>Record</h2>
            <dl className={styles.definitionGrid}>
              <div>
                <dt>ID</dt>
                <dd>{service.id}</dd>
              </div>
              <div>
                <dt>Trigger kind</dt>
                <dd>{formatStatusLabel(service.triggerKind)}</dd>
              </div>
              <div>
                <dt>Desired state</dt>
                <dd>{service.enabled ? "Enabled" : "Disabled"}</dd>
              </div>
              <div>
                <dt>Last run</dt>
                <dd>
                  {service.lastRunStatus
                    ? formatStatusLabel(service.lastRunStatus)
                    : "No runs"}
                </dd>
              </div>
              <div>
                <dt>Consecutive failures</dt>
                <dd>{service.consecutiveFailures}</dd>
              </div>
              <div>
                <dt>Next fire</dt>
                <dd>
                  {service.enabled ? nextFire || "Not scheduled" : "Paused"}
                </dd>
              </div>
              {service.behavior.roleName ? (
                <div>
                  <dt>Role</dt>
                  <dd>{service.behavior.roleName}</dd>
                </div>
              ) : null}
              {service.behavior.driverId ? (
                <div>
                  <dt>Driver</dt>
                  <dd>{service.behavior.driverId}</dd>
                </div>
              ) : null}
              {service.behavior.driverVersionId ? (
                <div>
                  <dt>Driver version</dt>
                  <dd>{service.behavior.driverVersionId}</dd>
                </div>
              ) : null}
            </dl>
          </section>

          {service.behavior.roleName?.trim() ? (
            <RolePromptCard
              workspaceId={workspaceId}
              roleName={service.behavior.roleName.trim()}
            />
          ) : null}

          <section className={styles.card}>
            <h2 className={styles.cardTitle}>Bindings</h2>
            {service.bindings.length === 0 ? (
              <p className={styles.emptyText}>No bindings configured.</p>
            ) : (
              <div className={styles.bindingList}>
                {service.bindings.map((binding) => (
                  <article className={styles.bindingRow} key={binding.id}>
                    <div className={styles.rowMain}>
                      <strong>{bindingCadenceLabel(binding)}</strong>
                      <span>{binding.routeKey || binding.id}</span>
                    </div>
                    <div className={styles.rowMeta}>
                      <span>{binding.enabled ? "Enabled" : "Disabled"}</span>
                      <span>
                        {!service.enabled
                          ? "Paused"
                          : binding.id === nextFireBinding?.id && nextFire
                            ? `Next ${nextFire}`
                            : "No next fire"}
                      </span>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </section>

          <section className={styles.card} data-testid="agent-service-runs">
            <div className={styles.cardHeadingRow}>
              <h2 className={styles.cardTitle}>Recent runs</h2>
              {initialized && !error ? (
                <span className={styles.total}>{total}</span>
              ) : null}
            </div>
            {error ? (
              <p className={styles.errorText} role="alert">
                Run history unavailable: {error.message}
              </p>
            ) : loading && !initialized ? (
              <p className={styles.emptyText}>Loading run history…</p>
            ) : runs.length === 0 ? (
              <p className={styles.emptyText}>No runs yet.</p>
            ) : (
              <div className={styles.runList}>
                {runs.map((run) => (
                  <article
                    className={styles.runRow}
                    data-testid={`agent-service-run-${run.runId}`}
                    key={run.runId}
                  >
                    <button
                      type="button"
                      className={styles.runToggle}
                      aria-expanded={expandedRunId === run.runId}
                      aria-controls={`agent-service-run-panel-${run.runId}`}
                      onClick={() => toggleRun(run.runId)}
                    >
                      <span
                        className={styles.runStatus}
                        data-status={run.status}
                      >
                        {formatStatusLabel(run.status)}
                      </span>
                      <span className={styles.rowMain}>
                        <strong>{runSummary(run)}</strong>
                        <span>{run.runId}</span>
                      </span>
                      <span className={styles.runTimes}>
                        <time dateTime={run.startedAt ?? run.createdAt}>
                          Started {runTimestamp(run)}
                        </time>
                        {run.finishedAt && formatFireTime(run.finishedAt) ? (
                          <time dateTime={run.finishedAt}>
                            Finished {formatFireTime(run.finishedAt)}
                          </time>
                        ) : null}
                      </span>
                      <span className={styles.chevron} aria-hidden="true">
                        {expandedRunId === run.runId ? "▾" : "▸"}
                      </span>
                    </button>
                    {expandedRunId === run.runId ? (
                      <div
                        className={styles.runPanel}
                        id={`agent-service-run-panel-${run.runId}`}
                      >
                        <div className={styles.runPanelHeading}>
                          <span
                            className={styles.runStatus}
                            data-status={run.status}
                          >
                            {formatStatusLabel(run.status)}
                          </span>
                          <p className={styles.fullSummary}>
                            {runSummary(run)}
                          </p>
                        </div>
                        <dl className={styles.runDefinitionGrid}>
                          <div>
                            <dt>Run ID</dt>
                            <dd>{run.runId}</dd>
                          </div>
                          <div>
                            <dt>Started</dt>
                            <dd>{runTimestamp(run) || "Unknown"}</dd>
                          </div>
                          <div>
                            <dt>Finished</dt>
                            <dd>
                              {formatFireTime(run.finishedAt) || "Not finished"}
                            </dd>
                          </div>
                          <div>
                            <dt>Duration</dt>
                            <dd>{runDuration(run)}</dd>
                          </div>
                          {run.errorClass?.trim() ? (
                            <div>
                              <dt>Error class</dt>
                              <dd>{run.errorClass}</dd>
                            </div>
                          ) : null}
                        </dl>
                        {run.output?.flue_stdout_tail ? (
                          <section className={styles.outputSection}>
                            <h3>Output (tail)</h3>
                            <pre>{run.output.flue_stdout_tail}</pre>
                          </section>
                        ) : null}
                        <HarnessLog workspaceId={workspaceId} run={run} />
                        {!run.output?.flue_stdout_tail &&
                        !run.output?.logs_ref ? (
                          <p className={styles.emptyText}>
                            No output was captured for this run.
                          </p>
                        ) : null}
                        <TaskLogsSection
                          workspaceId={workspaceId}
                          tasks={tasks}
                          loading={tasksLoading}
                          initialized={tasksInitialized}
                          error={tasksError}
                          liveRefreshTick={liveLogRefreshTick}
                        />
                        <section className={styles.timelineSection}>
                          <h3>Event timeline</h3>
                          {eventsError ? (
                            <p className={styles.errorText} role="alert">
                              Run events unavailable: {eventsError.message}
                            </p>
                          ) : eventsLoading && events.length === 0 ? (
                            <p className={styles.emptyText}>Loading events…</p>
                          ) : events.length === 0 ? (
                            <p className={styles.emptyText}>No events yet.</p>
                          ) : (
                            <ol className={styles.timeline}>
                              {foldHeartbeats(events).map((row) =>
                                row.kind === "event" ? (
                                  <li key={row.event.id}>
                                    <time dateTime={row.event.timestamp}>
                                      {formatFireTime(row.event.timestamp) ||
                                        row.event.timestamp}
                                    </time>
                                    <span>{row.event.actor}</span>
                                    <strong>{row.event.action}</strong>
                                  </li>
                                ) : (
                                  <li
                                    key={row.id}
                                    data-testid="timeline-heartbeat-fold"
                                  >
                                    <time dateTime={row.first.timestamp}>
                                      {formatFireTime(row.first.timestamp) ||
                                        row.first.timestamp}
                                    </time>
                                    <span>{row.actor}</span>
                                    <strong>{heartbeatRangeLabel(row)}</strong>
                                  </li>
                                ),
                              )}
                            </ol>
                          )}
                        </section>
                      </div>
                    ) : null}
                  </article>
                ))}
              </div>
            )}
          </section>
        </div>
      ) : (
        <div
          className={styles.scrollArea}
          role="tabpanel"
          id="agent-service-journal-panel"
          aria-labelledby="agent-service-journal-tab"
        >
          <section className={styles.card} data-testid="agent-service-journal">
            <div className={styles.cardHeadingRow}>
              <h2 className={styles.cardTitle}>Journal</h2>
              {journal ? (
                <time className={styles.total} dateTime={journal.modifiedAt}>
                  Updated {formatFireTime(journal.modifiedAt)}
                </time>
              ) : null}
            </div>
            {journalLoading && !journalInitialized ? (
              <p className={styles.emptyText}>Loading journal…</p>
            ) : journalError instanceof ApiError &&
              journalError.status === 404 ? (
              <p className={styles.emptyText} data-testid="journal-empty">
                {journalError.message.includes("no journal yet")
                  ? "No journal yet. The scout journal will appear after its first completed run."
                  : "This autonomous agent does not have a journal."}
              </p>
            ) : journalError ? (
              <p className={styles.errorText} role="alert">
                Journal unavailable: {journalError.message}
              </p>
            ) : journal ? (
              <>
                {journal.truncated ? (
                  <p className={styles.truncationNotice} role="status">
                    Showing the last 512 KiB of this journal.
                  </p>
                ) : null}
                <MarkdownRenderer
                  content={journal.content}
                  className={styles.journalContent}
                />
              </>
            ) : (
              <p className={styles.emptyText}>No journal content.</p>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
