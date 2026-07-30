/**
 * WorkflowAgentDetail — detail view for a workflow-plane agent (trigger
 * binding), rendered inside the same AgentsPage shell that role agents use.
 *
 * Capability-based content (a binding has runs + config, but no worktree):
 *  - Runs tab (terminal-tab equivalent): binding-scoped run history with live
 *    status via the per-run SSE stream; idle state shows the next fire.
 *  - Info tab: driver + active version + trigger cadence + run stats, plus an
 *    "Edit configuration" button opening WorkflowSourceModal for the driver.
 *  - Button bar: Run now, Enable/Disable.
 * Git / Diff / Files are intentionally absent — a binding has no worktree.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { SessionRunDetail } from "@/components/SessionRunDetail/SessionRunDetail";
import { WorkflowSourceModal } from "@/components/WorkflowSourceModal";
import { useAgentHistory } from "@/hooks/agents";
import { useTaskSessions } from "@/hooks/terminal";
import {
  useWorkflowAgentDetail,
  type WorkflowAgentRunStats,
} from "@/hooks/workflows/useWorkflowAgentDetail";
import { useToast } from "@/hooks/ui/useToast";
import {
  getWorkflowRun,
  getWorkspaceRole,
  isTerminalWorkflowRunStatus,
  promptAgentRoleName,
  updateWorkspaceRole,
  type AgentRecordSummary,
  type TriggerBinding,
  type UpdateTriggerBindingRequest,
  type WorkflowRun,
  type WorkflowRunStatus,
} from "@/api"; // eslint-disable-line boundaries/dependencies -- Pending hook migration.
import type { SessionRecord } from "@/types/agent";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import {
  bindingCadenceLabel,
  bindingDisplayName,
  bindingHealth,
  bindingKindLabel,
  bindingRunNowUnavailableReason,
  describeCronSchedule,
  formatFireTime,
} from "@/utils/bindingDisplay";
import {
  linkedRunSessionKey,
  linkedSessionsForRun,
  mergeWorkflowRun,
  workedTaskIdsForRun,
  type LinkedRunSession,
} from "@/utils/workflowRunDetail";

import { TaskLink } from "./TaskLink";
import styles from "./WorkflowAgentDetail.module.css";

/**
 * Cadence presets for the schedule editor, mirroring CreateAgentModal's
 * CADENCE_OPTIONS. CUSTOM_CADENCE reveals a raw-cron input; the expression is
 * validated by a PATCH round-trip (the server's cron parser), never a
 * client-side reimplementation of cron grammar.
 */
const CADENCE_OPTIONS = [
  { value: "10m", label: "Every 10 minutes", cron: "*/10 * * * *" },
  { value: "hourly", label: "Hourly", cron: "0 * * * *" },
  { value: "daily", label: "Daily (09:00)", cron: "0 9 * * *" },
] as const;

const CUSTOM_CADENCE = "custom";

/** Preselect the cadence dropdown from a binding's stored cron, else "custom". */
function cadenceValueForCron(cron: string | undefined): string {
  const match = CADENCE_OPTIONS.find((c) => c.cron === (cron ?? "").trim());
  return match ? match.value : CUSTOM_CADENCE;
}

export interface WorkflowAgentDetailProps {
  workspaceId: string;
  binding: TriggerBinding;
  onSetEnabled: (bindingId: string, enabled: boolean) => Promise<void>;
  /**
   * Run the binding on demand (config-by-reference). The run wears the binding's
   * role via server-side provenance — this component no longer merges the
   * binding's run-input into a payload.
   */
  onRunBinding: (bindingId: string) => Promise<WorkflowRun>;
  /** Rename / reschedule the binding (PATCH). Resolves with the updated binding. */
  onUpdate: (
    bindingId: string,
    req: UpdateTriggerBindingRequest,
  ) => Promise<TriggerBinding>;
  /** Delete the binding and revoke its connector grants (Decision 6). */
  onDelete: (bindingId: string) => Promise<void>;
  /** Called after a successful delete so the shell can navigate back. */
  onDeleted: () => void;
}

type BindingHealth = ReturnType<typeof bindingHealth>;

/** CSS custom-property color per run status, for the run status dot. */
const RUN_STATUS_COLOR: Record<WorkflowRunStatus, string> = {
  queued: "var(--color-status-idle, #888)",
  running: "var(--color-warning, #d99700)",
  completed: "var(--color-success, #3aa76d)",
  failed: "var(--color-danger, #d14545)",
  needs_review: "var(--color-primary, #4477aa)",
  cancelled: "var(--color-text-tertiary, #888)",
  suspended_awaiting_event: "var(--color-warning, #d99700)",
};

function runStatusLabel(status: WorkflowRunStatus): string {
  return status
    .split("_")
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

function formatRunTime(iso: string | null | undefined): string {
  return formatFireTime(iso) || "—";
}

export interface WorkflowAgentDetailState {
  runs: WorkflowRun[];
  loading: boolean;
  error: string | null;
  stats: WorkflowAgentRunStats;
  selectedRunId: string | null;
  setSelectedRunId: (runId: string) => void;
  focusRun: WorkflowRun | null;
  showSource: boolean;
  setShowSource: (show: boolean) => void;
  busy: boolean;
  runNowUnavailableReason: string | null;
  handleRunNow: () => Promise<void>;
  handleToggleEnabled: () => Promise<void>;
  promptRoleName: string;
  displayName: string;
  avatarBg: string;
  avatarFg: string;
  cadence: string;
  nextFire: string;
  health: BindingHealth;
}

export interface UseWorkflowAgentDetailStateArgs {
  workspaceId: string;
  binding: TriggerBinding | undefined;
  onSetEnabled: WorkflowAgentDetailProps["onSetEnabled"];
  onRunBinding: WorkflowAgentDetailProps["onRunBinding"];
}

export function useWorkflowAgentDetailState({
  workspaceId,
  binding,
  onSetEnabled,
  onRunBinding,
}: UseWorkflowAgentDetailStateArgs): WorkflowAgentDetailState {
  const bindingId = binding?.binding_id ?? "";
  const { runs, loading, error, stats, refresh } = useWorkflowAgentDetail(
    workspaceId,
    bindingId,
  );
  const { showToast } = useToast();

  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [showSource, setShowSource] = useState(false);
  const [busy, setBusy] = useState(false);

  // A prompt agent carries its roleName in the binding's run-input
  // (source_config_ref). When present, the Info tab shows the ROLE prompt editor
  // (the primary edit surface) alongside the source editor (advanced).
  const promptRoleName = binding ? promptAgentRoleName(binding) : "";

  // Color stays keyed on the stable binding_id (a rename must not recolor the
  // avatar); the initials + title show the operator-entered Name (fix a).
  const displayName = binding ? bindingDisplayName(binding) : "";
  const avatarBg = getAvatarColor(binding?.binding_id ?? "agent");
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#171717";
  const cadence = binding ? bindingCadenceLabel(binding) : "";
  const nextFire = formatFireTime(binding?.next_fire_at);
  const health = binding
    ? bindingHealth(binding)
    : ({ state: "idle", label: "Idle", tooltip: "" } as BindingHealth);
  const runNowUnavailableReason = binding
    ? bindingRunNowUnavailableReason(binding)
    : null;

  // Focus the selected run, else the newest (runs are newest-first).
  const focusRun = useMemo<WorkflowRun | null>(() => {
    if (selectedRunId) {
      return runs.find((r) => r.run_id === selectedRunId) ?? null;
    }
    return runs[0] ?? null;
  }, [selectedRunId, runs]);

  useEffect(() => {
    setSelectedRunId(null);
    setShowSource(false);
    setBusy(false);
  }, [bindingId]);

  const handleRunNow = useCallback(async () => {
    if (!binding || runNowUnavailableReason) return;
    setBusy(true);
    try {
      // Config-by-reference: the binding-scoped run-now endpoint stamps the
      // binding on the run and the run resolves its own config (roleName/backend)
      // server-side from provenance. No client-side run-input merge — that whole
      // by-value transport is gone.
      const run = await onRunBinding(binding.binding_id);
      showToast(`Run queued for ${binding.binding_id}: ${run.run_id}`, {
        type: "success",
      });
      setSelectedRunId(run.run_id);
      await refresh();
    } catch (err) {
      showToast(`Run failed: ${(err as Error).message}`, { type: "error" });
    } finally {
      setBusy(false);
    }
  }, [binding, onRunBinding, refresh, runNowUnavailableReason, showToast]);

  const handleToggleEnabled = useCallback(async () => {
    if (!binding) return;
    setBusy(true);
    try {
      await onSetEnabled(binding.binding_id, !binding.enabled);
      showToast(binding.enabled ? "Agent disabled" : "Agent enabled", {
        type: "success",
      });
    } catch (err) {
      showToast(`Failed to update: ${(err as Error).message}`, {
        type: "error",
      });
    } finally {
      setBusy(false);
    }
  }, [binding, onSetEnabled, showToast]);

  return {
    runs,
    loading,
    error,
    stats,
    selectedRunId,
    setSelectedRunId,
    focusRun,
    showSource,
    setShowSource,
    busy,
    handleRunNow,
    handleToggleEnabled,
    promptRoleName,
    displayName,
    avatarBg,
    avatarFg,
    cadence,
    nextFire,
    health,
    runNowUnavailableReason,
  };
}

export function WorkflowAgentHeader({
  binding,
  detail,
}: {
  binding: TriggerBinding;
  detail: WorkflowAgentDetailState;
}): JSX.Element {
  return (
    <div className={styles.header}>
      <span
        className={styles.avatar}
        style={{ backgroundColor: detail.avatarBg, color: detail.avatarFg }}
        aria-hidden="true"
      >
        {getCompactAvatarInitials(detail.displayName)}
      </span>
      <div className={styles.headText}>
        <h1 className={styles.title}>{detail.displayName}</h1>
        <p className={styles.sub}>
          <span>Autonomous agent</span>
          <span aria-hidden="true">·</span>
          <span>{detail.cadence || bindingKindLabel(binding)}</span>
        </p>
      </div>
      <span
        className={styles.statusPill}
        data-enabled={binding.enabled}
        data-state={detail.health.state}
        title={detail.health.tooltip}
        data-testid="workflow-agent-status-pill"
      >
        {detail.health.label}
      </span>
    </div>
  );
}

export function WorkflowAgentActionBar({
  binding,
  detail,
}: {
  binding: TriggerBinding;
  detail: WorkflowAgentDetailState;
}): JSX.Element {
  return (
    <div className={styles.buttonBar}>
      {detail.runNowUnavailableReason ? (
        <span
          className={styles.runNowHint}
          title={detail.runNowUnavailableReason}
          data-testid="workflow-agent-run-now-hint"
        >
          {detail.runNowUnavailableReason}
        </span>
      ) : (
        <button
          type="button"
          className={styles.btnPrimary}
          onClick={() => void detail.handleRunNow()}
          disabled={detail.busy}
          data-testid="workflow-agent-run-now"
        >
          Run now
        </button>
      )}
      <button
        type="button"
        className={styles.btn}
        onClick={() => void detail.handleToggleEnabled()}
        disabled={detail.busy}
        data-testid="workflow-agent-toggle-enabled"
      >
        {binding.enabled ? "Disable" : "Enable"}
      </button>
      <button
        type="button"
        className={styles.btn}
        onClick={() => detail.setShowSource(true)}
        data-testid="workflow-agent-edit-config"
      >
        Edit configuration
      </button>
    </div>
  );
}

export function WorkflowAgentRunsPane({
  workspaceId,
  binding,
  detail,
  active = true,
  onOpenTask,
}: {
  workspaceId: string;
  binding: TriggerBinding;
  detail: WorkflowAgentDetailState;
  active?: boolean;
  onOpenTask?: ((taskId: string) => void) | undefined;
}): JSX.Element {
  return (
    <RunsTab
      workspaceId={workspaceId}
      runs={detail.runs}
      loading={detail.loading}
      error={detail.error}
      enabled={binding.enabled}
      nextFire={detail.nextFire}
      cadence={detail.cadence}
      focusRun={detail.focusRun}
      active={active}
      selectedRunId={detail.selectedRunId}
      onSelectRun={detail.setSelectedRunId}
      onOpenTask={onOpenTask}
    />
  );
}

/**
 * Record-scoped run history. The durable agent endpoint aggregates runs from
 * every trigger binding attached to the AgentService, while binding-id routes
 * continue to use WorkflowAgentRunsPane for trigger-specific inspection.
 */
export function AgentRecordRunsPane({
  workspaceId,
  record,
  bindings,
  active = true,
  onOpenTask,
}: {
  workspaceId: string;
  record: AgentRecordSummary;
  bindings: TriggerBinding[];
  active?: boolean;
  onOpenTask?: ((taskId: string) => void) | undefined;
}): JSX.Element {
  const { runs, isLoading, error } = useAgentHistory(
    workspaceId,
    record.id,
    active,
  );
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  useEffect(() => setSelectedRunId(null), [record.id, workspaceId]);
  const selectedRun = runs.find((run) => run.run_id === selectedRunId) ?? null;
  const focusRun = selectedRun ?? runs[0] ?? null;
  const onlyBinding = bindings.length === 1 ? bindings[0] : undefined;
  const cadence =
    bindings.length === 0
      ? "a trigger to be configured"
      : onlyBinding
        ? bindingCadenceLabel(onlyBinding)
        : `one of ${bindings.length} configured triggers`;

  return (
    <RunsTab
      workspaceId={workspaceId}
      runs={runs}
      loading={isLoading}
      error={error?.message ?? null}
      enabled={record.enabled}
      nextFire={formatFireTime(
        record.next_fire_at ?? onlyBinding?.next_fire_at,
      )}
      cadence={cadence}
      focusRun={focusRun}
      active={active}
      selectedRunId={selectedRun?.run_id ?? focusRun?.run_id ?? null}
      onSelectRun={setSelectedRunId}
      onOpenTask={onOpenTask}
    />
  );
}

export function WorkflowAgentInfoPane({
  workspaceId,
  binding,
  detail,
  onUpdate,
  onDelete,
  onDeleted,
}: {
  workspaceId: string;
  binding: TriggerBinding;
  detail: WorkflowAgentDetailState;
  onUpdate: WorkflowAgentDetailProps["onUpdate"];
  onDelete: WorkflowAgentDetailProps["onDelete"];
  onDeleted: WorkflowAgentDetailProps["onDeleted"];
}): JSX.Element {
  return (
    <InfoTab
      workspaceId={workspaceId}
      binding={binding}
      roleName={detail.promptRoleName}
      driverId={binding.driver_id}
      activeVersionId={binding.driver_version_id}
      nextFire={detail.nextFire}
      stats={detail.stats}
      onEditConfig={() => detail.setShowSource(true)}
      onUpdate={onUpdate}
      onDelete={onDelete}
      onDeleted={onDeleted}
    />
  );
}

export function WorkflowAgentSourceModal({
  workspaceId,
  binding,
  isOpen,
  onClose,
}: {
  workspaceId: string;
  binding: TriggerBinding | undefined;
  isOpen: boolean;
  onClose: () => void;
}): JSX.Element | null {
  if (!binding) return null;
  return (
    <WorkflowSourceModal
      isOpen={isOpen}
      workspaceId={workspaceId}
      workflowName={binding.driver_id}
      onClose={onClose}
    />
  );
}
function RunsTab({
  workspaceId,
  runs,
  loading,
  error,
  enabled,
  nextFire,
  cadence,
  focusRun,
  active,
  selectedRunId,
  onSelectRun,
  onOpenTask,
}: {
  workspaceId: string;
  runs: WorkflowRun[];
  loading: boolean;
  error: string | null;
  enabled: boolean;
  nextFire: string;
  cadence: string;
  focusRun: WorkflowRun | null;
  active: boolean;
  selectedRunId: string | null;
  onSelectRun: (runId: string) => void;
  onOpenTask?: ((taskId: string) => void) | undefined;
}): JSX.Element {
  return (
    <div className={styles.scroll}>
      <div className={styles.idleBanner} data-testid="workflow-agent-idle">
        {enabled
          ? nextFire
            ? `Next fire at ${nextFire}`
            : `Enabled — waiting for ${cadence || "its trigger"}`
          : "Disabled — this agent will not run until enabled"}
      </div>

      {focusRun && active ? (
        <RunDetailCard
          workspaceId={workspaceId}
          run={focusRun}
          onOpenTask={onOpenTask}
        />
      ) : null}

      <section className={styles.card}>
        <h2 className={styles.cardLabel}>Run history</h2>
        {error ? (
          <div className={styles.errorText} role="alert">
            {error}
          </div>
        ) : loading && runs.length === 0 ? (
          <div className={styles.emptyText}>Loading runs…</div>
        ) : runs.length === 0 ? (
          <div
            className={styles.emptyText}
            data-testid="workflow-agent-no-runs"
          >
            No runs yet.{" "}
            {enabled
              ? nextFire
                ? `The next run is scheduled for ${nextFire}.`
                : "This agent runs when its trigger fires."
              : "Enable this agent to schedule runs."}
          </div>
        ) : (
          <ul className={styles.runList} data-testid="workflow-agent-run-list">
            {runs.map((run) => {
              const live = !isTerminalWorkflowRunStatus(run.status);
              // formatFireTime treats Go zero times as absent, so a queued
              // run (started_at = 0001-01-01…) honestly reads "Created …".
              const started = formatFireTime(run.started_at);
              const finished = formatFireTime(run.finished_at);
              return (
                <li
                  key={run.run_id}
                  className={styles.runRow}
                  data-selected={
                    (selectedRunId
                      ? run.run_id === selectedRunId
                      : run.run_id === focusRun?.run_id) || undefined
                  }
                >
                  <button
                    type="button"
                    className={styles.runRowSelect}
                    onClick={() => onSelectRun(run.run_id)}
                  >
                    <span
                      className={styles.runDot}
                      style={{ background: RUN_STATUS_COLOR[run.status] }}
                      data-live={live || undefined}
                      aria-hidden="true"
                    />
                    <span className={styles.runMain}>
                      <span className={styles.runStatus}>
                        {runStatusLabel(run.status)}
                        {live ? (
                          <span className={styles.liveTag}>live</span>
                        ) : null}
                      </span>
                      <span className={styles.runMeta}>
                        {started
                          ? `Started ${started}`
                          : `Created ${formatRunTime(run.created_at)}`}
                        {finished ? ` · Finished ${finished}` : ""}
                      </span>
                      {run.summary ? (
                        <span className={styles.runSummary}>{run.summary}</span>
                      ) : null}
                      {run.error_class ? (
                        <span className={styles.runErr}>{run.error_class}</span>
                      ) : null}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}

export function RunDetailCard({
  workspaceId,
  run,
  onOpenTask,
}: {
  workspaceId: string;
  run: WorkflowRun;
  onOpenTask?: ((taskId: string) => void) | undefined;
}): JSX.Element {
  const [detailRun, setDetailRun] = useState(run);
  const [detailLoadError, setDetailLoadError] = useState<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailReload, setDetailReload] = useState(0);
  const [selectedLinkKey, setSelectedLinkKey] = useState("");

  useEffect(() => {
    setDetailRun((previous) => mergeWorkflowRun(previous, run));
  }, [run]);

  useEffect(() => {
    if (!workspaceId || !run.run_id) return;
    let cancelled = false;
    setDetailLoading(true);
    setDetailLoadError(null);
    void getWorkflowRun(workspaceId, run.run_id)
      .then((fresh) => {
        if (!cancelled) {
          setDetailRun((previous) => mergeWorkflowRun(previous, fresh));
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setDetailLoadError(
            err instanceof Error ? err.message : "Failed to load run detail",
          );
        }
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [workspaceId, run.run_id, run.status, run.updated_at, detailReload]);

  // Avoid one stale render when navigating directly between run ids: the
  // detail state catches up in the effect above, while this frame uses the new
  // history row as its safe baseline.
  const displayedRun = detailRun.run_id === run.run_id ? detailRun : run;
  const live = !isTerminalWorkflowRunStatus(displayedRun.status);
  const linkedSessions = useMemo(
    () => linkedSessionsForRun(displayedRun),
    [displayedRun],
  );
  const taskIds = useMemo(
    () => workedTaskIdsForRun(displayedRun),
    [displayedRun],
  );
  const linkedSession = useMemo(
    () =>
      linkedSessions.find(
        (candidate) => linkedRunSessionKey(candidate) === selectedLinkKey,
      ) ??
      linkedSessions[0] ??
      null,
    [linkedSessions, selectedLinkKey],
  );
  const activeLinkKey = linkedSession ? linkedRunSessionKey(linkedSession) : "";
  useEffect(() => {
    if (activeLinkKey !== selectedLinkKey) {
      setSelectedLinkKey(activeLinkKey);
    }
  }, [activeLinkKey, selectedLinkKey]);
  const {
    sessions,
    isLoading: sessionsLoading,
    error: sessionsError,
  } = useTaskSessions(linkedSession?.taskId || null);
  const resolvedSession = useMemo(() => {
    if (!linkedSession?.taskId || !linkedSession.sessionId) {
      return { session: null, isFallback: false };
    }
    const persisted = sessions.find(
      (candidate) => candidate.session_id === linkedSession.sessionId,
    );
    return persisted
      ? { session: persisted, isFallback: false }
      : {
          session: fallbackSessionFromRun(displayedRun, linkedSession),
          isFallback: true,
        };
  }, [displayedRun, linkedSession, sessions]);
  const { session, isFallback: isFallbackSession } = resolvedSession;
  const terminalWithoutChild =
    isTerminalWorkflowRunStatus(displayedRun.status) &&
    linkedSessions.length === 0;

  // Zero-time (unset) instants format to "" and render as "—" / are omitted.
  const started = formatFireTime(displayedRun.started_at);
  const heartbeat = formatFireTime(displayedRun.last_heartbeat);
  return (
    <section className={styles.card} data-testid="workflow-agent-run-detail">
      <div className={styles.runDetailHead}>
        <span
          className={styles.runDot}
          style={{ background: RUN_STATUS_COLOR[displayedRun.status] }}
          data-live={live || undefined}
          aria-hidden="true"
        />
        <span className={styles.runDetailStatus}>
          {runStatusLabel(displayedRun.status)}
        </span>
        {live ? <span className={styles.liveTag}>live</span> : null}
        <code className={styles.runId}>{displayedRun.run_id}</code>
      </div>
      <dl className={styles.detailGrid}>
        {started ? (
          <div>
            <dt>Started</dt>
            <dd>{started}</dd>
          </div>
        ) : (
          <div>
            <dt>Created</dt>
            <dd>{formatRunTime(displayedRun.created_at)}</dd>
          </div>
        )}
        <div>
          <dt>Finished</dt>
          <dd>{formatRunTime(displayedRun.finished_at)}</dd>
        </div>
        {heartbeat && live ? (
          <div>
            <dt>Heartbeat</dt>
            <dd>{heartbeat}</dd>
          </div>
        ) : null}
        {displayedRun.error_class ? (
          <div>
            <dt>Error</dt>
            <dd className={styles.runErr}>{displayedRun.error_class}</dd>
          </div>
        ) : null}
        {taskIds.length > 0 ? (
          <div>
            <dt>{taskIds.length === 1 ? "Task" : "Tasks"}</dt>
            <dd className={styles.detailTaskLinks}>
              {taskIds.map((taskId) => (
                <TaskLink
                  key={taskId}
                  workspaceId={workspaceId}
                  taskId={taskId}
                  className={styles.taskLink}
                  onOpenTask={onOpenTask}
                />
              ))}
            </dd>
          </div>
        ) : null}
      </dl>
      {displayedRun.summary ? (
        <p className={styles.detailSummary}>{displayedRun.summary}</p>
      ) : null}

      {detailLoadError ? (
        <div
          className={styles.detailLoadError}
          role="alert"
          data-testid="workflow-agent-run-detail-error"
        >
          <span>Full run detail unavailable: {detailLoadError}</span>
          <button
            type="button"
            className={styles.btn}
            disabled={detailLoading}
            onClick={() => setDetailReload((attempt) => attempt + 1)}
            data-testid="workflow-agent-run-detail-retry"
          >
            {detailLoading ? "Retrying…" : "Retry"}
          </button>
        </div>
      ) : null}

      {linkedSessions.length > 1 ? (
        <div
          className={styles.sessionSelector}
          role="tablist"
          aria-label="Task sessions"
          data-testid="workflow-agent-session-selector"
        >
          {linkedSessions.map((link, index) => {
            const key = linkedRunSessionKey(link);
            const selected = key === activeLinkKey;
            return (
              <button
                type="button"
                role="tab"
                aria-selected={selected}
                className={styles.sessionTab}
                key={key}
                onClick={() => setSelectedLinkKey(key)}
                data-testid={`workflow-agent-session-${key}`}
              >
                {link.taskId || link.taskRunId || `Session ${index + 1}`}
              </button>
            );
          })}
        </div>
      ) : null}

      <div className={styles.runTranscript}>
        {linkedSession?.taskId && session ? (
          <SessionRunDetail
            taskId={linkedSession.taskId}
            session={session}
            retryTranscriptUnavailable={isFallbackSession && !live}
            exitCodeKnown={!isFallbackSession}
            telemetryKnown={!isFallbackSession}
          />
        ) : linkedSession?.taskRunId ? (
          <div className={styles.transcriptEmpty}>
            Transcript link pending for task run{" "}
            <code>{linkedSession.taskRunId}</code>.
          </div>
        ) : sessionsError ? (
          <div className={styles.transcriptEmpty}>
            Failed to load transcript metadata: {sessionsError.message}
          </div>
        ) : sessionsLoading ? (
          <div className={styles.transcriptEmpty}>Loading transcript…</div>
        ) : (
          <div className={styles.transcriptEmpty}>
            {terminalWithoutChild
              ? "This workflow run did not create a child task or invoke a model, so there is no transcript. A completed run usually means no eligible task was available."
              : "No task-run transcript linked to this run yet."}
          </div>
        )}
      </div>
    </section>
  );
}

function fallbackSessionFromRun(
  run: WorkflowRun,
  link: LinkedRunSession,
): SessionRecord {
  const sessionStatus = workflowRunToSessionStatus(run.status);
  const live = !isTerminalWorkflowRunStatus(run.status);
  const session: SessionRecord = {
    session_id: link.sessionId,
    task_id: link.taskId,
    agent_name: run.driver_id,
    backend: run.output?.backend ?? "flue",
    started_at: run.started_at || run.created_at,
    ended_at: run.finished_at ?? null,
    duration_s: 0,
    status: sessionStatus,
    // Required presentation placeholders; SessionRunDetail hides them until
    // the canonical task session supplies exit and usage evidence.
    exit_code: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: [],
    attempt_num: 0,
    is_active: live,
    has_transcript: true,
    has_diff: false,
  };
  if (run.error_class) {
    session.error_class = run.error_class;
    session.last_error = run.error_class;
  }
  return session;
}

function workflowRunToSessionStatus(status: WorkflowRunStatus): string {
  switch (status) {
    case "failed":
      return "failed";
    case "cancelled":
      return "aborted";
    case "queued":
    case "running":
    case "suspended_awaiting_event":
      return "running";
    default:
      return "completed";
  }
}

function InfoTab({
  workspaceId,
  binding,
  roleName,
  driverId,
  activeVersionId,
  nextFire,
  stats,
  onEditConfig,
  onUpdate,
  onDelete,
  onDeleted,
}: {
  workspaceId: string;
  binding: TriggerBinding;
  roleName: string;
  driverId: string;
  activeVersionId: string;
  nextFire: string;
  stats: { total: number; completed: number; failed: number; running: number };
  onEditConfig: () => void;
  onUpdate: (
    bindingId: string,
    req: UpdateTriggerBindingRequest,
  ) => Promise<TriggerBinding>;
  onDelete: (bindingId: string) => Promise<void>;
  onDeleted: () => void;
}): JSX.Element {
  const isCron = binding.source_kind === "cron";
  return (
    <div className={styles.scroll}>
      {roleName ? (
        <RolePromptCard workspaceId={workspaceId} roleName={roleName} />
      ) : null}

      <section className={styles.card}>
        <h2 className={styles.cardLabel}>Configuration</h2>
        <dl className={styles.configGrid}>
          <div>
            <dt>Driver</dt>
            <dd>{driverId}</dd>
          </div>
          <div>
            <dt>Active version</dt>
            <dd>
              <code>{activeVersionId || "—"}</code>
            </dd>
          </div>
          <div>
            <dt>Trigger</dt>
            <dd>{bindingKindLabel(binding)}</dd>
          </div>
          {roleName ? (
            <div>
              <dt>Role</dt>
              <dd>
                <code>{roleName}</code>
              </dd>
            </div>
          ) : null}
          {isCron ? (
            <>
              <div>
                <dt>Schedule</dt>
                <dd>
                  {describeCronSchedule(binding.schedule)}
                  {binding.schedule ? (
                    <code className={styles.inlineCode}>
                      {binding.schedule}
                    </code>
                  ) : null}
                </dd>
              </div>
              <div>
                <dt>Timezone</dt>
                <dd>{binding.schedule_timezone || "UTC"}</dd>
              </div>
              <div>
                <dt>Next fire</dt>
                <dd>{nextFire || (binding.enabled ? "—" : "Disabled")}</dd>
              </div>
            </>
          ) : (
            <div>
              <dt>Event patterns</dt>
              <dd>{(binding.event_type_patterns ?? []).join(", ") || "—"}</dd>
            </div>
          )}
          <div>
            <dt>State</dt>
            <dd>{binding.enabled ? "Enabled" : "Disabled"}</dd>
          </div>
        </dl>
      </section>

      <section className={styles.card}>
        <h2 className={styles.cardLabel}>Run stats</h2>
        <dl className={styles.statGrid}>
          <div className={styles.statCard}>
            <dt className={styles.statLabel}>Completed</dt>
            <dd className={styles.statValue} data-tone="success">
              {stats.completed}
            </dd>
          </div>
          <div className={styles.statCard}>
            <dt className={styles.statLabel}>Failed</dt>
            <dd className={styles.statValue} data-tone="danger">
              {stats.failed}
            </dd>
          </div>
          <div className={styles.statCard}>
            <dt className={styles.statLabel}>Running</dt>
            <dd className={styles.statValue} data-tone="warning">
              {stats.running}
            </dd>
          </div>
          <div className={styles.statCard}>
            <dt className={styles.statLabel}>Total shown</dt>
            <dd className={styles.statValue} data-tone="info">
              {stats.total}
            </dd>
          </div>
        </dl>
      </section>

      <ManageCard
        binding={binding}
        isCron={isCron}
        onEditConfig={onEditConfig}
        onUpdate={onUpdate}
        onDelete={onDelete}
        onDeleted={onDeleted}
      />
    </div>
  );
}

/**
 * ManageCard is the Info-tab editor: rename, reschedule (cron cadence preset or
 * a custom cron validated by the PATCH round-trip), open the source editor, and
 * delete (with a confirm step that spells out the connector-grant revocation).
 * It seeds its edit state from the binding and re-seeds when the binding's
 * identity/name/schedule change (e.g. after a save re-pulls the list).
 */
export function ManageCard({
  binding,
  isCron,
  onEditConfig,
  onUpdate,
  onDelete,
  onDeleted,
}: {
  binding: TriggerBinding;
  isCron: boolean;
  onEditConfig: () => void;
  onUpdate: (
    bindingId: string,
    req: UpdateTriggerBindingRequest,
  ) => Promise<TriggerBinding>;
  onDelete: (bindingId: string) => Promise<void>;
  onDeleted: () => void;
}): JSX.Element {
  const { showToast } = useToast();
  const [name, setName] = useState(binding.name);
  const [cadence, setCadence] = useState(cadenceValueForCron(binding.schedule));
  const [customCron, setCustomCron] = useState(binding.schedule ?? "");
  const [busy, setBusy] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  // Re-seed the editor when the underlying binding changes (post-save re-pull,
  // or navigating between bindings without unmounting).
  useEffect(() => {
    setName(binding.name);
    setCadence(cadenceValueForCron(binding.schedule));
    setCustomCron(binding.schedule ?? "");
    setEditError(null);
    setConfirmingDelete(false);
  }, [binding.binding_id, binding.name, binding.schedule]);

  const resolvedCron =
    cadence === CUSTOM_CADENCE
      ? customCron.trim()
      : (CADENCE_OPTIONS.find((c) => c.value === cadence)?.cron ?? "");

  const nameChanged = name.trim() !== binding.name && name.trim() !== "";
  const scheduleChanged =
    isCron && resolvedCron !== "" && resolvedCron !== (binding.schedule ?? "");

  const handleSaveName = useCallback(async () => {
    setEditError(null);
    if (!nameChanged) return;
    setBusy(true);
    try {
      await onUpdate(binding.binding_id, { name: name.trim() });
      showToast("Agent name updated", { type: "success" });
    } catch (err) {
      setEditError((err as Error).message || "Failed to update agent");
    } finally {
      setBusy(false);
    }
  }, [binding.binding_id, name, nameChanged, onUpdate, showToast]);

  const handleSaveSchedule = useCallback(async () => {
    setEditError(null);
    if (!scheduleChanged) return;
    setBusy(true);
    try {
      await onUpdate(binding.binding_id, { schedule: resolvedCron });
      showToast("Agent cadence updated", { type: "success" });
    } catch (err) {
      // A bad custom cron surfaces here as the server's parse error (400) —
      // we never re-implement cron validation client-side.
      setEditError((err as Error).message || "Failed to update agent cadence");
    } finally {
      setBusy(false);
    }
  }, [binding.binding_id, resolvedCron, scheduleChanged, onUpdate, showToast]);

  const handleDelete = useCallback(async () => {
    setBusy(true);
    setEditError(null);
    try {
      await onDelete(binding.binding_id);
      showToast(`Deleted ${binding.binding_id}`, { type: "success" });
      onDeleted();
    } catch (err) {
      setEditError((err as Error).message || "Failed to delete agent");
      setBusy(false);
    }
  }, [binding.binding_id, onDelete, onDeleted, showToast]);

  return (
    <section className={styles.card} data-testid="workflow-agent-manage">
      <h2 className={styles.cardLabel}>Manage</h2>
      <div className={styles.manageGrid}>
        <label className={styles.manageField}>
          <span className={styles.manageLabel}>Name</span>
          <input
            className={styles.manageInput}
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={busy}
            data-testid="workflow-agent-edit-name"
          />
        </label>

        {isCron && (
          <label className={styles.manageField}>
            <span className={styles.manageLabel}>Cadence</span>
            <select
              className={styles.manageInput}
              value={cadence}
              onChange={(e) => setCadence(e.target.value)}
              disabled={busy}
              data-testid="workflow-agent-edit-cadence"
            >
              {CADENCE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
              <option value={CUSTOM_CADENCE}>Custom cron…</option>
            </select>
          </label>
        )}

        {isCron && cadence === CUSTOM_CADENCE && (
          <label className={styles.manageField}>
            <span className={styles.manageLabel}>Cron expression</span>
            <input
              className={styles.manageInput}
              value={customCron}
              onChange={(e) => setCustomCron(e.target.value)}
              placeholder="*/15 * * * *"
              spellCheck={false}
              disabled={busy}
              data-testid="workflow-agent-edit-cron"
            />
          </label>
        )}
      </div>

      {editError && (
        <div
          className={styles.errorText}
          role="alert"
          data-testid="workflow-agent-edit-error"
        >
          {editError}
        </div>
      )}

      <div className={styles.manageActions}>
        <button
          type="button"
          className={styles.btnPrimary}
          onClick={handleSaveName}
          disabled={busy || !nameChanged}
          data-testid="workflow-agent-save-name"
        >
          Save name
        </button>
        {isCron && (
          <button
            type="button"
            className={styles.btnPrimary}
            onClick={handleSaveSchedule}
            disabled={busy || !scheduleChanged}
            data-testid="workflow-agent-save-schedule"
          >
            Save cadence
          </button>
        )}
        <button
          type="button"
          className={styles.btn}
          onClick={onEditConfig}
          data-testid="workflow-agent-info-edit-config"
        >
          Edit source
        </button>
      </div>

      <div className={styles.dangerZone}>
        {confirmingDelete ? (
          <div
            className={styles.confirmRow}
            data-testid="workflow-agent-delete-confirm"
          >
            <span className={styles.confirmText}>
              Delete this agent? This revokes its connector grants and cannot be
              undone.
            </span>
            <div className={styles.confirmActions}>
              <button
                type="button"
                className={styles.btnDanger}
                onClick={handleDelete}
                disabled={busy}
                data-testid="workflow-agent-delete-confirm-yes"
              >
                Delete
              </button>
              <button
                type="button"
                className={styles.btn}
                onClick={() => setConfirmingDelete(false)}
                disabled={busy}
                data-testid="workflow-agent-delete-cancel"
              >
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <button
            type="button"
            className={styles.btnDangerOutline}
            onClick={() => setConfirmingDelete(true)}
            disabled={busy}
            data-testid="workflow-agent-delete"
          >
            Delete agent
          </button>
        )}
      </div>
    </section>
  );
}

/**
 * RolePromptCard is a prompt agent's PRIMARY edit surface: the ROLE's prompt in
 * a textarea, loaded from GET /roles/{name} and saved via PATCH /roles/{name}.
 * Because the prompt lives on the Role (behavior config), one edit here updates
 * EVERY agent wearing the role — the point of the prompt-as-data model. The
 * source editor (WorkflowSourceModal, "Edit source"/"Edit configuration") stays
 * available as the secondary/advanced path for the driver's TS.
 */
function RolePromptCard({
  workspaceId,
  roleName,
}: {
  workspaceId: string;
  roleName: string;
}): JSX.Element {
  const { showToast } = useToast();
  const [prompt, setPrompt] = useState("");
  // The last-loaded/saved body, to detect a dirty edit; null until first load.
  const [baseline, setBaseline] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getWorkspaceRole(workspaceId, roleName)
      .then((res) => {
        if (cancelled) return;
        setPrompt(res.prompt ?? "");
        setBaseline(res.prompt ?? "");
      })
      .catch((err) => {
        if (!cancelled)
          setError((err as Error).message || "Failed to load role");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [workspaceId, roleName]);

  const dirty = baseline !== null && prompt !== baseline;

  const handleSave = useCallback(async () => {
    setSaving(true);
    setError(null);
    try {
      const res = await updateWorkspaceRole(workspaceId, roleName, { prompt });
      setBaseline(res.prompt ?? prompt);
      showToast(`Saved prompt for role ${roleName}`, { type: "success" });
    } catch (err) {
      setError((err as Error).message || "Failed to save prompt");
    } finally {
      setSaving(false);
    }
  }, [workspaceId, roleName, prompt, showToast]);

  return (
    <section className={styles.card} data-testid="workflow-agent-role-prompt">
      <h2 className={styles.cardLabel}>
        Role prompt — shared by every agent wearing “{roleName}”
      </h2>
      {loading ? (
        <p className={styles.emptyText}>Loading role prompt…</p>
      ) : (
        <>
          <textarea
            className={styles.manageInput}
            style={{
              minHeight: "12rem",
              fontFamily: "var(--font-mono, monospace)",
            }}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            spellCheck={false}
            disabled={saving}
            data-testid="workflow-agent-role-prompt-textarea"
          />
          {error && (
            <div
              className={styles.errorText}
              role="alert"
              data-testid="workflow-agent-role-prompt-error"
            >
              {error}
            </div>
          )}
          <div className={styles.manageActions}>
            <button
              type="button"
              className={styles.btnPrimary}
              onClick={handleSave}
              disabled={!dirty || saving}
              data-testid="workflow-agent-role-prompt-save"
            >
              {saving ? "Saving…" : "Save prompt"}
            </button>
          </div>
        </>
      )}
    </section>
  );
}
