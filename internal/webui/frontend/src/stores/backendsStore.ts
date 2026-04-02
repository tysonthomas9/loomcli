/**
 * Zustand vanilla store for backend health data caching.
 * Global singleton — backend health is system-wide, not workspace-scoped.
 * Replaces module-level cache in api/backends.ts.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchBackends as fetchBackendsApi } from "../api/backends";
import type { BackendHealthData } from "../api/backends";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface BackendsStoreState {
  backends: BackendHealthData[];
  isLoading: boolean;
  error: string | null;
}

export interface BackendsStoreActions {
  fetchBackends: () => Promise<BackendHealthData[]>;
  refreshBackends: () => Promise<BackendHealthData[]>;
  reset: () => void;
}

export type BackendsStore = BackendsStoreState & BackendsStoreActions;

// ---------------------------------------------------------------------------
// Initial state
// ---------------------------------------------------------------------------

export const INITIAL_BACKENDS_STATE: BackendsStoreState = {
  backends: [],
  isLoading: false,
  error: null,
};

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createBackendsStore(): StoreApi<BackendsStore> {
  // Closure state — not in Zustand, doesn't trigger re-renders
  let fetchPromise: Promise<BackendHealthData[]> | null = null;
  let cacheGeneration = 0;
  let hasFetched = false;

  return createStore<BackendsStore>((set, get) => ({
    ...INITIAL_BACKENDS_STATE,

    fetchBackends: async () => {
      if (hasFetched) {
        return get().backends;
      }
      if (fetchPromise !== null) {
        return fetchPromise;
      }

      set({ isLoading: true });
      const gen = cacheGeneration;

      fetchPromise = fetchBackendsApi().then(
        (backends) => {
          if (gen !== cacheGeneration) {
            // Stale response — a refresh happened while in-flight. Retry.
            fetchPromise = null;
            return get().fetchBackends();
          }
          hasFetched = true;
          fetchPromise = null;
          set({ backends, isLoading: false, error: null });
          return backends;
        },
        (err) => {
          if (gen === cacheGeneration) {
            fetchPromise = null;
            const message =
              err instanceof Error ? err.message : "Failed to fetch backends";
            set({ error: message, isLoading: false });
          }
          throw err;
        },
      );

      return fetchPromise;
    },

    refreshBackends: async () => {
      cacheGeneration++;
      hasFetched = false;
      fetchPromise = null;
      return get().fetchBackends();
    },

    reset: () => {
      cacheGeneration++;
      hasFetched = false;
      fetchPromise = null;
      set(INITIAL_BACKENDS_STATE);
    },
  }));
}

// ---------------------------------------------------------------------------
// Singleton
// ---------------------------------------------------------------------------

export const backendsStore = createBackendsStore();
