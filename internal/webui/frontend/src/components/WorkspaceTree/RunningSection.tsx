/**
 * RunningSection displays actively running tasks grouped by parent epic.
 * Uses agent lock status to determine which specific task each agent is on,
 * then looks up parent epic from the issue database.
 * Only visible when at least one agent is working or planning.
 */

import { useMemo } from "react";
import { useStore } from "zustand";

import {
  useActiveIssueLookups,
  useAgentStoreInstance,
  useWorkspaceContext,
  useWorkspaceTree,
} from "@/hooks";
import { parseLoomStatus } from "@/types/agent";
import type { Issue, LoomTaskInfo } from "@/types";

import styles from "./RunningSection.module.css";

export interface RunningSectionProps {
  onSelect?: ((issueId: string) => void) | undefined;
}

interface RunningTask {
  task: Issue;
  agentName: string;
  duration: string;
  issueAvailability: "available" | "missing" | "unknown";
}

interface EpicGroup {
  epic: Issue;
  completedCount: number;
  totalCount: number;
  runningTasks: RunningTask[];
}

type ActiveTaskMap = Map<string, { agentName: string; duration: string }>;

export function RunningSection({
  onSelect,
}: RunningSectionProps): JSX.Element | null {
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const agentTasks = useStore(agentStore, (s) => s.agentTasks);

  // Build map of taskId → { agentName, duration } from working agents.
  const activeTaskMap = useMemo(() => {
    const map: ActiveTaskMap = new Map();
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

  if (activeTaskMap.size === 0) return null;

  return (
    <RunningSectionContent
      activeTaskMap={activeTaskMap}
      agentTasks={agentTasks}
      onSelect={onSelect}
    />
  );
}

interface RunningSectionContentProps extends RunningSectionProps {
  activeTaskMap: ActiveTaskMap;
  agentTasks: Record<string, LoomTaskInfo>;
}

function RunningSectionContent({
  activeTaskMap,
  agentTasks,
  onSelect,
}: RunningSectionContentProps): JSX.Element | null {
  const { workspace, workspaceId } = useWorkspaceContext();

  // Fetch all workspace issues only when there is an active task to place.
  const { epics: allEpics, orphanTasks: allOrphans } = useWorkspaceTree(
    workspace?.name ?? "",
    "all",
    undefined,
    true,
  );

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

  // The workspace tree is a presentation projection: it excludes non-task
  // issue types and its list endpoint is capped. Resolve every active ID that
  // is absent from that projection through the authoritative single-issue
  // endpoint before deciding whether the issue was deleted.
  const missingTaskIDs = useMemo(
    () =>
      [...activeTaskMap.keys()]
        .filter((taskId) => !taskById.has(taskId))
        .sort(),
    [activeTaskMap, taskById],
  );
  const { results: directLookups } = useActiveIssueLookups(
    workspaceId,
    missingTaskIDs,
  );

  // Group running tasks under parent epics; collect orphans separately.
  const { epicGroups, orphanRunning } = useMemo(() => {
    const epicMap = new Map<string, EpicGroup>();
    const orphans: RunningTask[] = [];

    for (const [taskId, agentInfo] of activeTaskMap) {
      let task = taskById.get(taskId);
      const directLookup = directLookups.get(taskId);
      if (!task && directLookup?.status === "found") {
        task = directLookup.issue;
      }

      // Agent status can outlive its issue while execution winds down. Keep the
      // execution visible, but only label it deleted after an authoritative
      // direct 404. Loading and transport failures stay explicitly unknown.
      if (!task) {
        orphans.push({
          task: {
            id: taskId,
            title: agentTasks[agentInfo.agentName]?.title ?? taskId,
          } as Issue,
          agentName: agentInfo.agentName,
          duration: agentInfo.duration,
          issueAvailability:
            directLookup?.status === "missing" ? "missing" : "unknown",
        });
        continue;
      }
      const runningTask: RunningTask = {
        task,
        agentName: agentInfo.agentName,
        duration: agentInfo.duration,
        issueAvailability: "available",
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
  }, [activeTaskMap, taskById, allEpics, agentTasks, directLookups]);

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
              disabled={rt.issueAvailability !== "available"}
              title={
                rt.issueAvailability === "missing"
                  ? "Issue no longer exists; the agent may still be running"
                  : rt.issueAvailability === "unknown"
                    ? "Issue lookup is still pending or unavailable"
                    : undefined
              }
              onClick={() => {
                if (rt.issueAvailability === "available") {
                  onSelect?.(rt.task.id);
                }
              }}
            >
              <span className={styles.taskIcon}>&#x25D0;</span>
              <span className={styles.taskTitle}>{rt.task.title}</span>
              {rt.issueAvailability === "missing" && (
                <span className={styles.taskUnavailable}>
                  issue unavailable
                </span>
              )}
              {rt.issueAvailability === "unknown" && (
                <span className={styles.taskUnavailable}>
                  issue status unknown
                </span>
              )}
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
