/**
 * ChangesTab — the agent's one Changes tab (D43: Git and Diff merged).
 * A task agent sees its task, status and start point above its changed files;
 * a lead sees how many tasks are approved with a link to their PRs, then its
 * own working-area changes. Clicking a file expands its diff inline.
 */

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { getTaskStartedFrom } from "@/hooks/api";
import type { TaskStartedFrom } from "@/api/git/revisions";
import { useDiff } from "@/hooks/terminal";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { isLeadRole } from "@/utils/agentRole";
import { formatStatusLabel } from "@/utils/issue";

import { DiffFileRow } from "./DiffFileRow";
import { DiffFileViewer } from "./DiffFileViewer";
import styles from "./DiffTab.module.css";

interface ChangesTabProps {
  agent: LoomAgentStatus;
  isActive?: boolean;
  /** Workspace tasks, for the agent's task and the lead's approved count. */
  issues: Issue[];
  /** The lead a task starts from, when the workspace has exactly one. */
  lead?: string;
  /** Opens the task on its own Changes tab. */
  onOpenTaskChanges?: (task: Issue) => void;
}

const LIVE_REFRESH_MS = 5000;

/** An approved task closes with "Approved: code applied" (taskreview). */
function isApproved(issue: Issue): boolean {
  return (
    issue.status === "closed" &&
    (issue.close_reason ?? "").startsWith("Approved")
  );
}

function statusPill(task: Issue): string {
  if (task.status === "in_progress") return "Working";
  if (task.status === "review") return "Waiting for review";
  if (isApproved(task)) return "Approved";
  return formatStatusLabel(task.status ?? "open");
}

export function ChangesTab({
  agent,
  isActive,
  issues,
  lead,
  onOpenTaskChanges,
}: ChangesTabProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const parsed = parseLoomStatus(agent.status);
  const isLead = isLeadRole(agent.role);
  const taskId = isLead
    ? ""
    : agent.current_task_id ||
      agent.active_task_id ||
      parsed.taskId ||
      agent.task_id ||
      "";
  const task = issues.find((i) => i.id === taskId);
  const working =
    agent.live_status === "working" ||
    parsed.type === "working" ||
    parsed.type === "planning";

  const [startedFrom, setStartedFrom] = useState<TaskStartedFrom | null>(null);
  useEffect(() => {
    setStartedFrom(null);
    if (!taskId || isActive === false) return;
    let cancelled = false;
    getTaskStartedFrom(workspaceId, taskId, lead)
      .then((s) => {
        if (!cancelled) setStartedFrom(s);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [workspaceId, taskId, lead, isActive]);

  const {
    files,
    isLoading,
    error,
    patchErrors,
    viewedFiles,
    markViewed,
    patchCache,
    fetchPatch,
    summaryStats,
  } = useDiff({
    agentName: agent.name,
    enabled: (isActive ?? true) && (isLead || !!taskId),
    commitSignal: agent.ahead,
    ...(working ? { refreshMs: LIVE_REFRESH_MS } : {}),
  });

  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
  useEffect(() => {
    setExpandedFiles(new Set());
  }, [agent.name, agent.ahead]);

  function handleToggleExpand(path: string) {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
        fetchPatch(path);
      }
      return next;
    });
  }

  if (!isLead && !taskId) {
    return (
      <div className={styles.emptyState} data-testid="changes-no-task">
        No task assigned
      </div>
    );
  }

  let header: JSX.Element;
  if (isLead) {
    const approved = issues.filter(
      (i) => isApproved(i) && (!agent.parent || i.parent === agent.parent),
    ).length;
    header = (
      <div className={styles.changesHeader} data-testid="changes-lead-summary">
        <span>
          {approved} task{approved === 1 ? "" : "s"} approved
        </span>
        <span aria-hidden="true"> · </span>
        <Link
          to={`/ws/${encodeURIComponent(workspaceId)}/prs`}
          data-testid="changes-open-prs"
        >
          open PRs →
        </Link>
      </div>
    );
  } else {
    const blocker =
      startedFrom?.kind === "blocker"
        ? issues.find((i) => i.id === startedFrom.task)
        : undefined;
    const startLabel =
      startedFrom?.kind === "blocker"
        ? blocker
          ? `${blocker.id} ${blocker.title}`
          : (startedFrom.task ?? "its blocker task")
        : startedFrom?.kind === "lead"
          ? "the lead's latest work"
          : "trunk";
    const done = task?.status === "review" || task?.status === "closed";
    header = (
      <div className={styles.changesHeader} data-testid="changes-task-header">
        <div className={styles.changesTitleRow}>
          <span className={styles.changesTaskKey}>{taskId}</span>
          <span className={styles.changesTaskTitle}>{task?.title}</span>
          {task && (
            <span
              className={styles.changesPill}
              data-testid="changes-task-status"
            >
              {statusPill(task)}
            </span>
          )}
        </div>
        {startedFrom && (
          <div
            className={styles.changesStartedFrom}
            data-testid="changes-started-from"
          >
            Started from: {startLabel}
          </div>
        )}
        {done && task && onOpenTaskChanges && (
          <button
            type="button"
            className={styles.changesOpenTask}
            onClick={() => onOpenTaskChanges(task)}
            data-testid="changes-open-task"
          >
            {"Open the task's Changes →"}
          </button>
        )}
      </div>
    );
  }

  let body: JSX.Element | null;
  if (isLoading && files.length === 0) {
    body = <div className={styles.loading}>Loading changes…</div>;
  } else if (error && files.length === 0) {
    body = <div className={styles.error}>{error.message}</div>;
  } else if (files.length === 0) {
    body = isLead ? null : (
      <div className={styles.emptyState}>No changes yet</div>
    );
  } else {
    body = (
      <>
        <div className={styles.summaryBar}>
          <span className={styles.summaryCount}>
            {summaryStats.filesChanged} file
            {summaryStats.filesChanged !== 1 ? "s" : ""} changed
          </span>
          {summaryStats.additions > 0 && (
            <span className={styles.statAdd}>+{summaryStats.additions}</span>
          )}
          {summaryStats.deletions > 0 && (
            <span className={styles.statDel}>-{summaryStats.deletions}</span>
          )}
        </div>
        <div className={styles.fileList} data-testid="changes-file-list">
          {files.map((file) => {
            const isExpanded = expandedFiles.has(file.path);
            const cachedPatch = patchCache.get(file.path) ?? null;
            return (
              <div key={file.path}>
                <DiffFileRow
                  file={file}
                  isExpanded={isExpanded}
                  isViewed={viewedFiles.has(file.path)}
                  onToggleExpand={() => handleToggleExpand(file.path)}
                  onToggleViewed={() => markViewed(file.path)}
                />
                {isExpanded && (
                  <DiffFileViewer
                    patch={cachedPatch}
                    isLoading={!cachedPatch && !patchErrors.has(file.path)}
                    error={patchErrors.get(file.path)?.message}
                  />
                )}
              </div>
            );
          })}
        </div>
      </>
    );
  }

  return (
    <div data-testid="changes-tab">
      {header}
      {body}
    </div>
  );
}
