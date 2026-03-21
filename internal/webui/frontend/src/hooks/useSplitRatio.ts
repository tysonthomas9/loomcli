/**
 * useSplitRatio hook for managing and persisting the detail/terminal split ratio.
 * Follows the useTerminalFont pattern: localStorage-backed state with try/catch guards.
 */

import { useState, useCallback, useRef } from "react";

const STORAGE_KEY = "cortex:detail-panel-split-ratio";
const DEFAULT_RATIO = 0.5;
const MIN_RATIO = 0.15;
const MAX_RATIO = 0.85;
const MAXIMIZED_RATIO = 0.05;

function clamp(value: number): number {
  return Math.min(MAX_RATIO, Math.max(MIN_RATIO, value));
}

function getStoredRatio(): number | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored !== null) {
      const parsed = Number(stored);
      if (!Number.isNaN(parsed) && parsed >= MIN_RATIO && parsed <= MAX_RATIO) {
        return parsed;
      }
    }
  } catch {
    // localStorage unavailable
  }
  return null;
}

function storeRatio(value: number): void {
  try {
    localStorage.setItem(STORAGE_KEY, String(value));
  } catch {
    // localStorage unavailable
  }
}

export interface UseSplitRatioReturn {
  ratio: number;
  applyDelta: (containerHeight: number, deltaPixels: number) => void;
  resetRatio: () => void;
  isMaximized: boolean;
  toggleMaximize: () => void;
}

export function useSplitRatio(): UseSplitRatioReturn {
  const [ratio, setRatioState] = useState<number>(
    () => getStoredRatio() ?? DEFAULT_RATIO,
  );
  const [isMaximized, setIsMaximized] = useState(false);
  const preMaximizeRatioRef = useRef(DEFAULT_RATIO);

  const setRatio = useCallback((value: number) => {
    const clamped = clamp(value);
    setRatioState(clamped);
    storeRatio(clamped);
  }, []);

  const applyDelta = useCallback(
    (containerHeight: number, deltaPixels: number) => {
      if (containerHeight <= 0) return;
      setRatioState((prev) => {
        const clamped = clamp(prev + deltaPixels / containerHeight);
        storeRatio(clamped);
        return clamped;
      });
      if (isMaximized) setIsMaximized(false);
    },
    [isMaximized],
  );

  const resetRatio = useCallback(() => {
    setRatio(DEFAULT_RATIO);
    setIsMaximized(false);
  }, [setRatio]);

  const toggleMaximize = useCallback(() => {
    if (isMaximized) {
      setRatio(preMaximizeRatioRef.current);
      setIsMaximized(false);
    } else {
      preMaximizeRatioRef.current = ratio;
      setRatioState(MAXIMIZED_RATIO);
      // Don't persist MAXIMIZED_RATIO — keep the last real ratio in storage
      setIsMaximized(true);
    }
  }, [isMaximized, ratio, setRatio]);

  return { ratio, applyDelta, resetRatio, isMaximized, toggleMaximize };
}
