/**
 * AgentWorkPanel — Direction J right panel.
 *
 * Themed to match the rest of the app: uses --color-status-*,
 * --color-priority-*, --space-*, --font-size-*, etc. so light/dark theme
 * and global token tweaks apply consistently. Status is communicated
 * through a left-border accent + glyph, mirroring the IssueCard pattern
 * (no full-bleed pastel backgrounds — those clashed with the dark theme).
 *
 * Data source: existing IssueStore (no new endpoints — this is pure
 * client-side filtering + grouping over the issues already fetched for the
 * workspace).
 */

import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { useStore } from "zustand";

import { useWorkspaceViewActions } from "@/contexts/WorkspaceViewContext";
import { useAgentStoreInstance, useIssueStoreInstance } from "@/hooks";
import {
  isTerminalWorkflowRunStatus,
  type WorkflowRun,
  type WorkflowRunStatus,
} from "@/hooks/api";
import { type Issue, type LoomAgentStatus, parseLoomStatus } from "@/types";
import { isLeadRole } from "@/utils/agentRole";
import { statusBucket, type StatusBucket } from "@/utils/statusBuckets";

import {
  OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
  OPEN_QUEUE_PANEL_MAX_WIDTH,
  OPEN_QUEUE_PANEL_MIN_WIDTH,
} from "@/hooks/ui/useOpenQueuePanelWidth";

import { PanelWidthResizeHandle } from "./PanelWidthResizeHandle";
import styles from "./AgentWorkPanel.module.css";

interface AgentWorkPanelProps {
  agentName: string | undefined;
  /** Resizable panel width in px (defaults to 420). */
  panelWidth?: number | undefined;
  onPanelWidthDelta?: ((deltaPx: number) => void) | undefined;
  onPanelWidthReset?: (() => void) | undefined;
  /**
   * Override the default task-card click behavior. When provided, the panel
   * calls this instead of WorkspaceViewActions.handleIssueClick — used by
   * /agents to surface the IssueDetailPanel inline rather than as a slide-out.
   */
  onTaskClick?: (task: Issue) => void;
  /** Run the selected epic in the currently selected lead terminal. */
  onRunEpic?: (epicId: string) => void | Promise<void>;
  /** Open the terminal for the worker currently attached to a task. */
  onAgentClick?: (agentName: string) => void;
  /** Active epic-runner workflow runs keyed by epic id. */
  epicRunnerRuns?: Record<string, WorkflowRun | undefined>;
}

export interface EpicGroup {
  epicId: string;
  epicTitle: string;
  tasks: Issue[];
  doneCount: number;
  totalCount: number;
}

export interface Counts {
  active: number;
  done: number;
  open: number;
  review: number;
  blocked: number;
}

/** Status filter pills shown in the Open Queue header (non-lead modes). */
export type StatusFilter = "all" | StatusBucket;

/** Epic filter pills shown in the lead (no-epic) Open Queue header. */
export type LeadFilter = "all" | "running" | "idle";

export { statusBucket };

interface WorkerHistoryItem {
  agent: LoomAgentStatus;
  taskId: string;
  epicId: string;
  status: "running" | "completed" | "failed";
  openable: boolean;
}

const ORPHAN_EPIC_KEY = "__orphan__";

type PanelMode = "epic" | "agent" | "lead-open" | "workspace";

const STATUS_GLYPH: Record<string, string> = {
  active: "▸",
  in_progress: "▸",
  open: "○",
  ready: "○",
  blocked: "⨯",
  review: "◔",
  done: "✓",
  closed: "✓",
};

export function AgentWorkPanel({
  agentName,
  panelWidth = OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
  onPanelWidthDelta,
  onPanelWidthReset,
  onTaskClick,
  onRunEpic,
  onAgentClick,
  epicRunnerRuns,
}: AgentWorkPanelProps): JSX.Element {
  const { handleIssueClick } = useWorkspaceViewActions();
  const dispatchClick = onTaskClick ?? handleIssueClick;
  const issueStore = useIssueStoreInstance();
  const issuesMap = useStore(issueStore, (s) => s.issuesMap);
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const selectedAgent = useMemo(
    () => (agentName ? agents.find((x) => x.name === agentName) : undefined),
    [agentName, agents],
  );
  const selectedAgentIsLead = isLeadRole(selectedAgent?.role);
  // epicClaims maps epic id to lead name when SOME OTHER lead already owns
  // that epic (i.e. lead.parent === epicId and lead.name !== agentName).
  // Used to disable the Run button and surface a "claimed by ..." badge so
  // two leads can't silently bind to the same epic.
  const epicClaims = useMemo<Map<string, string>>(() => {
    const m = new Map<string, string>();
    for (const a of agents) {
      if (!a || !isLeadRole(a.role)) continue;
      if (!a.parent) continue;
      if (a.name === agentName) continue;
      m.set(a.parent, a.name);
    }
    return m;
  }, [agents, agentName]);
  const workerByTaskId = useMemo(() => buildWorkerByTaskId(agents), [agents]);
  const workerHistoryByEpic = useMemo(
    () => buildWorkerHistoryByEpic(agents, issuesMap),
    [agents, issuesMap],
  );

  // Detect the agent's "active epic": parse its current-task ID from status,
  // look the task up, walk to its parent. When set, the panel focuses on that
  // single epic and all its child tasks (not just the agent's own) so the
  // user sees the full DAG the agent is working under.
  const activeEpicId = useMemo<string | undefined>(() => {
    if (!agentName) return undefined;
    if (!selectedAgent) return undefined;
    if (selectedAgent.parent) return selectedAgent.parent;
    const parsed = parseLoomStatus(selectedAgent.status ?? "");
    if (!parsed.taskId) return undefined;
    const task = issuesMap.get(parsed.taskId);
    return task?.parent || undefined;
  }, [agentName, selectedAgent, issuesMap]);

  // Three rendering modes:
  //   1. activeEpic — agent is working a task; zoom into that epic and show
  //      ALL its child tasks (regardless of assignee) so siblings/blockers
  //      are visible.
  //   2. agent-scoped — agent is idle but has tasks assigned; group those by
  //      epic, fall back to "Unassigned" for orphans. (Original F2 behavior.)
  //   3. workspace-wide — agent is idle and has nothing assigned; show all
  //      workspace issues grouped by epic so the panel still has useful
  //      context. Triggered when groupAgentTasksByEpic returns 0 tasks.
  const { groups, totalTasks, mode } = useMemo(() => {
    if (activeEpicId) {
      const v = scopeToEpic(issuesMap, activeEpicId);
      return { ...v, mode: "epic" as const };
    }
    if (selectedAgentIsLead) {
      const v = groupOpenByEpic(issuesMap);
      return { ...v, mode: "lead-open" as const };
    }
    const agentScoped = groupAgentTasksByEpic(issuesMap, agentName);
    if (agentScoped.totalTasks > 0) {
      return { ...agentScoped, mode: "agent" as const };
    }
    const wide = groupAllByEpic(issuesMap);
    return { ...wide, mode: "workspace" as const };
  }, [issuesMap, agentName, activeEpicId, selectedAgentIsLead]);
  const focused = mode === "epic";
  const displayCounts = useMemo(
    () =>
      countTaskStatusesWithWorkers(
        groups.flatMap((group) => group.tasks),
        workerByTaskId,
      ),
    [groups, workerByTaskId],
  );

  // Open Queue interactions (Aether design, pin 20): status filter pills in
  // task modes, Running/Not running pills + collapsed epic cards in lead
  // mode. All reset when the selected agent changes so a stale filter never
  // hides another agent's queue.
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [leadFilter, setLeadFilter] = useState<LeadFilter>("all");
  const [expandedEpics, setExpandedEpics] = useState<Record<string, boolean>>(
    {},
  );
  useEffect(() => {
    setStatusFilter("all");
    setLeadFilter("all");
    setExpandedEpics({});
  }, [agentName]);

  // An epic is "running" when a lead has claimed it or an epic-runner
  // workflow run is still active — agent presence, not completion progress
  // (the axis the design iterations settled on).
  const isRunningEpic = (epicId: string): boolean => {
    if (epicClaims.has(epicId)) return true;
    const run = epicRunnerRuns?.[epicId];
    return run != null && !isTerminalWorkflowRunStatus(run.status);
  };

  const leadCounts = useMemo(() => {
    let running = 0;
    let idle = 0;
    if (mode === "lead-open") {
      for (const group of groups) {
        if (isOrphanGroup(group)) continue;
        if (isRunningEpic(group.epicId)) running++;
        else idle++;
      }
    }
    return { running, idle };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, groups, epicClaims, epicRunnerRuns]);

  const filterTasks = (tasks: Issue[]): Issue[] => {
    if (statusFilter === "all") return tasks;
    return tasks.filter(
      (task) =>
        statusBucket(effectiveTaskStatus(task, workerByTaskId.get(task.id))) ===
        statusFilter,
    );
  };

  const visibleGroups = useMemo(() => {
    if (mode === "lead-open") {
      if (leadFilter === "all") return groups;
      return groups.filter((group) => {
        if (isOrphanGroup(group)) return leadFilter === "idle";
        return isRunningEpic(group.epicId) === (leadFilter === "running");
      });
    }
    // Focused mode keeps its single group visible so the header context
    // stays put even when the filter empties the task list.
    if (focused || statusFilter === "all") return groups;
    return groups.filter((group) => filterTasks(group.tasks).length > 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    mode,
    groups,
    focused,
    leadFilter,
    statusFilter,
    workerByTaskId,
    epicClaims,
    epicRunnerRuns,
  ]);
  const isAssignedEpicFocused =
    focused && selectedAgentIsLead && selectedAgent?.parent === activeEpicId;
  const activeEpicLabel = isAssignedEpicFocused
    ? "Assigned epic"
    : "Active epic";
  const deliveryLabel = isAssignedEpicFocused
    ? leadDeliveryStateLabel(selectedAgent?.delivery_state)
    : "";

  if (!agentName) {
    return (
      <div className={styles.empty} style={{ width: panelWidth }}>
        {onPanelWidthDelta ? (
          <PanelWidthResizeHandle
            width={panelWidth}
            onDelta={onPanelWidthDelta}
            onReset={onPanelWidthReset}
            minWidth={OPEN_QUEUE_PANEL_MIN_WIDTH}
            maxWidth={OPEN_QUEUE_PANEL_MAX_WIDTH}
          />
        ) : null}
        <div className={styles.emptyMessage}>
          Select an agent from the rail to see their work.
        </div>
      </div>
    );
  }

  const focusedHistory = focused
    ? (workerHistoryByEpic.get(groups[0]?.epicId ?? "") ?? [])
    : [];

  return (
    <aside
      className={styles.panel}
      style={{ width: panelWidth }}
      aria-label="Agent work"
    >
      {onPanelWidthDelta ? (
        <PanelWidthResizeHandle
          width={panelWidth}
          onDelta={onPanelWidthDelta}
          onReset={onPanelWidthReset}
          minWidth={OPEN_QUEUE_PANEL_MIN_WIDTH}
          maxWidth={OPEN_QUEUE_PANEL_MAX_WIDTH}
        />
      ) : null}
      <div className={styles.panelContent}>
        <div className={styles.header}>
          {focused && groups[0] ? (
            <>
              <div className={styles.queueHeadRow}>
                <span className={styles.label}>Open queue</span>
                <span className={styles.openTotal}>
                  <strong>{totalTasks - displayCounts.done}</strong> open
                </span>
              </div>
              <div className={styles.activeEpicTitle}>
                <span
                  className={
                    isOrphanGroup(groups[0])
                      ? styles.orphanChip
                      : styles.epicChip
                  }
                >
                  {isOrphanGroup(groups[0]) ? "UNASSIGNED" : "EPIC"}
                </span>
                <span className={styles.activeEpicTitleText}>
                  {groups[0].epicTitle}
                </span>
                <code className={styles.activeEpicId}>{groups[0].epicId}</code>
              </div>
              <div className={styles.progressLine}>
                {agentName ? (
                  <>
                    claimed by {agentName} · {displayCounts.done} of{" "}
                    {totalTasks} done
                  </>
                ) : (
                  <>
                    {displayCounts.done} of {totalTasks} done
                  </>
                )}
              </div>
              {deliveryLabel ? (
                <div className={styles.activeEpicTag}>
                  <span
                    aria-hidden="true"
                    className={styles.activeEpicTagDot}
                  />
                  {activeEpicLabel}
                  <span className={styles.deliveryStatePill}>
                    {deliveryLabel}
                  </span>
                </div>
              ) : null}
            </>
          ) : (
            <div className={styles.label}>
              {formatQueueLabel(mode, groups, totalTasks, agentName)}
            </div>
          )}

          <DistributionBar counts={displayCounts} total={totalTasks} />

          {mode === "lead-open" ? (
            <div
              className={styles.filterRow}
              role="group"
              aria-label="Filter epics"
            >
              <FilterPill
                label="All"
                count={leadCounts.running + leadCounts.idle}
                active={leadFilter === "all"}
                onClick={() => setLeadFilter("all")}
              />
              <FilterPill
                label="Running"
                count={leadCounts.running}
                active={leadFilter === "running"}
                dotKind="in_progress"
                onClick={() => setLeadFilter("running")}
              />
              <FilterPill
                label="Not running"
                count={leadCounts.idle}
                active={leadFilter === "idle"}
                dotKind="open"
                onClick={() => setLeadFilter("idle")}
              />
            </div>
          ) : (
            <div
              className={styles.filterRow}
              role="group"
              aria-label="Filter tasks by status"
            >
              <FilterPill
                label="All"
                count={totalTasks}
                active={statusFilter === "all"}
                onClick={() => setStatusFilter("all")}
              />
              <FilterPill
                label="In Progress"
                count={displayCounts.active}
                active={statusFilter === "in_progress"}
                dotKind="in_progress"
                onClick={() => toggleStatusFilter("in_progress")}
              />
              <FilterPill
                label="Open"
                count={displayCounts.open}
                active={statusFilter === "open"}
                dotKind="open"
                onClick={() => toggleStatusFilter("open")}
              />
              <FilterPill
                label="Review"
                count={displayCounts.review}
                active={statusFilter === "review"}
                dotKind="review"
                onClick={() => toggleStatusFilter("review")}
              />
              <FilterPill
                label="Blocked"
                count={displayCounts.blocked}
                active={statusFilter === "blocked"}
                dotKind="blocked"
                onClick={() => toggleStatusFilter("blocked")}
              />
              <FilterPill
                label="Done"
                count={displayCounts.done}
                active={statusFilter === "done"}
                dotKind="done"
                onClick={() => toggleStatusFilter("done")}
              />
            </div>
          )}
        </div>

        <div className={styles.body}>
          {visibleGroups.length === 0 ? (
            <div className={styles.emptyState}>
              {groups.length > 0
                ? "Nothing matches this filter."
                : focused
                  ? "Active epic has no child tasks."
                  : mode === "lead-open"
                    ? "No open epics or tasks in this workspace."
                    : mode === "workspace"
                      ? "No issues in this workspace yet."
                      : `No tasks assigned to ${agentName} yet.`}
            </div>
          ) : (
            visibleGroups.map((group) => {
              const claimedBy = epicClaims.get(group.epicId);
              // Don't offer Run for drained epics (no open work left) or
              // ones already claimed by another lead. Both prevent the
              // dead-end / silent-conflict cases the multi-lead UI walk
              // surfaced.
              const remainingOpen = group.totalCount - group.doneCount;
              const epicRunnerRun = epicRunnerRuns?.[group.epicId];
              const runnerActive =
                epicRunnerRun != null &&
                !isTerminalWorkflowRunStatus(epicRunnerRun.status);
              const canRunEpic =
                mode === "lead-open" &&
                selectedAgentIsLead &&
                group.epicId !== ORPHAN_EPIC_KEY &&
                onRunEpic != null &&
                remainingOpen > 0 &&
                !claimedBy &&
                !runnerActive;
              const visibleTasks =
                mode === "lead-open" ? group.tasks : filterTasks(group.tasks);
              // Lead-mode epic cards collapse to a single header row by
              // default (the design's "› N" pill); Unassigned stays open.
              const collapsible = mode === "lead-open" && !isOrphanGroup(group);
              const collapsed = collapsible && !expandedEpics[group.epicId];
              return (
                <EpicGroupCard
                  key={group.epicId}
                  group={group}
                  visibleTasks={visibleTasks}
                  remainingOpen={remainingOpen}
                  collapsible={collapsible}
                  collapsed={collapsed}
                  onToggleCollapse={() =>
                    setExpandedEpics((prev) => ({
                      ...prev,
                      [group.epicId]: !prev[group.epicId],
                    }))
                  }
                  canRunEpic={canRunEpic}
                  claimedBy={claimedBy}
                  epicRunnerRun={epicRunnerRun}
                  onRunEpic={onRunEpic}
                  workerByTaskId={workerByTaskId}
                  workerHistory={
                    focused ? [] : (workerHistoryByEpic.get(group.epicId) ?? [])
                  }
                  onAgentClick={onAgentClick}
                  onTaskClick={(task) => dispatchClick(task)}
                />
              );
            })
          )}
        </div>

        {focused && focusedHistory.length > 0 && groups[0] ? (
          <div className={styles.footer}>
            <WorkerHistory
              items={focusedHistory}
              tasks={groups[0].tasks}
              onWorkerClick={onAgentClick}
              onTaskClick={(task) => dispatchClick(task)}
            />
          </div>
        ) : null}
      </div>
    </aside>
  );

  function toggleStatusFilter(next: Exclude<StatusFilter, "all">): void {
    setStatusFilter((prev) => (prev === next ? "all" : next));
  }
}

function EpicGroupCard({
  group,
  visibleTasks,
  remainingOpen,
  collapsible,
  collapsed,
  onToggleCollapse,
  canRunEpic,
  claimedBy,
  epicRunnerRun,
  onRunEpic,
  workerByTaskId,
  workerHistory,
  onAgentClick,
  onTaskClick,
}: {
  group: EpicGroup;
  /** Tasks after the header status filter; superset is group.tasks. */
  visibleTasks: Issue[];
  /** Open (non-done) tasks in this epic group. */
  remainingOpen: number;
  /** Lead mode: epic cards collapse to their header row. */
  collapsible: boolean;
  collapsed: boolean;
  onToggleCollapse: () => void;
  canRunEpic: boolean;
  /** Lead name when another lead already owns this epic; renders a badge. */
  claimedBy?: string | undefined;
  epicRunnerRun?: WorkflowRun | undefined;
  onRunEpic?: ((epicId: string) => void | Promise<void>) | undefined;
  workerByTaskId: Map<string, LoomAgentStatus>;
  workerHistory: WorkerHistoryItem[];
  onAgentClick?: ((agentName: string) => void) | undefined;
  onTaskClick: (task: Issue) => void;
}): JSX.Element {
  const pct =
    group.totalCount > 0 ? (group.doneCount / group.totalCount) * 100 : 0;
  const isOrphan = isOrphanGroup(group);
  const runnerActive =
    epicRunnerRun != null && !isTerminalWorkflowRunStatus(epicRunnerRun.status);
  const claimedTasks = epicRunnerRun
    ? group.tasks.filter((task) =>
        isTaskClaimedByWorkflowRun(task, epicRunnerRun.run_id),
      )
    : [];
  return (
    <div className={styles.group}>
      <div className={styles.groupHeader}>
        <span className={isOrphan ? styles.orphanChip : styles.epicChip}>
          {isOrphan ? "UNASSIGNED" : "EPIC"}
        </span>
        <span className={styles.epicTitle}>{group.epicTitle}</span>
        {collapsible ? (
          <button
            type="button"
            className={styles.expandPill}
            onClick={onToggleCollapse}
            aria-expanded={!collapsed}
            aria-label={`${collapsed ? "Expand" : "Collapse"} epic ${group.epicId} (${remainingOpen} open)`}
          >
            <span aria-hidden="true">{collapsed ? "›" : "⌄"}</span>
            {remainingOpen}
          </button>
        ) : (
          <span className={styles.epicCount}>
            {group.doneCount}/{group.totalCount}
          </span>
        )}
        {canRunEpic ? (
          <button
            type="button"
            className={styles.runEpicButton}
            onClick={() => {
              void onRunEpic?.(group.epicId);
            }}
            aria-label={`Run epic ${group.epicId}`}
          >
            Run
          </button>
        ) : runnerActive ? (
          <span
            className={styles.epicClaim}
            title={`Epic runner ${epicRunnerRun.run_id} is active`}
            aria-label={`Epic runner ${epicRunnerRun.run_id} is active for ${group.epicId}`}
          >
            runner active
          </span>
        ) : claimedBy ? (
          <span
            className={styles.epicClaim}
            title={`Claimed by lead ${claimedBy}`}
            aria-label={`Epic ${group.epicId} claimed by lead ${claimedBy}`}
          >
            claimed by {claimedBy} · {remainingOpen} open
          </span>
        ) : collapsible && remainingOpen > 0 ? (
          <span
            className={styles.epicClaim}
            title={`${remainingOpen} open tasks`}
            aria-label={`Epic ${group.epicId} unclaimed with ${remainingOpen} open tasks`}
          >
            Unclaimed · {remainingOpen} open
          </span>
        ) : null}
      </div>
      <div className={styles.epicProgress}>
        <div
          className={styles.epicProgressFill}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </div>
      {epicRunnerRun ? (
        <EpicRunnerRunStrip
          run={epicRunnerRun}
          claimedTasks={claimedTasks}
          totalTasks={group.totalCount}
        />
      ) : null}
      {collapsed ? null : (
        <>
          {visibleTasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              workerAgent={workerByTaskId.get(task.id)}
              onClick={() => onTaskClick(task)}
            />
          ))}
          <WorkerHistory
            items={workerHistory}
            tasks={group.tasks}
            onWorkerClick={onAgentClick}
            onTaskClick={onTaskClick}
          />
        </>
      )}
    </div>
  );
}

/**
 * DistributionBar — proportional status segments (in progress / open /
 * review / blocked / done) over the visible tasks. Replaces the old
 * done-only progress fill: the same strip now answers "where does the work
 * sit", not just "how much is finished".
 */
function DistributionBar({
  counts,
  total,
}: {
  counts: Counts;
  total: number;
}): JSX.Element | null {
  if (total <= 0) return null;
  const segments = [
    { kind: "in_progress", value: counts.active, label: "in progress" },
    { kind: "open", value: counts.open, label: "open" },
    { kind: "review", value: counts.review, label: "review" },
    { kind: "blocked", value: counts.blocked, label: "blocked" },
    { kind: "done", value: counts.done, label: "done" },
  ].filter((segment) => segment.value > 0);
  const summary = segments
    .map((segment) => `${segment.value} ${segment.label}`)
    .join(", ");
  return (
    <div
      className={styles.distBar}
      role="img"
      aria-label={`Status distribution: ${summary}`}
    >
      {segments.map((segment) => (
        <span
          key={segment.kind}
          className={styles.distSeg}
          data-k={segment.kind}
          style={{ flexGrow: segment.value }}
        />
      ))}
    </div>
  );
}

function FilterPill({
  label,
  count,
  active,
  dotKind,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  dotKind?: Exclude<StatusFilter, "all">;
  onClick: () => void;
}): JSX.Element {
  return (
    <button
      type="button"
      className={styles.filterPill}
      data-active={active ? "true" : undefined}
      aria-pressed={active}
      onClick={onClick}
    >
      {dotKind ? (
        <span className={styles.pillDot} data-k={dotKind} aria-hidden="true" />
      ) : null}
      {label}
      <span className={styles.pillCount}>{count}</span>
    </button>
  );
}

function EpicRunnerRunStrip({
  run,
  claimedTasks,
  totalTasks,
}: {
  run: WorkflowRun;
  claimedTasks: Issue[];
  totalTasks: number;
}): JSX.Element {
  const statusLabel = formatWorkflowRunStatus(run.status);
  const claimedTaskIds = claimedTasks.map((task) => task.id).join(", ");
  const logsRef = run.output?.logs_ref;

  return (
    <section
      className={styles.runnerStrip}
      data-status={run.status}
      aria-label={`Epic runner ${run.run_id}`}
    >
      <div className={styles.runnerStripHeader}>
        <span className={styles.runnerStatus}>Epic runner · {statusLabel}</span>
        <code className={styles.runnerRunId}>{shortRunId(run.run_id)}</code>
      </div>
      <div className={styles.runnerMeta}>
        {claimedTasks.length > 0
          ? `Working ${claimedTasks.length}/${totalTasks}: ${claimedTaskIds}`
          : isTerminalWorkflowRunStatus(run.status)
            ? "No child tasks claimed by this run."
            : "Waiting for the driver to claim child tasks."}
      </div>
      {run.summary ? (
        <div className={styles.runnerSummary}>{run.summary}</div>
      ) : null}
      {logsRef ? (
        <div className={styles.runnerMeta}>
          Logs <code className={styles.runnerRunId}>{logsRef}</code>
        </div>
      ) : null}
    </section>
  );
}

function WorkerHistory({
  items,
  tasks,
  onWorkerClick,
  onTaskClick,
}: {
  items: WorkerHistoryItem[];
  tasks: Issue[];
  onWorkerClick?: ((agentName: string) => void) | undefined;
  onTaskClick: (task: Issue) => void;
}): JSX.Element | null {
  if (items.length === 0) return null;
  const taskById = new Map(tasks.map((task) => [task.id, task]));

  return (
    <section className={styles.workerHistory} aria-label="Worker history">
      <div className={styles.workerHistoryHeader}>
        <span>Worker History</span>
        <span className={styles.workerHistoryCount}>{items.length}</span>
      </div>
      <div className={styles.workerHistoryList}>
        {items.map((item) => {
          const task = taskById.get(item.taskId);
          return (
            <div key={item.agent.name} className={styles.workerHistoryRow}>
              <div className={styles.workerHistoryMain}>
                <span
                  className={styles.workerHistoryName}
                  title={item.agent.name}
                >
                  {item.agent.name}
                </span>
                <span className={styles.workerHistoryMeta}>
                  {item.taskId} · {item.status}
                  {item.agent.session_id ? ` · ${item.agent.session_id}` : ""}
                </span>
              </div>
              <div className={styles.workerHistoryActions}>
                {task ? (
                  <button
                    type="button"
                    className={styles.workerHistoryButton}
                    onClick={() => onTaskClick(task)}
                  >
                    Task
                  </button>
                ) : null}
                {item.openable && onWorkerClick ? (
                  <button
                    type="button"
                    className={styles.workerHistoryButton}
                    onClick={() => onWorkerClick(item.agent.name)}
                  >
                    Terminal
                  </button>
                ) : (
                  <span className={styles.workerHistoryChip}>retained</span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

export function formatQueueLabel(
  mode: PanelMode,
  groups: EpicGroup[],
  totalTasks: number,
  agentName: string | undefined,
): string {
  const epicCount = groups.filter((group) => !isOrphanGroup(group)).length;
  const orphanCount = groups.length - epicCount;
  const groupParts = [`${epicCount} ${pluralize("epic", epicCount)}`];
  if (orphanCount > 0) {
    groupParts.push(`${orphanCount} unassigned`);
  }
  const taskPart = `${totalTasks} ${pluralize("task", totalTasks)}`;
  if (mode === "lead-open") {
    return `Open queue · ${groupParts.join(" · ")} · ${taskPart}`;
  }
  if (mode === "workspace") {
    return `Workspace queue · ${groupParts.join(" · ")} · ${taskPart}`;
  }
  return `${agentName ?? "Agent"}'s work · ${groupParts.join(" · ")} · ${taskPart}`;
}

function pluralize(word: string, count: number): string {
  return count === 1 ? word : `${word}s`;
}

function isOrphanGroup(group: EpicGroup): boolean {
  return group.epicId === ORPHAN_EPIC_KEY;
}

function TaskCard({
  task,
  workerAgent,
  onClick,
}: {
  task: Issue;
  workerAgent?: LoomAgentStatus | undefined;
  onClick: () => void;
}): JSX.Element {
  const status = effectiveTaskStatus(task, workerAgent);
  const glyph = STATUS_GLYPH[status] ?? STATUS_GLYPH["open"];
  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    onClick();
  };
  return (
    <div data-status={status} className={styles.taskCard}>
      <div
        role="button"
        tabIndex={0}
        onClick={onClick}
        onKeyDown={handleKeyDown}
        className={styles.taskContent}
      >
        <div className={styles.taskMeta}>
          <span className={styles.statusGlyph} aria-hidden="true">
            {glyph}
          </span>
          <span className={styles.taskId}>{task.id}</span>
          {workerAgent ? (
            <span className={styles.workerCluster}>
              <span className={styles.workerChip} title={workerAgent.name}>
                {workerAgent.name}
              </span>
            </span>
          ) : null}
        </div>
        <div className={styles.taskTitle}>{task.title}</div>
      </div>
    </div>
  );
}

function isTaskClaimedByWorkflowRun(task: Issue, runId: string): boolean {
  return task.assignee === `driver-run:${runId}`;
}

function formatWorkflowRunStatus(status: WorkflowRunStatus): string {
  return status.replace(/_/g, " ");
}

function shortRunId(runId: string): string {
  if (runId.length <= 18) return runId;
  return `${runId.slice(0, 8)}...${runId.slice(-6)}`;
}

export function leadDeliveryStateLabel(
  deliveryState: string | undefined,
): string {
  switch ((deliveryState ?? "").trim().toLowerCase()) {
    case "pending":
      return "context pending";
    case "delivered":
      return "context sent";
    case "acknowledged":
      return "lead acknowledged";
    default:
      return "";
  }
}

export function countTaskStatusesWithWorkers(
  tasks: Issue[],
  workerByTaskId: Map<string, LoomAgentStatus>,
): Counts {
  const counts: Counts = { active: 0, done: 0, open: 0, review: 0, blocked: 0 };
  for (const task of tasks) {
    const bucket = statusBucket(
      effectiveTaskStatus(task, workerByTaskId.get(task.id)),
    );
    if (bucket === "in_progress") counts.active++;
    else if (bucket === "done") counts.done++;
    else if (bucket === "blocked") counts.blocked++;
    else if (bucket === "review") counts.review++;
    else counts.open++;
  }
  return counts;
}

export function effectiveTaskStatus(
  task: Issue,
  workerAgent?: LoomAgentStatus | undefined,
): string {
  const status = (task.status ?? "open").toLowerCase();
  if (status === "closed" || status === "done") return status;
  if (workerAgent && isWorkerTerminalOpenable(workerAgent)) return "active";
  return status;
}

export function buildWorkerByTaskId(
  agents: LoomAgentStatus[],
): Map<string, LoomAgentStatus> {
  const byTaskId = new Map<string, LoomAgentStatus>();

  for (const agent of agents) {
    const taskId =
      agent.task_id || parseLoomStatus(agent.status ?? "").taskId || "";
    if (!taskId) continue;

    const current = byTaskId.get(taskId);
    if (!current || compareWorkerAgent(agent, current) < 0) {
      byTaskId.set(taskId, agent);
    }
  }

  return byTaskId;
}

export function buildWorkerHistoryByEpic(
  agents: LoomAgentStatus[],
  issuesMap: Map<string, Issue>,
): Map<string, WorkerHistoryItem[]> {
  const byEpic = new Map<string, WorkerHistoryItem[]>();

  for (const agent of agents) {
    if (agent.mode !== "ephemeral") continue;
    const taskId =
      agent.task_id || parseLoomStatus(agent.status ?? "").taskId || "";
    if (!taskId) continue;
    const epicId =
      agent.parent || issuesMap.get(taskId)?.parent || ORPHAN_EPIC_KEY;
    const openable = isWorkerTerminalOpenable(agent);
    const status = workerHistoryStatus(agent, openable);
    const list = byEpic.get(epicId) ?? [];
    list.push({ agent, taskId, epicId, status, openable });
    byEpic.set(epicId, list);
  }

  for (const list of byEpic.values()) {
    list.sort((a, b) => {
      const rank = workerHistoryRank(a) - workerHistoryRank(b);
      if (rank !== 0) return rank;
      if (a.taskId !== b.taskId) return a.taskId.localeCompare(b.taskId);
      return a.agent.name.localeCompare(b.agent.name);
    });
  }

  return byEpic;
}

function workerHistoryStatus(
  agent: LoomAgentStatus,
  openable: boolean,
): WorkerHistoryItem["status"] {
  const parsed = parseLoomStatus(agent.status ?? "");
  const state = String(agent.state ?? "")
    .trim()
    .toLowerCase();
  if (parsed.type === "error" || state === "dead") return "failed";
  if (openable) return "running";
  return "completed";
}

function workerHistoryRank(item: WorkerHistoryItem): number {
  switch (item.status) {
    case "running":
      return 0;
    case "failed":
      return 1;
    case "completed":
      return 2;
  }
}

export function isWorkerTerminalOpenable(agent: LoomAgentStatus): boolean {
  const state = String(agent.state ?? "")
    .trim()
    .toLowerCase();
  const desiredState = String(agent.desired_state ?? "")
    .trim()
    .toLowerCase();
  return state !== "stopped" && state !== "dead" && desiredState !== "stopped";
}

function compareWorkerAgent(
  candidate: LoomAgentStatus,
  current: LoomAgentStatus,
): number {
  const scoreDelta = workerAgentScore(current) - workerAgentScore(candidate);
  if (scoreDelta !== 0) return scoreDelta;
  return candidate.name.localeCompare(current.name);
}

function workerAgentScore(agent: LoomAgentStatus): number {
  const parsed = parseLoomStatus(agent.status ?? "");
  let score = 0;
  if (
    parsed.type === "working" ||
    parsed.type === "planning" ||
    parsed.type === "review"
  ) {
    score += 100;
  }
  if (agent.desired_state === "running") score += 20;
  if (agent.desired_state === "stopped") score -= 20;
  if (agent.mode === "ephemeral") score += 5;
  if (agent.session_id) score += 1;
  return score;
}

/**
 * groupAgentTasksByEpic filters issues to those assigned to the agent and
 * groups them by parent (epic). Tasks without a parent are bucketed under
 * a synthetic "Unassigned" epic so they're still visible.
 *
 * Status counting:
 *   - active: status in {in_progress, active}
 *   - done:   status in {closed, done}
 *   - open:   status in {open, ready}
 *   - blocked: status == blocked
 */
export function groupAgentTasksByEpic(
  issuesMap: Map<string, Issue>,
  agentName: string | undefined,
): { groups: EpicGroup[]; counts: Counts; totalTasks: number } {
  const counts: Counts = { active: 0, done: 0, open: 0, review: 0, blocked: 0 };
  if (!agentName) {
    return { groups: [], counts, totalTasks: 0 };
  }

  // Bucket assigned issues by their parent epic ID (or orphan).
  const byEpic = new Map<string, Issue[]>();
  let totalTasks = 0;

  for (const issue of issuesMap.values()) {
    if (issue.assignee !== agentName) continue;
    if (issue.issue_type === "epic") continue; // don't show the epic itself as a task

    totalTasks++;
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "closed" || status === "done") counts.done++;
    else if (status === "blocked") counts.blocked++;
    else if (status === "review") counts.review++;
    else counts.open++;

    const epicKey = issue.parent || ORPHAN_EPIC_KEY;
    const list = byEpic.get(epicKey) ?? [];
    list.push(issue);
    byEpic.set(epicKey, list);
  }

  // Resolve epic titles. The map already carries the epic issue if it was
  // fetched alongside its children; otherwise we fall back to the raw ID.
  const groups: EpicGroup[] = [];
  for (const [epicKey, tasks] of byEpic.entries()) {
    const epicIssue =
      epicKey === ORPHAN_EPIC_KEY ? undefined : issuesMap.get(epicKey);
    const epicTitle =
      epicKey === ORPHAN_EPIC_KEY
        ? "Unassigned"
        : (epicIssue?.title ?? epicKey);

    let doneCount = 0;
    for (const t of tasks) {
      const s = (t.status ?? "open").toLowerCase();
      if (s === "closed" || s === "done") doneCount++;
    }

    // Sort tasks: active first, then open, blocked, review, done last.
    tasks.sort(taskSortRank);

    groups.push({
      epicId: epicKey,
      epicTitle,
      tasks,
      doneCount,
      totalCount: tasks.length,
    });
  }

  // Stable sort: orphan group last; otherwise alphabetical by title.
  groups.sort((a, b) => {
    if (a.epicId === ORPHAN_EPIC_KEY) return 1;
    if (b.epicId === ORPHAN_EPIC_KEY) return -1;
    return a.epicTitle.localeCompare(b.epicTitle);
  });

  return { groups, counts, totalTasks };
}

function taskSortRank(a: Issue, b: Issue): number {
  return statusRank(a.status) - statusRank(b.status);
}

/**
 * groupAllByEpic returns a workspace-wide view: every non-epic issue grouped
 * by its parent epic. Used when the selected agent has nothing assigned and
 * no active task — the right panel still shows useful context (the broader
 * workspace queue) instead of going empty.
 */
export function groupAllByEpic(issuesMap: Map<string, Issue>): {
  groups: EpicGroup[];
  counts: Counts;
  totalTasks: number;
} {
  const counts: Counts = { active: 0, done: 0, open: 0, review: 0, blocked: 0 };
  const byEpic = new Map<string, Issue[]>();
  let totalTasks = 0;

  for (const issue of issuesMap.values()) {
    if (issue.issue_type === "epic") continue;
    totalTasks++;
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "closed" || status === "done") counts.done++;
    else if (status === "blocked") counts.blocked++;
    else if (status === "review") counts.review++;
    else counts.open++;

    const epicKey = issue.parent || ORPHAN_EPIC_KEY;
    const list = byEpic.get(epicKey) ?? [];
    list.push(issue);
    byEpic.set(epicKey, list);
  }

  const groups: EpicGroup[] = [];
  for (const [epicKey, tasks] of byEpic.entries()) {
    const epicIssue =
      epicKey === ORPHAN_EPIC_KEY ? undefined : issuesMap.get(epicKey);
    const epicTitle =
      epicKey === ORPHAN_EPIC_KEY
        ? "Unassigned"
        : (epicIssue?.title ?? epicKey);

    let doneCount = 0;
    for (const t of tasks) {
      const s = (t.status ?? "open").toLowerCase();
      if (s === "closed" || s === "done") doneCount++;
    }
    tasks.sort(taskSortRank);

    groups.push({
      epicId: epicKey,
      epicTitle,
      tasks,
      doneCount,
      totalCount: tasks.length,
    });
  }

  groups.sort((a, b) => {
    if (a.epicId === ORPHAN_EPIC_KEY) return 1;
    if (b.epicId === ORPHAN_EPIC_KEY) return -1;
    return a.epicTitle.localeCompare(b.epicTitle);
  });

  return { groups, counts, totalTasks };
}

/**
 * groupOpenByEpic returns open epics plus their non-closed child tasks. This
 * is the idle lead view: it shows what a lead can pick up next without mixing
 * in completed history.
 */
export function groupOpenByEpic(issuesMap: Map<string, Issue>): {
  groups: EpicGroup[];
  counts: Counts;
  totalTasks: number;
} {
  const counts: Counts = { active: 0, done: 0, open: 0, review: 0, blocked: 0 };
  const byEpic = new Map<string, Issue[]>();
  let totalTasks = 0;

  for (const issue of issuesMap.values()) {
    if (issue.issue_type === "epic") {
      if (isOpenIssue(issue)) {
        byEpic.set(issue.id, byEpic.get(issue.id) ?? []);
      }
      continue;
    }
    if (!isOpenIssue(issue)) continue;
    totalTasks++;
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "blocked") counts.blocked++;
    else if (status === "review") counts.review++;
    else counts.open++;

    const epicKey = issue.parent || ORPHAN_EPIC_KEY;
    const list = byEpic.get(epicKey) ?? [];
    list.push(issue);
    byEpic.set(epicKey, list);
  }

  const groups: EpicGroup[] = [];
  for (const [epicKey, tasks] of byEpic.entries()) {
    const epicIssue =
      epicKey === ORPHAN_EPIC_KEY ? undefined : issuesMap.get(epicKey);
    const epicTitle =
      epicKey === ORPHAN_EPIC_KEY
        ? "Unassigned"
        : (epicIssue?.title ?? epicKey);
    tasks.sort(taskSortRank);
    groups.push({
      epicId: epicKey,
      epicTitle,
      tasks,
      doneCount: 0,
      totalCount: tasks.length,
    });
  }

  groups.sort((a, b) => {
    if (a.epicId === ORPHAN_EPIC_KEY) return 1;
    if (b.epicId === ORPHAN_EPIC_KEY) return -1;
    return a.epicTitle.localeCompare(b.epicTitle);
  });

  return { groups, counts, totalTasks };
}

/**
 * scopeToEpic returns a single-group view focused on one epic and ALL its
 * child tasks (regardless of assignee). Used when the selected agent has an
 * active task — the panel zooms into the broader DAG the agent is contributing
 * to so the user sees siblings, blockers, and downstream work in context.
 */
export function scopeToEpic(
  issuesMap: Map<string, Issue>,
  epicId: string,
): { groups: EpicGroup[]; counts: Counts; totalTasks: number } {
  const counts: Counts = { active: 0, done: 0, open: 0, review: 0, blocked: 0 };
  const tasks: Issue[] = [];

  for (const issue of issuesMap.values()) {
    if (issue.parent !== epicId) continue;
    if (issue.issue_type === "epic") continue;
    tasks.push(issue);
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "closed" || status === "done") counts.done++;
    else if (status === "blocked") counts.blocked++;
    else if (status === "review") counts.review++;
    else counts.open++;
  }

  tasks.sort(taskSortRank);

  const epicIssue = issuesMap.get(epicId);
  const doneCount = tasks.filter((t) => {
    const s = (t.status ?? "").toLowerCase();
    return s === "closed" || s === "done";
  }).length;

  const group: EpicGroup = {
    epicId,
    epicTitle: epicIssue?.title ?? epicId,
    tasks,
    doneCount,
    totalCount: tasks.length,
  };

  return { groups: [group], counts, totalTasks: tasks.length };
}

function isOpenIssue(issue: Issue): boolean {
  const status = (issue.status ?? "open").toLowerCase();
  return status !== "closed" && status !== "done" && status !== "deferred";
}

function statusRank(status: string | undefined): number {
  switch ((status ?? "").toLowerCase()) {
    case "in_progress":
    case "active":
      return 0;
    case "open":
    case "ready":
      return 1;
    case "blocked":
      return 2;
    case "review":
      return 3;
    case "closed":
    case "done":
      return 4;
    default:
      return 5;
  }
}
