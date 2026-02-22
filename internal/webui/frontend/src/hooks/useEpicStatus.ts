/**
 * Hook to fetch epic completion statuses for swim lane headers.
 * Returns a map of epic ID to EpicStatus for efficient lookup.
 */

import { useEffect, useState, useRef } from 'react';

import { getEpicStatuses } from '@/api/issues';
import type { EpicStatus } from '@/types';

export interface UseEpicStatusReturn {
  /** Map of epic issue ID to its completion status */
  epicStatuses: Map<string, EpicStatus>;
  /** Whether the data is currently loading */
  isLoading: boolean;
  /** Error message if fetch failed */
  error: string | null;
}

/**
 * Fetches epic completion statuses and returns them as a lookup map.
 * Only fetches when enabled (e.g., when groupBy === 'epic').
 *
 * @param enabled - Whether to fetch epic statuses (skip when not grouping by epic)
 */
export function useEpicStatus(enabled: boolean): UseEpicStatusReturn {
  const [epicStatuses, setEpicStatuses] = useState<Map<string, EpicStatus>>(new Map());
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!enabled) {
      setEpicStatuses(new Map());
      setIsLoading(false);
      setError(null);
      return;
    }

    // Cancel any in-flight request
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    let cancelled = false;
    setIsLoading(true);

    getEpicStatuses(controller.signal)
      .then((statuses) => {
        if (cancelled) return;
        const map = new Map<string, EpicStatus>();
        for (const status of statuses) {
          if (status.epic) {
            map.set(status.epic.id, status);
          }
        }
        setEpicStatuses(map);
        setError(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const message = err instanceof Error ? err.message : 'Failed to fetch epic statuses';
        setError(message);
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [enabled]);

  return { epicStatuses, isLoading, error };
}
