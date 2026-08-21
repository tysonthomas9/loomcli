import { useMemo } from "react";

import type { Issue, LoomAgentStatus } from "@/types";
import {
  effectiveAgentStatus,
  parseLoomStatus,
  resolveAgentForTask,
} from "@/types";

export interface PipelineCounts {
  backlog: number;
  designing: number;
  awaitingApproval: number;
  building: number;
  deferred: number;
  awaitingMerge: number;
  merged: number;
  taskCount: number;
}

function isPlanningAgent(agent: LoomAgentStatus | undefined): boolean {
  if (!agent) return false;

  return (
    agent.active_phase === "planning" ||
    agent.role === "plan" ||
    parseLoomStatus(effectiveAgentStatus(agent)).type === "planning"
  );
}

/**
 * Derive a present-state task partition from the shared issue board and live
 * agents. Awaiting merge is intentionally separate: it counts agent branches,
 * not tasks.
 */
export function derivePipeline(
  issues: readonly Issue[],
  agents: readonly LoomAgentStatus[],
): PipelineCounts {
  const counts: PipelineCounts = {
    backlog: 0,
    designing: 0,
    awaitingApproval: 0,
    building: 0,
    deferred: 0,
    awaitingMerge: agents.filter((agent) => agent.ahead > 0).length,
    merged: 0,
    taskCount: 0,
  };

  for (const issue of issues) {
    if (issue.issue_type === "epic") continue;
    counts.taskCount += 1;

    if (issue.status === "review") {
      counts.awaitingApproval += 1;
      continue;
    }

    if (issue.status === "in_progress") {
      if (isPlanningAgent(resolveAgentForTask(agents, issue.id))) {
        counts.designing += 1;
      } else {
        counts.building += 1;
      }
      continue;
    }

    if (issue.status === "blocked") {
      counts.building += 1;
      continue;
    }

    if (issue.status === "closed") {
      counts.merged += 1;
      continue;
    }

    if (issue.status === "deferred") {
      counts.deferred += 1;
      continue;
    }

    // Open is the normal backlog state. Keep any future/unknown state in the
    // backlog so the visible task rows always remain an honest partition.
    counts.backlog += 1;
  }

  return counts;
}

export function usePipelineCounts(
  issues: readonly Issue[],
  agents: readonly LoomAgentStatus[],
): PipelineCounts {
  return useMemo(() => derivePipeline(issues, agents), [issues, agents]);
}
