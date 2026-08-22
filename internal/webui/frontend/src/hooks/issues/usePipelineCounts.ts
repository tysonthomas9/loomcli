import { useMemo } from "react";

import type { RepoInfo } from "@/api/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import {
  effectiveAgentStatus,
  parseLoomStatus,
  resolveAgentForTask,
} from "@/types";
import { targetBranchForSource } from "@/utils/workspace/repoPresentation";

export interface AwaitingMergeGroup {
  branch: string;
  count: number;
}

export interface PipelineCounts {
  backlog: number;
  designing: number;
  awaitingApproval: number;
  building: number;
  deferred: number;
  awaitingMerge: AwaitingMergeGroup[];
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
  repos: readonly RepoInfo[] = [],
): PipelineCounts {
  const awaitingMergeByBranch = new Map<string, number>();
  for (const agent of agents) {
    if (agent.ahead <= 0) continue;
    const branch = targetBranchForSource(repos, agent.repo);
    awaitingMergeByBranch.set(
      branch,
      (awaitingMergeByBranch.get(branch) ?? 0) + 1,
    );
  }

  const counts: PipelineCounts = {
    backlog: 0,
    designing: 0,
    awaitingApproval: 0,
    building: 0,
    deferred: 0,
    awaitingMerge: [...awaitingMergeByBranch].map(([branch, count]) => ({
      branch,
      count,
    })),
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
  repos: readonly RepoInfo[],
): PipelineCounts {
  return useMemo(
    () => derivePipeline(issues, agents, repos),
    [issues, agents, repos],
  );
}
