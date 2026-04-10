/**
 * RunningSection displays in-progress tasks grouped by parent epic.
 * Only visible when tasks are actively running (in_progress or review).
 * Shows epic name + progress fraction and each running task with assignee.
 */

import { useMemo } from "react";

import { useAgentContext } from "@/hooks";
import { useWorkspaceTree } from "@/hooks/useWorkspaceTree";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import { useWorkspaceRepos } from "@/hooks/useWorkspaceRepos";
import { parseLoomStatus } from "@/types/agent";

import styles from "./RunningSection.module.css";

export interface RunningSectionProps {
  onSelect?: ((issueId: string) => void) | undefined;
  onTaskTerminalOpen?:
    | ((issueId: string, agentName: string) => void)
    | undefined;
}

const ACTIVE_STATUSES = new Set(["in_progress", "review"]);

/** Format elapsed time from an ISO date string to a compact label like "5m", "2h". */
function formatElapsed(isoDate: string): string {
  const diffMs = Date.now() - new Date(isoDate).getTime();
  if (diffMs < 0) return "";
  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

export function RunningSection({
  onSelect,
  onTaskTerminalOpen,
}: RunningSectionProps): JSX.Element | null {
  const { workspaceId } = useWorkspaceContext();
  const { workspace, repos } = useWorkspaceRepos();
  const { agents } = useAgentContext();
  const repoNames = useMemo(() => repos.map((r) => r.name), [repos]);

  const { epics: allEpics } = useWorkspaceTree(
    workspace?.name ?? "",
    "all",
    repoNames,
    workspaceId,
  );

  // Build set of agent names that are actively working/planning.
  const workingAgents = useMemo(() => {
    const names = new Set<string>();
    for (const agent of agents) {
      const parsed = parseLoomStatus(agent.status);
      if (parsed.type === "working" || parsed.type === "planning") {
        names.add(agent.name);
      }
    }
    return names;
  }, [agents]);

  // Filter to epics with tasks whose assigned agent is actively working.
  const runningEpics = useMemo(() => {
    return allEpics
      .map((entry) => {
        const activeTasks = entry.tasks.filter(
          (t) =>
            ACTIVE_STATUSES.has(t.status ?? "") &&
            t.assignee != null &&
            workingAgents.has(t.assignee),
        );
        const completedCount = entry.tasks.filter(
          (t) => t.status === "closed",
        ).length;
        return {
          epic: entry.epic,
          activeTasks,
          completedCount,
          totalCount: entry.tasks.length,
        };
      })
      .filter((e) => e.activeTasks.length > 0);
  }, [allEpics, workingAgents]);

  if (runningEpics.length === 0) return null;

  return (
    <div className={styles.section}>
      <div className={styles.header}>Running</div>
      <div className={styles.list}>
        {runningEpics.map(
          ({ epic, activeTasks, completedCount, totalCount }) => (
            <div key={epic.id} className={styles.epicGroup}>
              <div className={styles.epicRow}>
                <span className={styles.epicIcon}>&#x25CE;</span>
                <span className={styles.epicTitle}>{epic.title}</span>
                <span className={styles.epicProgress}>
                  {completedCount}/{totalCount}
                </span>
              </div>
              {activeTasks.map((task) => {
                const elapsed = formatElapsed(task.updated_at);
                const handleClick = () => {
                  if (task.assignee && onTaskTerminalOpen) {
                    onTaskTerminalOpen(task.id, task.assignee);
                  } else {
                    onSelect?.(task.id);
                  }
                };
                return (
                  <button
                    key={task.id}
                    type="button"
                    className={styles.taskRow}
                    onClick={handleClick}
                  >
                    <span className={styles.taskIcon}>&#x25D0;</span>
                    <span className={styles.taskTitle}>{task.title}</span>
                    {task.assignee && (
                      <span className={styles.taskAssignee}>
                        {task.assignee}
                      </span>
                    )}
                    {elapsed && (
                      <span className={styles.taskElapsed}>{elapsed}</span>
                    )}
                  </button>
                );
              })}
            </div>
          ),
        )}
      </div>
    </div>
  );
}
