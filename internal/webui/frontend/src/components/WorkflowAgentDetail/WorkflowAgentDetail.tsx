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

import { useCallback, useEffect, useMemo, useState } from "react";

import { WorkflowSourceModal } from "@/components/WorkflowSourceModal";
import { useWorkflowAgentDetail } from "@/hooks/workflows/useWorkflowAgentDetail";
import { useToast } from "@/hooks/ui/useToast";
import {
  getWorkspaceRole,
  isTerminalWorkflowRunStatus,
  promptAgentRoleName,
  updateWorkspaceRole,
  type TriggerBinding,
  type UpdateTriggerBindingRequest,
  type WorkflowRun,
  type WorkflowRunStatus,
} from "@/api";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import {
  bindingCadenceLabel,
  bindingHealth,
  bindingKindLabel,
  describeCronSchedule,
  formatFireTime,
} from "@/utils/bindingDisplay";

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
  onRunBinding,
  onUpdate,
  onDelete,
  onDeleted,
}: WorkflowAgentDetailProps): JSX.Element {
  const { runs, loading, error, driverId, activeVersionId, stats, refresh } =
    useWorkflowAgentDetail(workspaceId, binding.driver_id);
  const { showToast } = useToast();

  const [tab, setTab] = useState<DetailTab>("runs");
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [showSource, setShowSource] = useState(false);
  const [busy, setBusy] = useState(false);

  // A prompt agent carries its roleName in the binding's run-input
  // (source_config_ref). When present, the Info tab shows the ROLE prompt editor
  // (the primary edit surface) alongside the source editor (advanced).
  const promptRoleName = promptAgentRoleName(binding);

  const avatarBg = getAvatarColor(binding.binding_id);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#171717";
  const cadence = bindingCadenceLabel(binding);
  const nextFire = formatFireTime(binding.next_fire_at);
  const health = bindingHealth(binding);

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
  }, [binding.binding_id, onRunBinding, refresh, showToast]);

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
          data-state={health.state}
          title={health.tooltip}
          data-testid="workflow-agent-status-pill"
        >
          {health.label}
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
            workspaceId={workspaceId}
            binding={binding}
            roleName={promptRoleName}
            driverId={driverId || binding.driver_id}
            activeVersionId={activeVersionId || binding.driver_version_id}
            nextFire={nextFire}
            stats={stats}
            onEditConfig={() => setShowSource(true)}
            onUpdate={onUpdate}
            onDelete={onDelete}
            onDeleted={onDeleted}
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
function ManageCard({
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
  const canSave = !busy && (nameChanged || scheduleChanged);

  const handleSave = useCallback(async () => {
    setEditError(null);
    const req: UpdateTriggerBindingRequest = {};
    if (nameChanged) req.name = name.trim();
    if (scheduleChanged) req.schedule = resolvedCron;
    if (req.name === undefined && req.schedule === undefined) return;
    setBusy(true);
    try {
      await onUpdate(binding.binding_id, req);
      showToast("Agent updated", { type: "success" });
    } catch (err) {
      // A bad custom cron surfaces here as the server's parse error (400) —
      // we never re-implement cron validation client-side.
      setEditError((err as Error).message || "Failed to update agent");
    } finally {
      setBusy(false);
    }
  }, [
    binding.binding_id,
    name,
    nameChanged,
    resolvedCron,
    scheduleChanged,
    onUpdate,
    showToast,
  ]);

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
        <div className={styles.errorText} role="alert" data-testid="workflow-agent-edit-error">
          {editError}
        </div>
      )}

      <div className={styles.manageActions}>
        <button
          type="button"
          className={styles.btnPrimary}
          onClick={handleSave}
          disabled={!canSave}
          data-testid="workflow-agent-save-edit"
        >
          Save changes
        </button>
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
          <div className={styles.confirmRow} data-testid="workflow-agent-delete-confirm">
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
        if (!cancelled) setError((err as Error).message || "Failed to load role");
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
            style={{ minHeight: "12rem", fontFamily: "var(--font-mono, monospace)" }}
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
