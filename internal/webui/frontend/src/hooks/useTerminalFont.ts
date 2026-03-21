/**
 * useTerminalFont hook for terminal font family and size management.
 * Reads/writes localStorage, providing instant feedback with no save button.
 * Follows the useTheme pattern: localStorage-backed state with try/catch guards.
 */

import { useState, useCallback } from "react";

export const DEFAULT_FONT_FAMILY = 'Menlo, Monaco, "Courier New", monospace';
export const DEFAULT_FONT_SIZE = 14;

const STORAGE_KEY_FONT_FAMILY = "cortex:terminal-font-family";
const STORAGE_KEY_FONT_SIZE = "cortex:terminal-font-size";

/** Sentinel value in the dropdown that triggers the custom text input. */
export const CUSTOM_FONT_SENTINEL = "__custom__";

export const FONT_FAMILY_OPTIONS: { label: string; value: string }[] = [
  { label: "Menlo", value: "Menlo, monospace" },
  { label: "Monaco", value: "Monaco, monospace" },
  { label: "Fira Code", value: '"Fira Code", monospace' },
  { label: "JetBrains Mono", value: '"JetBrains Mono", monospace' },
  { label: "Cascadia Code", value: '"Cascadia Code", monospace' },
  { label: "Source Code Pro", value: '"Source Code Pro", monospace' },
  { label: "Courier New", value: '"Courier New", monospace' },
  { label: "Custom\u2026", value: CUSTOM_FONT_SENTINEL },
];

export const FONT_SIZE_OPTIONS: number[] = [
  10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24,
];

function getStoredFontFamily(): string | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_FONT_FAMILY);
    if (stored && stored.trim().length > 0) {
      return stored;
    }
  } catch {
    // localStorage unavailable (private browsing)
  }
  return null;
}

function getStoredFontSize(): number | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_FONT_SIZE);
    if (stored !== null) {
      const parsed = Number(stored);
      if (!Number.isNaN(parsed) && parsed >= 8 && parsed <= 72) {
        return parsed;
      }
    }
  } catch {
    // localStorage unavailable (private browsing)
  }
  return null;
}

export interface UseTerminalFontReturn {
  fontFamily: string;
  fontSize: number;
  setFontFamily: (family: string) => void;
  setFontSize: (size: number) => void;
}

export function useTerminalFont(): UseTerminalFontReturn {
  const [fontFamily, setFontFamilyState] = useState<string>(
    () => getStoredFontFamily() ?? DEFAULT_FONT_FAMILY,
  );

  const [fontSize, setFontSizeState] = useState<number>(
    () => getStoredFontSize() ?? DEFAULT_FONT_SIZE,
  );

  const setFontFamily = useCallback((family: string) => {
    const value = family.trim() || DEFAULT_FONT_FAMILY;
    setFontFamilyState(value);
    try {
      localStorage.setItem(STORAGE_KEY_FONT_FAMILY, value);
    } catch {
      // localStorage unavailable
    }
  }, []);

  const setFontSize = useCallback((size: number) => {
    const value =
      !Number.isNaN(size) && size >= 8 && size <= 72 ? size : DEFAULT_FONT_SIZE;
    setFontSizeState(value);
    try {
      localStorage.setItem(STORAGE_KEY_FONT_SIZE, String(value));
    } catch {
      // localStorage unavailable
    }
  }, []);

  return { fontFamily, fontSize, setFontFamily, setFontSize };
}
