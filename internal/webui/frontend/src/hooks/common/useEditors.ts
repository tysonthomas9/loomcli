import { useEffect, useMemo, useCallback } from "react";
import { useStore } from "zustand";

import { editorStore } from "@/stores";
import { openInEditor } from "@/api/workspace";
import type { EditorInfo } from "@/types/common";

export interface UseEditorsResult {
  editors: EditorInfo[];
  detectedEditors: EditorInfo[];
  isLoading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  openEditor: (editorId: string, path: string) => Promise<void>;
}

export function useEditors(): UseEditorsResult {
  const editors = useStore(editorStore, (s) => s.editors);
  const isLoading = useStore(editorStore, (s) => s.isLoading);
  const errorMsg = useStore(editorStore, (s) => s.error);

  useEffect(() => {
    editorStore
      .getState()
      .fetchEditors()
      .catch(() => {});
  }, []);

  const error = useMemo(
    () => (errorMsg ? new Error(errorMsg) : null),
    [errorMsg],
  );

  const detectedEditors = useMemo(
    () => editors.filter((e) => e.detected),
    [editors],
  );

  const refresh = useCallback(async () => {
    await editorStore.getState().refreshEditors();
  }, []);

  const openEditor = useCallback(async (editorId: string, path: string) => {
    await openInEditor(editorId, path);
  }, []);

  return { editors, detectedEditors, isLoading, error, refresh, openEditor };
}
