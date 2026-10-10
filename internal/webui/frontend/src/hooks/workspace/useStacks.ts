import { useCallback, useEffect, useState } from "react";

import {
  fetchStacks,
  queueMergeUpTo,
  type StackCard,
  type StackCardLayer,
} from "@/api/workspace/git";

export type { StackCard, StackCardLayer };

/** Merge up to here: queue change's PR and the approved PRs below it (D38). */
export async function mergeUpTo(
  workspaceId: string,
  change: string,
): Promise<void> {
  await queueMergeUpTo(workspaceId, change);
}

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
