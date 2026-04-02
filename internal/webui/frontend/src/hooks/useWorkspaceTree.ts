/**
 * useWorkspaceTree - React hook for building an epic/task tree from workspace issues.
 * Fetches issues filtered by source repos, groups tasks under parent epics,
 * and applies active/all filtering.
 */

import { useMemo, useState, useEffect, useCallback, useRef } from "react";

import { getKanbanIssues } from "@/api";
import type { Issue } from "@/types";

import { useWorkspaceContext } from "./useWorkspaceContext";

/** An epic with its grouped child tasks. */
export interface EpicWithTasks {
  epic: Issue;
  tasks: Issue[];
}

/** Return type for useWorkspaceTree. */
export interface UseWorkspaceTreeReturn {
  /** Epics with their child tasks grouped. */
  epics: EpicWithTasks[];
  /** Tasks that have no parent or whose parent is not in the epic set. */
  orphanTasks: Issue[];
  /** Whether data is being fetched. */
  isLoading: boolean;
  /** Fetch error, null if ok. */
  error: string | null;
  /** Manually trigger a refetch. */
  refetch: () => Promise<void>;
}

/**
 * Builds an epic/task tree for a workspace.
 *
 * @param workspaceName - workspace identifier (used as cache key)
 * @param activeFilter - 'active' shows only epics with in_progress/review tasks, 'all' shows everything
 * @param sourceRepos - repos in the workspace for issue filtering
 */
export function useWorkspaceTree(
  _workspaceName: string,
  activeFilter: "active" | "all",
  sourceRepos?: string[],
): UseWorkspaceTreeReturn {
  const { workspaceId } = useWorkspaceContext();
  const [issues, setIssues] = useState<Issue[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const shouldFetch = sourceRepos !== undefined && sourceRepos.length > 0;

  // Stable key for sourceRepos to prevent infinite re-renders from array identity changes
  const sourceReposKey = useMemo(
    () => (sourceRepos ? [...sourceRepos].sort().join(",") : ""),
    [sourceRepos],
  );

  // Store sourceRepos in a ref so fetchData always uses the latest value
  // without recreating the callback on every render
  const sourceReposRef = useRef(sourceRepos);
  sourceReposRef.current = sourceRepos;

  const fetchData = useCallback(
    async (signal?: AbortSignal) => {
      if (!shouldFetch) return;
      setIsLoading(true);
      setError(null);
      try {
        const filter = sourceReposRef.current
          ? { source_repos: sourceReposRef.current }
          : undefined;
        const reqOpts = signal ? { signal } : undefined;
        const data = await getKanbanIssues(workspaceId, filter, reqOpts);
        setIssues(data);
      } catch (err) {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : "Failed to fetch issues");
      } finally {
        setIsLoading(false);
      }
    },
    [workspaceId, shouldFetch, sourceReposKey], // eslint-disable-line react-hooks/exhaustive-deps
  );

  // Auto-fetch on mount (AbortController cancels in-flight fetch on unmount or dep change)
  useEffect(() => {
    const controller = new AbortController();
    void fetchData(controller.signal);
    return () => controller.abort();
  }, [fetchData]);

  const refetch = useCallback(() => fetchData(), [fetchData]);

  // Partition issues into epics and tasks by issue_type.
  const { epics: epicIssues, tasks: taskIssues } = useMemo(() => {
    const epics: Issue[] = [];
    const tasks: Issue[] = [];
    for (const issue of issues) {
      if (issue.issue_type === "epic") {
        epics.push(issue);
      } else if (issue.issue_type === "task") {
        tasks.push(issue);
      }
    }
    return { epics, tasks };
  }, [issues]);

  // Group tasks under parent epics; collect orphans.
  const { grouped, orphanTasks: allOrphans } = useMemo(() => {
    const epicSet = new Set(epicIssues.map((e) => e.id));
    const epicTaskMap = new Map<string, Issue[]>();
    const orphans: Issue[] = [];

    for (const epic of epicIssues) {
      epicTaskMap.set(epic.id, []);
    }

    for (const task of taskIssues) {
      if (task.parent && epicSet.has(task.parent)) {
        epicTaskMap.get(task.parent)!.push(task);
      } else {
        orphans.push(task);
      }
    }

    const result: EpicWithTasks[] = epicIssues.map((epic) => ({
      epic,
      tasks: epicTaskMap.get(epic.id) ?? [],
    }));

    return { grouped: result, orphanTasks: orphans };
  }, [epicIssues, taskIssues]);

  // Apply active filter: only show epics containing tasks with status in_progress or review.
  const { epics: filteredEpics, orphanTasks: filteredOrphans } = useMemo(() => {
    if (activeFilter === "all") {
      return { epics: grouped, orphanTasks: allOrphans };
    }

    // Per design: 'active' filter shows only tasks with active statuses,
    // not all tasks within an active epic. Users switch to 'all' for the full picture.
    const activeStatuses = new Set(["in_progress", "review"]);

    const epics = grouped
      .map((entry) => ({
        epic: entry.epic,
        tasks: entry.tasks.filter((t) => activeStatuses.has(t.status ?? "")),
      }))
      .filter((entry) => entry.tasks.length > 0);

    const orphans = allOrphans.filter((t) =>
      activeStatuses.has(t.status ?? ""),
    );

    return { epics, orphanTasks: orphans };
  }, [grouped, allOrphans, activeFilter]);

  return {
    epics: filteredEpics,
    orphanTasks: filteredOrphans,
    isLoading,
    error,
    refetch,
  };
}
