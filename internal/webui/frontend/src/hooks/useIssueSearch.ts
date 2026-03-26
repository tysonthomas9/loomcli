/**
 * Custom hook for searching issues client-side.
 * Fetches a lightweight issue list on mount and provides filtering by ID and title.
 * Uses a module-level cache so multiple hook instances share one fetch.
 */

import { useState, useEffect, useCallback, useMemo } from "react";

import { getKanbanIssues } from "@/api";
import type { Issue } from "@/types";

// Module-level cache shared across all hook instances
let cachedIssues: Issue[] | null = null;
let fetchPromise: Promise<Issue[]> | null = null;

export interface UseIssueSearchReturn {
  /** Filtered search results */
  results: Issue[];
  /** Whether initial data is loading */
  isLoading: boolean;
  /** Search function - filters by ID and title substring */
  search: (query: string) => void;
  /** Current search query */
  query: string;
}

export function useIssueSearch(workspaceId: string): UseIssueSearchReturn {
  const [allIssues, setAllIssues] = useState<Issue[]>(cachedIssues ?? []);
  const [isLoading, setIsLoading] = useState(cachedIssues === null);
  const [query, setQuery] = useState("");

  // Fetch issues once, shared across all instances
  useEffect(() => {
    if (cachedIssues !== null) {
      setAllIssues(cachedIssues);
      setIsLoading(false);
      return;
    }

    let cancelled = false;

    async function fetchIssues() {
      // Deduplicate concurrent fetches
      if (!fetchPromise) {
        fetchPromise = getKanbanIssues(workspaceId);
      }

      try {
        const issues = await fetchPromise;
        cachedIssues = issues;
        if (!cancelled) {
          setAllIssues(issues);
        }
      } catch {
        // Silently fail - search will just have no results
        fetchPromise = null;
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    void fetchIssues();
    return () => {
      cancelled = true;
    };
  }, [workspaceId]);

  const search = useCallback((q: string) => {
    setQuery(q);
  }, []);

  const results = useMemo(() => {
    if (!query.trim()) return [];

    const lowerQuery = query.trim().toLowerCase();
    return allIssues.filter(
      (issue) =>
        issue.id.toLowerCase().includes(lowerQuery) ||
        issue.title.toLowerCase().includes(lowerQuery),
    );
  }, [query, allIssues]);

  return { results, isLoading, search, query };
}
