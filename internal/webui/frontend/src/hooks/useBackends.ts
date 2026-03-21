import { useState, useEffect, useCallback } from "react";

import { fetchBackends, type BackendHealthData } from "../api/backends";
import {
  toBackendInfo,
  type BackendInfo,
} from "../components/BackendSelectorDropdown/backendDefaults";

export interface UseBackendsReturn {
  backends: BackendInfo[];
  isLoading: boolean;
  error: string | null;
  refetch: () => void;
}

/**
 * Hook that fetches backend health data from GET /api/backends
 * and merges each entry with known brand defaults via toBackendInfo().
 */
export function useBackends(): UseBackendsReturn {
  const [backends, setBackends] = useState<BackendInfo[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [generation, setGeneration] = useState(0);

  const mapHealthToInfo = useCallback(
    (items: BackendHealthData[]): BackendInfo[] =>
      items.map((item) => {
        const apiData: Partial<BackendInfo> = { available: item.available };
        if (item.display_name) apiData.displayName = item.display_name;
        if (item.message) apiData.healthMessage = item.message;
        return toBackendInfo(item.name, apiData);
      }),
    [],
  );

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    setError(null);

    fetchBackends()
      .then((data) => {
        if (!cancelled) {
          setBackends(mapHealthToInfo(data));
          setIsLoading(false);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(
            err instanceof Error ? err.message : "Failed to fetch backends",
          );
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [generation, mapHealthToInfo]);

  const refetch = useCallback(() => {
    setGeneration((g) => g + 1);
  }, []);

  return { backends, isLoading, error, refetch };
}
