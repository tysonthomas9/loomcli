/**
 * Persists a resizable panel width per workspace.
 *
 * Width updates are applied to state immediately; the localStorage write is
 * debounced so a pointer drag (~100 events/sec) doesn't write per move.
 * Pending writes are flushed on unmount and on workspace switch.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { wsGet, wsSet } from "@/utils/scopedStorage";

const PERSIST_DEBOUNCE_MS = 200;

export interface PanelWidthConfig {
  storageKey: string;
  defaultWidth: number;
  minWidth: number;
  maxWidth: number;
}

export interface UsePersistedPanelWidthReturn {
  width: number;
  applyDelta: (deltaPx: number) => void;
  resetWidth: () => void;
}

function readStoredWidth(
  workspaceId: string | undefined,
  { storageKey, defaultWidth, minWidth, maxWidth }: PanelWidthConfig,
): number {
  if (!workspaceId) return defaultWidth;
  const stored = wsGet(workspaceId, storageKey);
  if (stored === null) return defaultWidth;
  const parsed = Number(stored);
  if (Number.isNaN(parsed)) return defaultWidth;
  return Math.min(maxWidth, Math.max(minWidth, parsed));
}

export function usePersistedPanelWidth(
  workspaceId: string | undefined,
  config: PanelWidthConfig,
): UsePersistedPanelWidthReturn {
  const { storageKey, defaultWidth, minWidth, maxWidth } = config;
  const [width, setWidth] = useState(() => readStoredWidth(workspaceId, config));

  const clamp = useCallback(
    (value: number) => Math.min(maxWidth, Math.max(minWidth, value)),
    [minWidth, maxWidth],
  );

  // Pending debounced write; carries its own workspace/key so a flush after
  // a workspace switch still writes to the workspace it belongs to.
  const pendingRef = useRef<{ ws: string; key: string; value: number } | null>(
    null,
  );
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flush = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    const pending = pendingRef.current;
    pendingRef.current = null;
    if (pending) {
      wsSet(pending.ws, pending.key, String(pending.value));
    }
  }, []);

  const schedulePersist = useCallback(
    (value: number) => {
      if (!workspaceId) return;
      pendingRef.current = { ws: workspaceId, key: storageKey, value };
      if (timerRef.current === null) {
        timerRef.current = setTimeout(() => {
          timerRef.current = null;
          const pending = pendingRef.current;
          pendingRef.current = null;
          if (pending) {
            wsSet(pending.ws, pending.key, String(pending.value));
          }
        }, PERSIST_DEBOUNCE_MS);
      }
    },
    [workspaceId, storageKey],
  );

  useEffect(() => {
    setWidth(readStoredWidth(workspaceId, config));
    // Flush the previous workspace's pending write before switching, and on
    // unmount.
    return flush;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId, storageKey, flush]);

  const applyDelta = useCallback(
    (deltaPx: number) => {
      setWidth((prev) => {
        const clamped = clamp(prev + deltaPx);
        schedulePersist(clamped);
        return clamped;
      });
    },
    [clamp, schedulePersist],
  );

  const resetWidth = useCallback(() => {
    const clamped = clamp(defaultWidth);
    setWidth(clamped);
    schedulePersist(clamped);
  }, [clamp, defaultWidth, schedulePersist]);

  return { width, applyDelta, resetWidth };
}
