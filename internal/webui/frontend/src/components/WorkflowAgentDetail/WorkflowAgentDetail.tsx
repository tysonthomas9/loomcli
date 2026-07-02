/**
 * WorkflowAgentDetail — detail view for a workflow-plane agent (trigger
 * binding), rendered inside the same AgentsPage shell that role agents use.
 *
 * Capability-based content (a binding has runs + config, but no worktree):
 *  - Runs tab (terminal-tab equivalent): run history from listWorkflowRuns with
 *    live status via the per-run SSE stream; idle state shows the next fire.
 *  - Info tab: driver + active version + trigger cadence + run stats, plus an
 *    "Edit configuration" button opening WorkflowSourceModal for the driver.
 *  - Button bar: Run now, Enable/Disable.
 * Git / Diff / Files are intentionally absent — a binding has no worktree.
 */

import { useCallback, useMemo, useState } from "react";

import { WorkflowSourceModal } from "@/components/WorkflowSourceModal";
import { useWorkflowAgentDetail } from "@/hooks/workflows/useWorkflowAgentDetail";
import { useToast } from "@/hooks/ui/useToast";
import {
  isTerminalWorkflowRunStatus,
  type TriggerBinding,
  type WorkflowRun,
  type WorkflowRunStatus,
} from "@/api";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import {
  bindingCadenceLabel,
  bindingKindLabel,
  describeCronSchedule,
  formatFireTime,
} from "@/utils/bindingDisplay";

import styles from "./WorkflowAgentDetail.module.css";

export interface WorkflowAgentDetailProps {
  workspaceId: string;
  binding: TriggerBinding;
  onSetEnabled: (bindingId: string, enabled: boolean) => Promise<void>;
  onRunWorkflow: (name: string, payload: unknown) => Promise<WorkflowRun>;
}

type DetailTab = "runs" | "info";

/** CSS custom-property color per run status, for the run status dot. */
const RUN_STATUS_COLOR: Record<WorkflowRunStatus, string> = {
  queued: "var(--color-status-idle, #888)",
  running: "var(--color-warning, #d99700)",
  completed: "var(--color-success, #3aa76d)",
  failed: "var(--color-danger, #d14545)",
  needs_review: "var(--color-primary, #4477aa)",
  cancelled: "var(--color-text-tertiary, #888)",
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

export function WorkflowAgentDetail({
  workspaceId,
  binding,
  onSetEnabled,
  onRunWorkflow,
}: WorkflowAgentDetailProps): JSX.Element {
  const { runs, loading, error, driverId, activeVersionId, stats, refresh } =
    useWorkflowAgentDetail(workspaceId, binding.driver_id);
  const { showToast } = useToast();

  const [tab, setTab] = useState<DetailTab>("runs");
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [showSource, setShowSource] = useState(false);
  const [busy, setBusy] = useState(false);

  const avatarBg = getAvatarColor(binding.binding_id);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#171717";
  const cadence = bindingCadenceLabel(binding);
  const nextFire = formatFireTime(binding.next_fire_at);

  // Focus the selected run, else the newest (runs are newest-first).
  const focusRun = useMemo<WorkflowRun | null>(() => {
    if (selectedRunId) {
      return runs.find((r) => r.run_id === selectedRunId) ?? null;
    }
    return runs[0] ?? null;
  }, [selectedRunId, runs]);

  const handleRunNow = useCallback(async () => {
    setBusy(true);
    try {
      const run = await onRunWorkflow(binding.driver_id, {});
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
  }, [binding.driver_id, binding.binding_id, onRunWorkflow, refresh, showToast]);

  const handleToggleEnabled = useCallback(async () => {
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
  }, [binding.binding_id, binding.enabled, onSetEnabled, showToast]);

  return (
    <div className={styles.root} data-testid="workflow-agent-detail">
      <div className={styles.header}>
        <span
          className={styles.avatar}
          style={{ backgroundColor: avatarBg, color: avatarFg }}
          aria-hidden="true"
        >
          {getCompactAvatarInitials(binding.binding_id)}
        </span>
        <div className={styles.headText}>
          <h1 className={styles.title}>{binding.binding_id}</h1>
          <p className={styles.sub}>
            <span>Autonomous agent</span>
            <span aria-hidden="true">·</span>
            <span>{cadence || bindingKindLabel(binding)}</span>
          </p>
        </div>
        <span
          className={styles.statusPill}
          data-enabled={binding.enabled}
          title={
            binding.enabled && nextFire ? `Next fire ${nextFire}` : undefined
          }
        >
          {binding.enabled ? "Enabled" : "Disabled"}
        </span>
      </div>

      <div className={styles.buttonBar}>
        <button
          type="button"
          className={styles.btnPrimary}
          onClick={handleRunNow}
          disabled={busy}
          data-testid="workflow-agent-run-now"
        >
          Run now
        </button>
        <button
          type="button"
          className={styles.btn}
          onClick={handleToggleEnabled}
          disabled={busy}
          data-testid="workflow-agent-toggle-enabled"
        >
          {binding.enabled ? "Disable" : "Enable"}
        </button>
        <button
          type="button"
          className={styles.btn}
          onClick={() => setShowSource(true)}
          data-testid="workflow-agent-edit-config"
        >
          Edit configuration
        </button>
      </div>

      <div className={styles.tabStrip} role="tablist">
        <button
          type="button"
          role="tab"
          className={styles.tab}
          data-active={tab === "runs" || undefined}
          aria-selected={tab === "runs"}
          onClick={() => setTab("runs")}
          data-testid="workflow-agent-tab-runs"
        >
          Runs
        </button>
        <button
          type="button"
          role="tab"
          className={styles.tab}
          data-active={tab === "info" || undefined}
          aria-selected={tab === "info"}
          onClick={() => setTab("info")}
          data-testid="workflow-agent-tab-info"
        >
          Info
        </button>
      </div>

      <div className={styles.body}>
        {tab === "runs" ? (
          <RunsTab
            runs={runs}
            loading={loading}
            error={error}
            enabled={binding.enabled}
            nextFire={nextFire}
            cadence={cadence}
            focusRun={focusRun}
            selectedRunId={selectedRunId}
            onSelectRun={setSelectedRunId}
          />
        ) : (
          <InfoTab
            binding={binding}
            driverId={driverId || binding.driver_id}
            activeVersionId={activeVersionId || binding.driver_version_id}
            nextFire={nextFire}
            stats={stats}
            onEditConfig={() => setShowSource(true)}
          />
        )}
      </div>

      <WorkflowSourceModal
        isOpen={showSource}
        workspaceId={workspaceId}
        workflowName={binding.driver_id}
        onClose={() => setShowSource(false)}
      />
    </div>
  );
}

function RunsTab({
  runs,
  loading,
  error,
  enabled,
  nextFire,
  cadence,
  focusRun,
  selectedRunId,
  onSelectRun,
}: {
  runs: WorkflowRun[];
  loading: boolean;
  error: string | null;
  enabled: boolean;
  nextFire: string;
  cadence: string;
  focusRun: WorkflowRun | null;
  selectedRunId: string | null;
  onSelectRun: (runId: string) => void;
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

      {focusRun ? (
        <RunDetailCard run={focusRun} />
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
          <div className={styles.emptyText} data-testid="workflow-agent-no-runs">
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
                <li key={run.run_id}>
                  <button
                    type="button"
                    className={styles.runRow}
                    data-selected={
                      (selectedRunId
                        ? run.run_id === selectedRunId
                        : run.run_id === focusRun?.run_id) || undefined
                    }
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

function RunDetailCard({ run }: { run: WorkflowRun }): JSX.Element {
  const live = !isTerminalWorkflowRunStatus(run.status);
  // Zero-time (unset) instants format to "" and render as "—" / are omitted.
  const started = formatFireTime(run.started_at);
  const heartbeat = formatFireTime(run.last_heartbeat);
  return (
    <section className={styles.card} data-testid="workflow-agent-run-detail">
      <div className={styles.runDetailHead}>
        <span
          className={styles.runDot}
          style={{ background: RUN_STATUS_COLOR[run.status] }}
          data-live={live || undefined}
          aria-hidden="true"
        />
        <span className={styles.runDetailStatus}>
          {runStatusLabel(run.status)}
        </span>
        {live ? <span className={styles.liveTag}>live</span> : null}
        <code className={styles.runId}>{run.run_id}</code>
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
            <dd>{formatRunTime(run.created_at)}</dd>
          </div>
        )}
        <div>
          <dt>Finished</dt>
          <dd>{formatRunTime(run.finished_at)}</dd>
        </div>
        {heartbeat && live ? (
          <div>
            <dt>Heartbeat</dt>
            <dd>{heartbeat}</dd>
          </div>
        ) : null}
        {run.error_class ? (
          <div>
            <dt>Error</dt>
            <dd className={styles.runErr}>{run.error_class}</dd>
          </div>
        ) : null}
      </dl>
      {run.summary ? <p className={styles.detailSummary}>{run.summary}</p> : null}
    </section>
  );
}

function InfoTab({
  binding,
  driverId,
  activeVersionId,
  nextFire,
  stats,
  onEditConfig,
}: {
  binding: TriggerBinding;
  driverId: string;
  activeVersionId: string;
  nextFire: string;
  stats: { total: number; completed: number; failed: number; running: number };
  onEditConfig: () => void;
}): JSX.Element {
  const isCron = binding.source_kind === "cron";
  return (
    <div className={styles.scroll}>
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
          {isCron ? (
            <>
              <div>
                <dt>Schedule</dt>
                <dd>
                  {describeCronSchedule(binding.schedule)}
                  {binding.schedule ? (
                    <code className={styles.inlineCode}>{binding.schedule}</code>
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

      <section className={styles.card}>
        <button
          type="button"
          className={styles.btn}
          onClick={onEditConfig}
          data-testid="workflow-agent-info-edit-config"
        >
          Edit configuration
        </button>
      </section>
    </div>
  );
}
