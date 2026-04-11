/**
 * Zustand vanilla store for editor data caching.
 * Global singleton — editor list is system-wide, not workspace-scoped.
 * Replaces module-level cache in api/editors.ts.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchEditors as fetchEditorsApi } from "../api/editors";
import type { EditorInfo } from "@/types/common";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface EditorStoreState {
  editors: EditorInfo[];
  isLoading: boolean;
  error: string | null;
}

export interface EditorStoreActions {
  fetchEditors: () => Promise<EditorInfo[]>;
  refreshEditors: () => Promise<EditorInfo[]>;
  reset: () => void;
}

export type EditorStore = EditorStoreState & EditorStoreActions;

// ---------------------------------------------------------------------------
// Initial state
// ---------------------------------------------------------------------------

export const INITIAL_EDITOR_STATE: EditorStoreState = {
  editors: [],
  isLoading: false,
  error: null,
};

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createEditorStore(): StoreApi<EditorStore> {
  // Closure state — not in Zustand, doesn't trigger re-renders
  let fetchPromise: Promise<EditorInfo[]> | null = null;
  let hasFetched = false;
  let generation = 0;

  return createStore<EditorStore>((set, get) => ({
    ...INITIAL_EDITOR_STATE,

    fetchEditors: async () => {
      if (hasFetched) {
        return get().editors;
      }
      if (fetchPromise !== null) {
        return fetchPromise;
      }

      set({ isLoading: true });
      const gen = generation;

      fetchPromise = fetchEditorsApi().then(
        (editors) => {
          if (gen !== generation) {
            // Stale response — a refresh happened while in-flight. Retry.
            fetchPromise = null;
            return get().fetchEditors();
          }
          hasFetched = true;
          fetchPromise = null;
          set({ editors, isLoading: false, error: null });
          return editors;
        },
        (err) => {
          if (gen === generation) {
            fetchPromise = null;
            const message =
              err instanceof Error ? err.message : "Failed to fetch editors";
            set({ error: message, isLoading: false });
          }
          throw err;
        },
      );

      return fetchPromise;
    },

    refreshEditors: async () => {
      generation++;
      hasFetched = false;
      fetchPromise = null;
      return get().fetchEditors();
    },

    reset: () => {
      generation++;
      hasFetched = false;
      fetchPromise = null;
      set(INITIAL_EDITOR_STATE);
    },
  }));
}

// ---------------------------------------------------------------------------
// Singleton
// ---------------------------------------------------------------------------

export const editorStore = createEditorStore();
