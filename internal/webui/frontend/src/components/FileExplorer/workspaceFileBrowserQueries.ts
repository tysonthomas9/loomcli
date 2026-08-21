import { useCallback, useMemo } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";

import type { DiffFile } from "@/api/issues";
import type { FileCheckout } from "@/api/workspace";
import {
  fetchDiffFiles,
  gitStatusScoped,
  listFileCheckouts,
} from "@/hooks/api";
import {
  agentQueryKeys,
  fileQueryKeys,
  normalizeFileScopeRef,
} from "@/hooks/queryKeys";
import { checkoutRefKey, type CheckoutRef } from "@/utils/fileExplorerRefs";

export interface BranchDiffRequest {
  key: string;
  agent: string;
}

interface UseWorkspaceFileCheckoutsQueryArgs {
  workspaceId: string;
  isActive: boolean;
}

interface UseWorkspaceFileCheckoutsQueryResult {
  checkouts: FileCheckout[];
  checkoutError: string | null;
  refreshCheckouts: () => Promise<void>;
}

interface UseWorkspaceFileBrowserQueriesArgs {
  workspaceId: string;
  isActive: boolean;
  statusRefs: CheckoutRef[];
  branchDiffRequests: BranchDiffRequest[];
}

interface UseWorkspaceFileBrowserQueriesResult {
  gitStatusByRef: Record<string, Record<string, string>>;
  branchDiffsByRef: Record<string, DiffFile[] | undefined>;
  refreshGitStatus: () => Promise<void>;
  refreshBranchDiffs: () => Promise<void>;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function useWorkspaceFileCheckoutsQuery({
  workspaceId,
  isActive,
}: UseWorkspaceFileCheckoutsQueryArgs): UseWorkspaceFileCheckoutsQueryResult {
  const queryClient = useQueryClient();

  const checkoutsQuery = useQuery({
    queryKey: fileQueryKeys.checkouts(workspaceId),
    queryFn: () => listFileCheckouts(workspaceId),
    enabled: isActive,
  });

  const checkouts = checkoutsQuery.data?.checkouts ?? [];
  const checkoutError = checkoutsQuery.error
    ? errorMessage(checkoutsQuery.error)
    : null;

  const refreshCheckouts = useCallback(
    () =>
      queryClient.invalidateQueries({
        queryKey: fileQueryKeys.checkouts(workspaceId),
      }),
    [queryClient, workspaceId],
  );

  return {
    checkouts,
    checkoutError,
    refreshCheckouts,
  };
}

export function useWorkspaceFileBrowserQueries({
  workspaceId,
  isActive,
  statusRefs,
  branchDiffRequests,
}: UseWorkspaceFileBrowserQueriesArgs): UseWorkspaceFileBrowserQueriesResult {
  const queryClient = useQueryClient();

  const statusQueries = useQueries({
    queries: statusRefs.map((ref) => {
      const normalizedRef = normalizeFileScopeRef(ref);
      return {
        queryKey: fileQueryKeys.gitStatus(workspaceId, ref),
        queryFn: () => gitStatusScoped(workspaceId, normalizedRef),
        enabled: isActive && statusRefs.length > 0,
      };
    }),
  });

  const branchDiffQueries = useQueries({
    queries: branchDiffRequests.map((request) => ({
      queryKey: agentQueryKeys.diffFiles(workspaceId, request.agent, "HEAD"),
      queryFn: () => fetchDiffFiles(workspaceId, request.agent, "HEAD"),
      enabled:
        isActive && branchDiffRequests.length > 0 && request.agent !== "",
    })),
  });

  const gitStatusByRef = useMemo(() => {
    const next: Record<string, Record<string, string>> = {};
    statusRefs.forEach((ref, index) => {
      const query = statusQueries[index];
      if (!query?.data && !query?.isError) return;
      next[checkoutRefKey(ref)] = query.data?.status ?? {};
    });
    return next;
  }, [statusQueries, statusRefs]);

  const branchDiffsByRef = useMemo(() => {
    const next: Record<string, DiffFile[] | undefined> = {};
    branchDiffRequests.forEach((request, index) => {
      const query = branchDiffQueries[index];
      if (!query?.data && !query?.isError) return;
      next[request.key] = query.data ?? [];
    });
    return next;
  }, [branchDiffQueries, branchDiffRequests]);

  const refreshGitStatus = useCallback(async () => {
    await Promise.all(
      statusRefs.map((ref) =>
        queryClient.invalidateQueries({
          queryKey: fileQueryKeys.gitStatus(workspaceId, ref),
        }),
      ),
    );
  }, [queryClient, statusRefs, workspaceId]);

  const refreshBranchDiffs = useCallback(async () => {
    await Promise.all(
      branchDiffRequests.map((request) =>
        queryClient.invalidateQueries({
          queryKey: agentQueryKeys.diffFiles(
            workspaceId,
            request.agent,
            "HEAD",
          ),
        }),
      ),
    );
  }, [branchDiffRequests, queryClient, workspaceId]);

  return {
    gitStatusByRef,
    branchDiffsByRef,
    refreshGitStatus,
    refreshBranchDiffs,
  };
}
