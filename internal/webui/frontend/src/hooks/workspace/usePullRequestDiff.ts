import { useEffect, useState } from "react";

import { getPullRequestDiff, type PullRequestDiff } from "@/api/workspace";

export interface UsePullRequestDiffParams {
  workspaceId: string;
  owner: string;
  repo: string;
  number: number;
  refreshKey?: number;
}

export interface UsePullRequestDiffResult {
  diff: PullRequestDiff | null;
  isLoading: boolean;
  error: string | null;
}

function errorMessageFromUnknown(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Failed to load pull request diff";
}

export function usePullRequestDiff({
  workspaceId,
  owner,
  repo,
  number: pullNumber,
  refreshKey,
}: UsePullRequestDiffParams): UsePullRequestDiffResult {
  const [diff, setDiff] = useState<PullRequestDiff | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    setIsLoading(true);
    setError(null);

    getPullRequestDiff(workspaceId, owner, repo, pullNumber)
      .then((nextDiff) => {
        if (!cancelled) setDiff(nextDiff);
      })
      .catch((loadError: unknown) => {
        if (cancelled) return;
        setDiff(null);
        setError(errorMessageFromUnknown(loadError));
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [workspaceId, owner, repo, pullNumber, refreshKey]);

  return { diff, isLoading, error };
}
