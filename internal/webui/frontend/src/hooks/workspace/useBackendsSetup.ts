/**
 * useBackendsSetup — sibling of useBackends that returns the raw
 * BackendHealthData (including setup metadata) instead of the legacy
 * BackendInfo shape.
 *
 * Both hooks read the same backendsStore so the network request fires
 * once. Use this hook when you need install_actions, login_actions,
 * env_vars, authenticated, or ready. Use useBackends when you only
 * need the legacy fields and the brand-merged BackendInfo shape.
 */

import { useEffect } from "react";
import { useStore } from "zustand";

import { backendsStore } from "@/stores";
import type { BackendHealthData } from "@/api/workspace";

export interface UseBackendsSetupReturn {
  backends: BackendHealthData[];
  isLoading: boolean;
  error: string | null;
  refresh: () => Promise<BackendHealthData[]>;
}

export function useBackendsSetup(): UseBackendsSetupReturn {
  const backends = useStore(backendsStore, (s) => s.backends);
  const isLoading = useStore(backendsStore, (s) => s.isLoading);
  const error = useStore(backendsStore, (s) => s.error);

  useEffect(() => {
    backendsStore
      .getState()
      .fetchBackends()
      .catch(() => {});
  }, []);

  return {
    backends,
    isLoading,
    error,
    refresh: () => backendsStore.getState().refreshBackends(),
  };
}
