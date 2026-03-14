import { useState, useEffect, useCallback, useRef } from "react";
import {
  fetchEditors,
  refreshEditors,
  getCachedEditors,
  openInEditor,
} from "@/api/editors";
import type { EditorInfo } from "@/types/editor";

export interface UseEditorsResult {
  editors: EditorInfo[];
  detectedEditors: EditorInfo[];
  isLoading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  openEditor: (editorId: string, path: string) => Promise<void>;
}

export function useEditors(): UseEditorsResult {
  const [editors, setEditors] = useState<EditorInfo[]>(
    getCachedEditors() ?? [],
  );
  const [isLoading, setIsLoading] = useState(getCachedEditors() === null);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    if (getCachedEditors() !== null)
      return () => {
        mountedRef.current = false;
      };

    let cancelled = false;
    (async () => {
      try {
        const result = await fetchEditors();
        if (!cancelled && mountedRef.current) {
          setEditors(result);
          setError(null);
        }
      } catch (err) {
        if (!cancelled && mountedRef.current) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      } finally {
        if (!cancelled && mountedRef.current) {
          setIsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
      mountedRef.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const result = await refreshEditors();
      if (mountedRef.current) {
        setEditors(result);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  const openEditor = useCallback(async (editorId: string, path: string) => {
    await openInEditor(editorId, path);
  }, []);

  const detectedEditors = editors.filter((e) => e.detected);

  return { editors, detectedEditors, isLoading, error, refresh, openEditor };
}
