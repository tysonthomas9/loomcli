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

import { useMemo } from "react";
import { useStore } from "zustand";

import { useWorkspaceViewActions } from "@/contexts/WorkspaceViewContext";
import { useAgentStoreInstance, useIssueStoreInstance } from "@/hooks";
import { type Issue, parseLoomStatus } from "@/types";

import styles from "./AgentWorkPanel.module.css";

interface AgentWorkPanelProps {
  agentName: string | undefined;
  /**
   * Override the default task-card click behavior. When provided, the panel
   * calls this instead of WorkspaceViewActions.handleIssueClick — used by
   * /agents to surface the IssueDetailPanel inline rather than as a slide-out.
   */
  onTaskClick?: (task: Issue) => void;
}

interface EpicGroup {
  epicId: string;
  epicTitle: string;
  tasks: Issue[];
  doneCount: number;
  totalCount: number;
}

interface Counts {
  active: number;
  done: number;
  open: number;
  blocked: number;
}

const ORPHAN_EPIC_KEY = "__orphan__";

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

const PRIORITY_LABEL: Record<number, string> = {
  0: "P0",
  1: "P1",
  2: "P2",
  3: "P3",
  4: "P4",
};

const PRIORITY_CLASS: Record<number, string | undefined> = {
  0: styles.priority0,
  1: styles.priority1,
  2: styles.priority2,
  3: styles.priority3,
  4: styles.priority4,
};

export function AgentWorkPanel({
  agentName,
  onTaskClick,
}: AgentWorkPanelProps): JSX.Element {
  const { handleIssueClick } = useWorkspaceViewActions();
  const dispatchClick = onTaskClick ?? handleIssueClick;
  const issueStore = useIssueStoreInstance();
  const issuesMap = useStore(issueStore, (s) => s.issuesMap);
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  // Detect the agent's "active epic": parse its current-task ID from status,
  // look the task up, walk to its parent. When set, the panel focuses on that
  // single epic and all its child tasks (not just the agent's own) so the
  // user sees the full DAG the agent is working under.
  const activeEpicId = useMemo<string | undefined>(() => {
    if (!agentName) return undefined;
    const a = agents.find((x) => x.name === agentName);
    if (!a) return undefined;
    const parsed = parseLoomStatus(a.status ?? "");
    if (!parsed.taskId) return undefined;
    const task = issuesMap.get(parsed.taskId);
    return task?.parent || undefined;
  }, [agentName, agents, issuesMap]);

  // Three rendering modes:
  //   1. activeEpic — agent is working a task; zoom into that epic and show
  //      ALL its child tasks (regardless of assignee) so siblings/blockers
  //      are visible.
  //   2. agent-scoped — agent is idle but has tasks assigned; group those by
  //      epic, fall back to "Unassigned" for orphans. (Original F2 behavior.)
  //   3. workspace-wide — agent is idle and has nothing assigned; show all
  //      workspace issues grouped by epic so the panel still has useful
  //      context. Triggered when groupAgentTasksByEpic returns 0 tasks.
  const { groups, counts, totalTasks, mode } = useMemo(() => {
    if (activeEpicId) {
      const v = scopeToEpic(issuesMap, activeEpicId);
      return { ...v, mode: "epic" as const };
    }
    const agentScoped = groupAgentTasksByEpic(issuesMap, agentName);
    if (agentScoped.totalTasks > 0) {
      return { ...agentScoped, mode: "agent" as const };
    }
    const wide = groupAllByEpic(issuesMap);
    return { ...wide, mode: "workspace" as const };
  }, [issuesMap, agentName, activeEpicId]);
  const focused = mode === "epic";

  if (!agentName) {
    return (
      <div className={styles.empty}>
        Select an agent from the rail to see their work.
      </div>
    );
  }

  const overallPct = totalTasks > 0 ? (counts.done / totalTasks) * 100 : 0;

  return (
    <aside className={styles.panel} aria-label="Agent work">
      <div className={styles.header}>
        {focused && groups[0] ? (
          <>
            <div className={styles.activeEpicTag}>
              <span aria-hidden="true" className={styles.activeEpicTagDot} />
              Active epic
            </div>
            <div className={styles.activeEpicTitle}>{groups[0].epicTitle}</div>
          </>
        ) : (
          <div className={styles.label}>
            {mode === "workspace"
              ? `Workspace queue · ${groups.length} epic${groups.length === 1 ? "" : "s"} · ${totalTasks} task${totalTasks === 1 ? "" : "s"}`
              : `${agentName}'s work · ${groups.length} epic${groups.length === 1 ? "" : "s"} · ${totalTasks} task${totalTasks === 1 ? "" : "s"}`}
          </div>
        )}

        <div className={styles.countRow}>
          <CountChip label={`${counts.done} done`} />
          <CountChip label={`${counts.active} active`} />
          <CountChip label={`${counts.open} queued`} />
          <CountChip label={`${counts.blocked} blocked`} />
        </div>

        <div className={styles.progressBar}>
          <div
            className={styles.progressBarFill}
            style={{ width: `${Math.min(100, Math.max(0, overallPct))}%` }}
          />
        </div>
      </div>

      <div className={styles.body}>
        {totalTasks === 0 ? (
          <div className={styles.emptyState}>
            {focused
              ? "Active epic has no child tasks."
              : mode === "workspace"
                ? "No issues in this workspace yet."
                : `No tasks assigned to ${agentName} yet.`}
          </div>
        ) : (
          groups.map((group) => (
            <EpicGroupCard
              key={group.epicId}
              group={group}
              onTaskClick={(task) => dispatchClick(task)}
            />
          ))
        )}
      </div>
    </aside>
  );
}

function EpicGroupCard({
  group,
  onTaskClick,
}: {
  group: EpicGroup;
  onTaskClick: (task: Issue) => void;
}): JSX.Element {
  const pct =
    group.totalCount > 0 ? (group.doneCount / group.totalCount) * 100 : 0;
  return (
    <div className={styles.group}>
      <div className={styles.groupHeader}>
        <span className={styles.epicChip}>EPIC</span>
        <span className={styles.epicTitle}>{group.epicTitle}</span>
        <span className={styles.epicCount}>
          {group.doneCount}/{group.totalCount}
        </span>
      </div>
      <div className={styles.epicProgress}>
        <div
          className={styles.epicProgressFill}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </div>
      {group.tasks.map((task) => (
        <TaskCard
          key={task.id}
          task={task}
          onClick={() => onTaskClick(task)}
        />
      ))}
    </div>
  );
}

function TaskCard({
  task,
  onClick,
}: {
  task: Issue;
  onClick: () => void;
}): JSX.Element {
  const status = (task.status ?? "open").toLowerCase();
  const glyph = STATUS_GLYPH[status] ?? STATUS_GLYPH["open"];
  const priority = task.priority ?? 2;
  const priorityClass = PRIORITY_CLASS[priority] ?? PRIORITY_CLASS[2];
  const priorityLabel = PRIORITY_LABEL[priority] ?? "P2";
  return (
    <button
      type="button"
      onClick={onClick}
      data-status={status}
      data-priority={priority}
      className={styles.taskCard}
    >
      <div className={styles.taskMeta}>
        <span className={styles.statusGlyph} aria-hidden="true">
          {glyph}
        </span>
        <span className={styles.taskId}>{task.id}</span>
        <span className={`${styles.priorityChip} ${priorityClass ?? ""}`}>
          {priorityLabel}
        </span>
      </div>
      <div className={styles.taskTitle}>{task.title}</div>
    </button>
  );
}

function CountChip({ label }: { label: string }): JSX.Element {
  return <span className={styles.countChip}>{label}</span>;
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
  const counts: Counts = { active: 0, done: 0, open: 0, blocked: 0 };
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
export function groupAllByEpic(
  issuesMap: Map<string, Issue>,
): { groups: EpicGroup[]; counts: Counts; totalTasks: number } {
  const counts: Counts = { active: 0, done: 0, open: 0, blocked: 0 };
  const byEpic = new Map<string, Issue[]>();
  let totalTasks = 0;

  for (const issue of issuesMap.values()) {
    if (issue.issue_type === "epic") continue;
    totalTasks++;
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "closed" || status === "done") counts.done++;
    else if (status === "blocked") counts.blocked++;
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
 * scopeToEpic returns a single-group view focused on one epic and ALL its
 * child tasks (regardless of assignee). Used when the selected agent has an
 * active task — the panel zooms into the broader DAG the agent is contributing
 * to so the user sees siblings, blockers, and downstream work in context.
 */
export function scopeToEpic(
  issuesMap: Map<string, Issue>,
  epicId: string,
): { groups: EpicGroup[]; counts: Counts; totalTasks: number } {
  const counts: Counts = { active: 0, done: 0, open: 0, blocked: 0 };
  const tasks: Issue[] = [];

  for (const issue of issuesMap.values()) {
    if (issue.parent !== epicId) continue;
    if (issue.issue_type === "epic") continue;
    tasks.push(issue);
    const status = (issue.status ?? "open").toLowerCase();
    if (status === "in_progress" || status === "active") counts.active++;
    else if (status === "closed" || status === "done") counts.done++;
    else if (status === "blocked") counts.blocked++;
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
