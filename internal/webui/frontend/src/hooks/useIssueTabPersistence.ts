import { useState, useCallback, useEffect, useRef } from "react";

import {
  fetchIssueTabState,
  saveIssueTabState,
  deleteIssueTabState,
} from "@/api/issueTabs";
import type { IssueTabState, IssueTab } from "@/api/issueTabs";
import type { MutationPayload } from "@/api/sse";

export interface UseIssueTabPersistenceReturn {
  /** Loaded state from Redis (null if none saved or still loading) */
  savedState: IssueTabState | null;
  /** Whether the initial load is in progress */
  isLoading: boolean;
  /** Debounced save of current tab state */
  saveTabs: (tabs: IssueTab[], activeTabId: string) => void;
  /** Clear persisted tab state */
  clearTabs: () => void;
  /** Call from SSE onMutation handler to trigger refetch on external changes */
  handleMutation: (mutation: MutationPayload) => void;
}

const SAVE_DEBOUNCE_MS = 300;
const REFETCH_DEBOUNCE_MS = 100;

export function useIssueTabPersistence(
  issueId: string,
): UseIssueTabPersistenceReturn {
  const [savedState, setSavedState] = useState<IssueTabState | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const mountedRef = useRef(true);
  const saveDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const refetchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Track the issueId to avoid stale saves
  const issueIdRef = useRef(issueId);

  useEffect(() => {
    issueIdRef.current = issueId;
  }, [issueId]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (saveDebounceRef.current) {
        clearTimeout(saveDebounceRef.current);
      }
      if (refetchDebounceRef.current) {
        clearTimeout(refetchDebounceRef.current);
      }
    };
  }, []);

  const fetchState = useCallback(async () => {
    if (!issueId) return;
    setIsLoading(true);
    try {
      const state = await fetchIssueTabState(issueId);
      if (mountedRef.current && issueIdRef.current === issueId) {
        setSavedState(state);
      }
    } catch {
      // Silently fail - fallback to default tabs
      if (mountedRef.current) {
        setSavedState(null);
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, [issueId]);

  // Fetch on mount or issueId change
  useEffect(() => {
    setSavedState(null);
    setIsLoading(true);
    fetchState();
  }, [fetchState]);

  const saveTabs = useCallback((tabs: IssueTab[], activeTabId: string) => {
    if (saveDebounceRef.current) {
      clearTimeout(saveDebounceRef.current);
    }
    saveDebounceRef.current = setTimeout(() => {
      if (!mountedRef.current) return;
      const currentIssueId = issueIdRef.current;
      if (!currentIssueId) return;
      saveIssueTabState(currentIssueId, tabs, activeTabId).catch(() => {
        // Silently fail - persistence is best-effort
      });
    }, SAVE_DEBOUNCE_MS);
  }, []);

  const clearTabs = useCallback(() => {
    const currentIssueId = issueIdRef.current;
    if (!currentIssueId) return;
    deleteIssueTabState(currentIssueId).catch(() => {
      // Silently fail - cleanup is best-effort
    });
    setSavedState(null);
  }, []);

  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (mutation.type !== "issue_tabs") return;
      if (mutation.issue_id !== issueId) return;
      // Debounce refetch to collapse rapid events
      if (refetchDebounceRef.current) {
        clearTimeout(refetchDebounceRef.current);
      }
      refetchDebounceRef.current = setTimeout(() => {
        fetchState();
      }, REFETCH_DEBOUNCE_MS);
    },
    [issueId, fetchState],
  );

  return {
    savedState,
    isLoading,
    saveTabs,
    clearTabs,
    handleMutation,
  };
}
