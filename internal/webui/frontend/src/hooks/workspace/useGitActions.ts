/**
 * Hook for executing git actions (push, pull, sync, PR, reset, target update)
 * with loading state management and toast feedback.
 */

import { useState, useCallback, useRef, useEffect } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";

import { ApiError } from "@/api/common";
import { getIssue, updateIssue } from "@/api/issues/issues";
import {
  gitPush,
  gitPull,
  gitSync,
  gitCreatePR,
  gitReset,
  gitUpdateTarget,
} from "@/api/workspace";
import type { GitResetLockedResponse } from "@/api/workspace";
import { useToast } from "@/hooks/ui";
import { agentQueryKeys, fileQueryKeys } from "@/hooks/queryKeys";
import { useWorkspaceContext } from "./useWorkspaceContext";

export interface UseGitActionsOptions {
  agentName: string | null;
  /** Linked loom task — PR URL is written to external_ref after create. */
  taskId?: string | null;
  onStatusChange?: () => void;
}

export interface GitActionState {
  isLoading: boolean;
  error: string | null;
}

export interface UseGitActionsReturn {
  push: (target?: string) => Promise<void>;
  pull: (source?: string) => Promise<void>;
  sync: () => Promise<void>;
  createPR: (target?: string) => Promise<void>;
  reset: (branch?: string, force?: boolean) => Promise<void>;
  updateTarget: (branch: string) => Promise<void>;
  pushState: GitActionState;
  pullState: GitActionState;
  syncState: GitActionState;
  prState: GitActionState;
  resetState: GitActionState;
  targetState: GitActionState;
  anyLoading: boolean;
}

const INITIAL_STATE: GitActionState = { isLoading: false, error: null };

function extractErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (
      typeof err.body === "object" &&
      err.body !== null &&
      "error" in err.body
    ) {
      return (err.body as { error: string }).error;
    }
    if (typeof err.body === "string") return err.body;
    return err.statusText;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}

function queryKeyStartsWith(queryKey: QueryKey, prefix: QueryKey): boolean {
  return (
    queryKey.length >= prefix.length &&
    prefix.every((part, index) => Object.is(queryKey[index], part))
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isAgentFileGitStatusQuery(
  queryKey: QueryKey,
  workspaceId: string,
  agentName: string,
): boolean {
  const prefix = fileQueryKeys.gitStatusPrefix(workspaceId);
  if (!queryKeyStartsWith(queryKey, prefix)) return false;
  const ref = queryKey[prefix.length];
  return (
    isRecord(ref) &&
    ref.scope === "agent" &&
    ref.target === agentName.trim()
  );
}

export function useGitActions({
  agentName,
  taskId,
  onStatusChange,
}: UseGitActionsOptions): UseGitActionsReturn {
  const { workspaceId } = useWorkspaceContext();
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const mountedRef = useRef(true);
  const onStatusChangeRef = useRef(onStatusChange);
  onStatusChangeRef.current = onStatusChange;

  // Track mounted state for safe async cleanup
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const [pushState, setPushState] = useState<GitActionState>(INITIAL_STATE);
  const [pullState, setPullState] = useState<GitActionState>(INITIAL_STATE);
  const [syncState, setSyncState] = useState<GitActionState>(INITIAL_STATE);
  const [prState, setPrState] = useState<GitActionState>(INITIAL_STATE);
  const [resetState, setResetState] = useState<GitActionState>(INITIAL_STATE);
  const [targetState, setTargetState] = useState<GitActionState>(INITIAL_STATE);

  const handleApiError = useCallback(
    (err: unknown, action: string): string => {
      if (err instanceof ApiError) {
        // 409: Conflict
        if (err.status === 409) {
          const body = err.body as { conflicted_files?: string[] } | undefined;
          const files = body?.conflicted_files ?? [];
          const msg =
            files.length > 0
              ? `Merge conflicts in ${files.length} file${files.length > 1 ? "s" : ""}`
              : `${action} resulted in conflicts`;
          showToast(msg, { type: "warning" });
          return msg;
        }
        // 423: Locked
        if (err.status === 423) {
          const body = err.body as GitResetLockedResponse | undefined;
          const info = body?.lock_info;
          const msg = info
            ? `Agent locked by ${info.agent} (${info.duration})`
            : "Agent is locked";
          showToast(msg, { type: "error" });
          return msg;
        }
        // 503: gh not installed
        if (err.status === 503) {
          const msg = extractErrorMessage(err);
          showToast(msg, { type: "error" });
          return msg;
        }
      }
      // Generic error
      const msg = extractErrorMessage(err);
      showToast(`${action} failed: ${msg}`, { type: "error" });
      return msg;
    },
    [showToast],
  );

  const invalidateAgentGitQueries = useCallback(
    (name: string) => {
      const trimmedName = name.trim();
      if (!trimmedName) return;

      void queryClient.invalidateQueries({
        queryKey: agentQueryKeys.diffFiles(workspaceId, trimmedName, "HEAD"),
      });
      void queryClient.invalidateQueries({
        queryKey: agentQueryKeys.diffCommits(workspaceId, trimmedName),
      });
      void queryClient.invalidateQueries({
        queryKey: agentQueryKeys.agentGitStatus(workspaceId, trimmedName),
      });
      void queryClient.invalidateQueries({
        queryKey: agentQueryKeys.diffStat(workspaceId, trimmedName),
      });
      void queryClient.invalidateQueries({
        queryKey: fileQueryKeys.checkouts(workspaceId),
      });
      void queryClient.invalidateQueries({
        predicate: (query) =>
          isAgentFileGitStatusQuery(query.queryKey, workspaceId, trimmedName),
      });
    },
    [queryClient, workspaceId],
  );

  const notifyGitStateChanged = useCallback(
    (name: string) => {
      invalidateAgentGitQueries(name);
      onStatusChangeRef.current?.();
    },
    [invalidateAgentGitQueries],
  );

  const push = useCallback(
    async (target?: string) => {
      if (!agentName) return;
      setPushState({ isLoading: true, error: null });
      try {
        const result = await gitPush(workspaceId, agentName, target);
        if (mountedRef.current) {
          setPushState({ isLoading: false, error: null });
          showToast(result.message || "Push successful", { type: "success" });
          notifyGitStateChanged(agentName);
        }
      } catch (err) {
        if (mountedRef.current) {
          const msg = handleApiError(err, "Push");
          setPushState({ isLoading: false, error: msg });
          notifyGitStateChanged(agentName);
        }
      }
    },
    [workspaceId, agentName, showToast, handleApiError, notifyGitStateChanged],
  );

  const pull = useCallback(
    async (source?: string) => {
      if (!agentName) return;
      setPullState({ isLoading: true, error: null });
      try {
        const result = await gitPull(workspaceId, agentName, source);
        if (mountedRef.current) {
          setPullState({ isLoading: false, error: null });
          showToast(result.message || "Pull successful", { type: "success" });
          notifyGitStateChanged(agentName);
        }
      } catch (err) {
        if (mountedRef.current) {
          const msg = handleApiError(err, "Pull");
          setPullState({ isLoading: false, error: msg });
          notifyGitStateChanged(agentName);
        }
      }
    },
    [workspaceId, agentName, showToast, handleApiError, notifyGitStateChanged],
  );

  const sync = useCallback(async () => {
    if (!agentName) return;
    setSyncState({ isLoading: true, error: null });
    try {
      await gitSync(workspaceId, agentName);
      if (mountedRef.current) {
        setSyncState({ isLoading: false, error: null });
        showToast("Sync successful", { type: "success" });
        notifyGitStateChanged(agentName);
      }
    } catch (err) {
      if (mountedRef.current) {
        const msg = handleApiError(err, "Sync");
        setSyncState({ isLoading: false, error: msg });
        notifyGitStateChanged(agentName);
      }
    }
  }, [
    workspaceId,
    agentName,
    showToast,
    handleApiError,
    notifyGitStateChanged,
  ]);

  const createPR = useCallback(
    async (target?: string) => {
      if (!agentName) return;
      setPrState({ isLoading: true, error: null });
      try {
        const result = await gitCreatePR(workspaceId, agentName, target);
        if (result.url && taskId) {
          try {
            if (result.already_exists) {
              // Backfill a missing link, but never touch status — the task
              // may have moved on (or closed) since the PR was created.
              const issue = await getIssue(workspaceId, taskId);
              if (!issue.external_ref) {
                await updateIssue(workspaceId, taskId, {
                  external_ref: result.url,
                });
              }
            } else {
              await updateIssue(workspaceId, taskId, {
                external_ref: result.url,
                status: "review",
              });
            }
          } catch {
            // PR was created; linking the ticket is best-effort.
          }
        }
        if (mountedRef.current) {
          setPrState({ isLoading: false, error: null });
          if (result.already_exists) {
            showToast(
              result.url
                ? `PR already exists: ${result.url}`
                : "PR already exists",
              {
                type: "info",
              },
            );
          } else if (result.no_commits) {
            showToast("No commits to create PR for", { type: "info" });
          } else {
            showToast(result.url ? `PR created: ${result.url}` : "PR created", {
              type: "success",
            });
          }
          notifyGitStateChanged(agentName);
        }
      } catch (err) {
        if (mountedRef.current) {
          const msg = handleApiError(err, "Create PR");
          setPrState({ isLoading: false, error: msg });
        }
      }
    },
    [
      workspaceId,
      agentName,
      taskId,
      showToast,
      handleApiError,
      notifyGitStateChanged,
    ],
  );

  const reset = useCallback(
    async (branch?: string, force?: boolean) => {
      if (!agentName) return;
      setResetState({ isLoading: true, error: null });
      try {
        const result = await gitReset(workspaceId, agentName, branch, force);
        if (mountedRef.current) {
          setResetState({ isLoading: false, error: null });
          showToast(result.message || "Reset successful", { type: "success" });
          notifyGitStateChanged(agentName);
        }
      } catch (err) {
        if (mountedRef.current) {
          const msg = handleApiError(err, "Reset");
          setResetState({ isLoading: false, error: msg });
          notifyGitStateChanged(agentName);
        }
      }
    },
    [workspaceId, agentName, showToast, handleApiError, notifyGitStateChanged],
  );

  const updateTarget = useCallback(
    async (branch: string) => {
      if (!agentName) return;
      setTargetState({ isLoading: true, error: null });
      try {
        await gitUpdateTarget(workspaceId, agentName, branch);
        if (mountedRef.current) {
          setTargetState({ isLoading: false, error: null });
          showToast(`Target branch updated to ${branch}`, { type: "success" });
          notifyGitStateChanged(agentName);
        }
      } catch (err) {
        if (mountedRef.current) {
          const msg = handleApiError(err, "Update target");
          setTargetState({ isLoading: false, error: msg });
        }
      }
    },
    [workspaceId, agentName, showToast, handleApiError, notifyGitStateChanged],
  );

  const anyLoading =
    pushState.isLoading ||
    pullState.isLoading ||
    syncState.isLoading ||
    prState.isLoading ||
    resetState.isLoading ||
    targetState.isLoading;

  return {
    push,
    pull,
    sync,
    createPR,
    reset,
    updateTarget,
    pushState,
    pullState,
    syncState,
    prState,
    resetState,
    targetState,
    anyLoading,
  };
}
