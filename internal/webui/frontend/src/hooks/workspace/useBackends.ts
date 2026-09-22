import { useEffect, useMemo, useCallback } from "react";
import { useStore } from "zustand";

import { backendsStore } from "@/stores";
import type { BackendHealthData } from "@/api/workspace";
import {
  isUserFacingBackend,
  toBackendInfo,
  type BackendInfo,
} from "@/utils/workspace";

export interface UseBackendsReturn {
  backends: BackendInfo[];
  isLoading: boolean;
  error: string | null;
  refetch: () => void;
}

/**
 * Hook that reads backend health data from backendsStore
 * and merges each entry with known brand defaults via toBackendInfo().
 */
export function useBackends(): UseBackendsReturn {
  const rawBackends = useStore(backendsStore, (s) => s.backends);
  const isLoading = useStore(backendsStore, (s) => s.isLoading);
  const error = useStore(backendsStore, (s) => s.error);

  useEffect(() => {
    backendsStore
      .getState()
      .fetchBackends()
      .catch(() => {});
  }, []);

  const backends = useMemo(
    () =>
      rawBackends
        .filter((item) => isUserFacingBackend(item.name))
        .map((item: BackendHealthData) => {
          const apiData: Partial<BackendInfo> = {
            available: item.available,
            installed: item.installed,
            apiKeySet: item.api_key_set,
          };
          if (item.display_name) apiData.displayName = item.display_name;
          if (item.message) apiData.healthMessage = item.message;
          if (item.version) apiData.version = item.version;
          return toBackendInfo(item.name, apiData);
        }),
    [rawBackends],
  );

  const refetch = useCallback(() => {
    backendsStore
      .getState()
      .refreshBackends()
      .catch(() => {});
  }, []);

  return { backends, isLoading, error, refetch };
}
