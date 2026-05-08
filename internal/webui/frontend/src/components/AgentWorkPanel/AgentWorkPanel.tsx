/**
 * AgentWorkPanel — Direction J right panel.
 *
 * Shows the selected agent's work: tasks where assignee == agent.name,
 * grouped by parent (epic), with per-epic progress bars and state-colored
 * task cards. Click a task → navigate to the issue's full-page detail.
 *
 * Data source: existing IssueStore (no new endpoints — this is pure
 * client-side filtering + grouping over the issues already fetched for the
 * workspace).
 */

import { useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useStore } from "zustand";

import { useAgentStoreInstance, useIssueStoreInstance } from "@/hooks";
import { type Issue, parseLoomStatus } from "@/types";

interface AgentWorkPanelProps {
  agentName: string | undefined;
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

const STATE_STYLE: Record<
  string,
  { bg: string; border: string; glyph: string; faded: boolean }
> = {
  active: { bg: "#fde4d9", border: "#c96442", glyph: "▸", faded: false },
  in_progress: { bg: "#fde4d9", border: "#c96442", glyph: "▸", faded: false },
  open: { bg: "#fff", border: "#333", glyph: "○", faded: false },
  ready: { bg: "#fff", border: "#333", glyph: "○", faded: false },
  blocked: { bg: "#f3c8c8", border: "#d14545", glyph: "⨯", faded: false },
  review: { bg: "#d9e4f2", border: "#4477aa", glyph: "◔", faded: false },
  done: { bg: "#d8ecde", border: "#3aa76d", glyph: "✓", faded: true },
  closed: { bg: "#d8ecde", border: "#3aa76d", glyph: "✓", faded: true },
};

const PRIORITY_STYLE: Record<
  number,
  { label: string; color: string; border: string }
> = {
  0: { label: "P0", color: "#d14545", border: "#d14545" },
  1: { label: "P1", color: "#d99700", border: "#d99700" },
  2: { label: "P2", color: "#555", border: "#888" },
  3: { label: "P3", color: "#555", border: "#888" },
  4: { label: "P4", color: "#888", border: "#aaa" },
};

export function AgentWorkPanel({ agentName }: AgentWorkPanelProps): JSX.Element {
  const { workspaceId = "" } = useParams<{ workspaceId: string }>();
  const navigate = useNavigate();
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

  const { groups, counts, totalTasks, focused } = useMemo(() => {
    if (activeEpicId) {
      const focusedView = scopeToEpic(issuesMap, activeEpicId);
      return { ...focusedView, focused: true };
    }
    const fallback = groupAgentTasksByEpic(issuesMap, agentName);
    return { ...fallback, focused: false };
  }, [issuesMap, agentName, activeEpicId]);

  if (!agentName) {
    return (
      <div
        style={{
          width: 360,
          flexShrink: 0,
          padding: 16,
          color: "var(--color-text-muted, #888)",
          background: "var(--color-bg-soft, #faf8f3)",
        }}
      >
        <div style={{ fontSize: 12 }}>
          Select an agent from the rail to see their work.
        </div>
      </div>
    );
  }

  const overallPct = totalTasks > 0 ? (counts.done / totalTasks) * 100 : 0;

  return (
    <div
      style={{
        width: 360,
        flexShrink: 0,
        background: "var(--color-bg-soft, #faf8f3)",
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        borderLeft: "1px solid var(--color-border, #ddd)",
      }}
    >
      <div
        style={{
          padding: "10px 14px",
          borderBottom: "1px solid var(--color-border, #ddd)",
        }}
      >
        {focused && groups[0] ? (
          <>
            <div
              style={{
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: 0.4,
                textTransform: "uppercase",
                color: "#c96442",
                display: "flex",
                alignItems: "center",
                gap: 6,
              }}
            >
              <span
                aria-hidden="true"
                style={{
                  width: 6,
                  height: 6,
                  borderRadius: "50%",
                  background: "#c96442",
                  display: "inline-block",
                }}
              />
              Active epic
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, marginTop: 2 }}>
              {groups[0].epicTitle}
            </div>
          </>
        ) : (
          <div
            style={{
              fontSize: 11,
              fontWeight: 700,
              letterSpacing: 0.4,
              textTransform: "uppercase",
              color: "var(--color-text-muted, #666)",
            }}
          >
            {agentName}'s work · {groups.length} epic{groups.length === 1 ? "" : "s"}
            {" · "}
            {totalTasks} task{totalTasks === 1 ? "" : "s"}
          </div>
        )}

        <div style={{ display: "flex", gap: 4, marginTop: 6, flexWrap: "wrap" }}>
          <CountChip label={`${counts.done} done`} />
          <CountChip label={`${counts.active} active`} />
          <CountChip label={`${counts.open} queued`} />
          <CountChip label={`${counts.blocked} blocked`} />
        </div>

        <ProgressBar pct={overallPct} color="#3aa76d" />
      </div>

      <div
        style={{
          flex: 1,
          overflow: "auto",
          padding: 10,
          display: "flex",
          flexDirection: "column",
          gap: 12,
          minHeight: 0,
        }}
      >
        {totalTasks === 0 ? (
          <div
            style={{
              fontSize: 12,
              color: "var(--color-text-muted, #888)",
              textAlign: "center",
              padding: 24,
            }}
          >
            {focused
              ? "Active epic has no child tasks."
              : `No tasks assigned to ${agentName} yet.`}
          </div>
        ) : (
          groups.map((group) => (
            <EpicGroupCard
              key={group.epicId}
              group={group}
              onTaskClick={(taskId) =>
                navigate(`/ws/${workspaceId}/issues/${encodeURIComponent(taskId)}`)
              }
            />
          ))
        )}
      </div>
    </div>
  );
}

function EpicGroupCard({
  group,
  onTaskClick,
}: {
  group: EpicGroup;
  onTaskClick: (taskId: string) => void;
}): JSX.Element {
  const pct =
    group.totalCount > 0 ? (group.doneCount / group.totalCount) * 100 : 0;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            letterSpacing: 0.4,
            background: "var(--color-bg, #fff)",
            border: "1px solid #888",
            borderRadius: 3,
            padding: "1px 6px",
          }}
        >
          EPIC
        </span>
        <span style={{ fontSize: 13, fontWeight: 700, flex: 1, minWidth: 0 }}>
          {group.epicTitle}
        </span>
        <span style={{ fontSize: 11, color: "var(--color-text-muted, #666)" }}>
          {group.doneCount}/{group.totalCount}
        </span>
      </div>
      <ProgressBar pct={pct} color="#3aa76d" thin />
      {group.tasks.map((task) => (
        <TaskCard
          key={task.id}
          task={task}
          onClick={() => onTaskClick(task.id)}
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
  const style = STATE_STYLE[status] ?? STATE_STYLE["open"];
  const prio = PRIORITY_STYLE[task.priority ?? 2] ?? PRIORITY_STYLE[2];
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        textAlign: "left",
        padding: "5px 7px",
        background: style?.bg ?? "#fff",
        border: `1px solid ${style?.border ?? "#333"}`,
        borderRadius: 3,
        cursor: "pointer",
        opacity: style?.faded ? 0.75 : 1,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{ fontSize: 11 }}>{style?.glyph ?? "○"}</span>
        <span
          style={{
            fontFamily: "var(--font-mono, ui-monospace, monospace)",
            fontSize: 9,
            color: "var(--color-text-muted, #888)",
          }}
        >
          {task.id}
        </span>
        <span
          style={{
            fontSize: 9,
            padding: "0 5px",
            borderRadius: 2,
            border: `1px solid ${prio?.border ?? "#888"}`,
            color: prio?.color ?? "#555",
          }}
        >
          {prio?.label ?? "P2"}
        </span>
      </div>
      <div
        style={{
          fontSize: 11,
          marginTop: 2,
          textDecoration: status === "closed" || status === "done" ? "line-through" : "none",
        }}
      >
        {task.title}
      </div>
    </button>
  );
}

function CountChip({ label }: { label: string }): JSX.Element {
  return (
    <span
      style={{
        fontSize: 10,
        padding: "1px 6px",
        border: "1px solid #888",
        borderRadius: 3,
        color: "var(--color-text, #333)",
      }}
    >
      {label}
    </span>
  );
}

function ProgressBar({
  pct,
  color,
  thin = false,
}: {
  pct: number;
  color: string;
  thin?: boolean;
}): JSX.Element {
  return (
    <div
      style={{
        marginTop: thin ? 2 : 8,
        height: thin ? 4 : 6,
        background: "#ececea",
        borderRadius: 3,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          width: `${Math.min(100, Math.max(0, pct))}%`,
          height: "100%",
          background: color,
        }}
      />
    </div>
  );
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
