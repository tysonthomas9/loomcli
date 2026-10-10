import { useCallback, useEffect, useState } from "react";

import { fetchStacks, type StackCard } from "@/api/workspace/git";

export type { StackCard };

/** A stack merge that is queued or running (not blocked). */
export function mergeRunning(card: StackCard): boolean {
  return Boolean(card.merge && card.merge.phase !== "blocked");
}

/**
 * The workspace's published stacks. Refreshed every 3s while a merge runs so
 * rows update live, otherwise every 15s.
 */
export function useStacks(workspaceId: string): {
  stacks: StackCard[];
  refresh: () => Promise<void>;
} {
  const [stacks, setStacks] = useState<StackCard[]>([]);
  const refresh = useCallback(async () => {
    try {
      setStacks(await fetchStacks(workspaceId));
    } catch {
      // The stack view is optional; the PR list keeps working without it.
    }
  }, [workspaceId]);
  const running = stacks.some(mergeRunning);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(
      () => void refresh(),
      running ? 3000 : 15000,
    );
    return () => window.clearInterval(timer);
  }, [refresh, running]);
  return { stacks, refresh };
}
