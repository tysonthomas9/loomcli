/**
 * RunningSection displays actively running tasks grouped by parent epic.
 * Uses agent lock status to determine which specific task each agent is on,
 * then looks up parent epic from the issue database.
 * Only visible when at least one agent is working or planning.
 */

import { useMemo } from "react";
import { useStore } from "zustand";

import {
  useAgentStoreInstance,
  useWorkspaceRepos,
  useWorkspaceTree,
} from "@/hooks";
import { parseLoomStatus } from "@/types/agent";
import type { Issue } from "@/types";

import styles from "./RunningSection.module.css";

export interface RunningSectionProps {
  onSelect?: ((issueId: string) => void) | undefined;
}

interface RunningTask {
  task: Issue;
  agentName: string;
  duration: string;
}

interface EpicGroup {
  epic: Issue;
  completedCount: number;
  totalCount: number;
  runningTasks: RunningTask[];
}

export function RunningSection({
  onSelect,
}: RunningSectionProps): JSX.Element | null {
  const { workspace } = useWorkspaceRepos();
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const agentTasks = useStore(agentStore, (s) => s.agentTasks);

  // Fetch all workspace issues to resolve parent epics.
  const { epics: allEpics, orphanTasks: allOrphans } = useWorkspaceTree(
    workspace?.name ?? "",
    "all",
    undefined,
    true,
  );

  // Build map of taskId → { agentName, duration } from working agents.
  const activeTaskMap = useMemo(() => {
    const map = new Map<string, { agentName: string; duration: string }>();
    for (const agent of agents) {
      const parsed = parseLoomStatus(agent.status);
      if (
        (parsed.type === "working" || parsed.type === "planning") &&
        parsed.taskId
      ) {
        map.set(parsed.taskId, {
          agentName: agent.name,
          duration: parsed.duration ?? "",
        });
      }
    }
    return map;
  }, [agents]);

  // Build a flat lookup: taskId → Issue for all tasks across epics + orphans.
  const taskById = useMemo(() => {
    const map = new Map<string, Issue>();
    for (const entry of allEpics) {
      for (const t of entry.tasks) {
        map.set(t.id, t);
      }
    }
    for (const t of allOrphans) {
      map.set(t.id, t);
    }
    return map;
  }, [allEpics, allOrphans]);

  // Group running tasks under parent epics; collect orphans separately.
  const { epicGroups, orphanRunning } = useMemo(() => {
    const epicMap = new Map<string, EpicGroup>();
    const orphans: RunningTask[] = [];

    for (const [taskId, agentInfo] of activeTaskMap) {
      const task = taskById.get(taskId);
      const runningTask: RunningTask = {
        task:
          task ??
          ({
            id: taskId,
            title: agentTasks[agentInfo.agentName]?.title ?? taskId,
          } as Issue),
        agentName: agentInfo.agentName,
        duration: agentInfo.duration,
      };

      // Find parent epic
      const parentEpic = allEpics.find((e) =>
        e.tasks.some((t) => t.id === taskId),
      );

      if (parentEpic) {
        let group = epicMap.get(parentEpic.epic.id);
        if (!group) {
          group = {
            epic: parentEpic.epic,
            completedCount: parentEpic.tasks.filter(
              (t) => t.status === "closed",
            ).length,
            totalCount: parentEpic.tasks.length,
            runningTasks: [],
          };
          epicMap.set(parentEpic.epic.id, group);
        }
        group.runningTasks.push(runningTask);
      } else {
        orphans.push(runningTask);
      }
    }

    return {
      epicGroups: [...epicMap.values()],
      orphanRunning: orphans,
    };
  }, [activeTaskMap, taskById, allEpics, agentTasks]);

  if (epicGroups.length === 0 && orphanRunning.length === 0) return null;

  return (
    <div className={styles.section}>
      <div className={styles.header}>Running</div>
      <div className={styles.list}>
        {epicGroups.map(
          ({ epic, runningTasks, completedCount, totalCount }) => (
            <div key={epic.id} className={styles.epicGroup}>
              <div className={styles.epicRow}>
                <span className={styles.epicIcon}>&#x25CE;</span>
                <span className={styles.epicTitle}>{epic.title}</span>
                <span className={styles.epicProgress}>
                  {completedCount}/{totalCount}
                </span>
              </div>
              {runningTasks.map((rt) => (
                <button
                  key={rt.task.id}
                  type="button"
                  className={styles.taskRow}
                  onClick={() => onSelect?.(rt.task.id)}
                >
                  <span className={styles.taskIcon}>&#x25D0;</span>
                  <span className={styles.taskTitle}>{rt.task.title}</span>
                  {rt.duration && (
                    <span className={styles.taskElapsed}>{rt.duration}</span>
                  )}
                </button>
              ))}
            </div>
          ),
        )}
        {orphanRunning.map((rt) => (
          <div key={rt.task.id} className={styles.epicGroup}>
            <button
              type="button"
              className={styles.taskRowOrphan}
              onClick={() => onSelect?.(rt.task.id)}
            >
              <span className={styles.taskIcon}>&#x25D0;</span>
              <span className={styles.taskTitle}>{rt.task.title}</span>
              {rt.duration && (
                <span className={styles.taskElapsed}>{rt.duration}</span>
              )}
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
