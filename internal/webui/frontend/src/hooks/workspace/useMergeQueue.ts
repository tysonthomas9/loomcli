import { useCallback, useEffect, useState } from "react";

import { fetchMergeQueue, type QueuedMerge } from "@/api/workspace/git";

export type { QueuedMerge };

/** The workspace's queued, running and blocked stack merges, refreshed every 10s. */
export function useMergeQueue(workspaceId: string): QueuedMerge[] {
  const [queue, setQueue] = useState<QueuedMerge[]>([]);
  const refresh = useCallback(async () => {
    try {
      setQueue(await fetchMergeQueue(workspaceId));
    } catch {
      // The queue is a status strip; the PR list keeps working without it.
    }
  }, [workspaceId]);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 10000);
    return () => window.clearInterval(timer);
  }, [refresh]);
  return queue;
}
