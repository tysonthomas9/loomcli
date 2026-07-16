/**
 * useTerminalFont hook for terminal font family and size management.
 * Reads/writes localStorage, providing instant feedback with no save button.
 * Follows the useTheme pattern: localStorage-backed state with try/catch guards.
 */

import { useState, useCallback, useEffect, useRef } from "react";

export const DEFAULT_FONT_FAMILY = 'Menlo, Monaco, "Courier New", monospace';
export const DEFAULT_FONT_SIZE = 12;

const STORAGE_KEY_FONT_FAMILY = "cortex:terminal-font-family";
const STORAGE_KEY_FONT_SIZE = "cortex:terminal-font-size";

/** Dispatched when terminal font prefs change (same tab or via hook sync). */
export const TERMINAL_FONT_CHANGE_EVENT = "loom:terminal-font-change";

export interface TerminalFontChangeDetail {
  fontFamily: string;
  fontSize: number;
}

/** CSS custom properties consumed by TerminalInstance / wterm. */
export const TERMINAL_FONT_FAMILY_VAR = "--terminal-font-family";
export const TERMINAL_FONT_SIZE_VAR = "--terminal-font-size";

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
  8, 9, 10, 11, 12, 13, 14, 16, 18, 20, 22,
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

/** Push stored terminal font prefs onto :root for wterm CSS vars. */
export function applyTerminalFont(
  family: string = getStoredFontFamily() ?? DEFAULT_FONT_FAMILY,
  size: number = getStoredFontSize() ?? DEFAULT_FONT_SIZE,
): void {
  if (typeof document === "undefined") return;
  document.documentElement.style.setProperty(TERMINAL_FONT_FAMILY_VAR, family);
  document.documentElement.style.setProperty(
    TERMINAL_FONT_SIZE_VAR,
    `${size}px`,
  );
}

function dispatchTerminalFontChange(detail: TerminalFontChangeDetail): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(
    new CustomEvent<TerminalFontChangeDetail>(TERMINAL_FONT_CHANGE_EVENT, {
      detail,
    }),
  );
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

  const fontSizeRef = useRef(fontSize);
  fontSizeRef.current = fontSize;
  const fontFamilyRef = useRef(fontFamily);
  fontFamilyRef.current = fontFamily;

  useEffect(() => {
    applyTerminalFont(fontFamily, fontSize);
  }, [fontFamily, fontSize]);

  // Keep hook instances in sync when another component updates prefs.
  useEffect(() => {
    const onFontChange = (event: Event) => {
      const detail = (event as CustomEvent<TerminalFontChangeDetail>).detail;
      if (!detail) return;
      setFontFamilyState(detail.fontFamily);
      setFontSizeState(detail.fontSize);
    };
    window.addEventListener(TERMINAL_FONT_CHANGE_EVENT, onFontChange);
    return () =>
      window.removeEventListener(TERMINAL_FONT_CHANGE_EVENT, onFontChange);
  }, []);

  // Cross-tab sync via localStorage.
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.storageArea !== localStorage) return;
      let nextFamily = fontFamilyRef.current;
      let nextSize = fontSizeRef.current;
      let changed = false;

      if (event.key === STORAGE_KEY_FONT_FAMILY && event.newValue?.trim()) {
        nextFamily = event.newValue;
        changed = true;
      }
      if (event.key === STORAGE_KEY_FONT_SIZE && event.newValue !== null) {
        const parsed = Number(event.newValue);
        if (!Number.isNaN(parsed) && parsed >= 8 && parsed <= 72) {
          nextSize = parsed;
          changed = true;
        }
      }
      if (!changed) return;

      setFontFamilyState(nextFamily);
      setFontSizeState(nextSize);
      applyTerminalFont(nextFamily, nextSize);
      dispatchTerminalFontChange({
        fontFamily: nextFamily,
        fontSize: nextSize,
      });
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const setFontFamily = useCallback((family: string) => {
    const value = family.trim() || DEFAULT_FONT_FAMILY;
    const size = fontSizeRef.current;
    setFontFamilyState(value);
    try {
      localStorage.setItem(STORAGE_KEY_FONT_FAMILY, value);
    } catch {
      // localStorage unavailable
    }
    applyTerminalFont(value, size);
    dispatchTerminalFontChange({ fontFamily: value, fontSize: size });
  }, []);

  const setFontSize = useCallback((size: number) => {
    const value =
      !Number.isNaN(size) && size >= 8 && size <= 72 ? size : DEFAULT_FONT_SIZE;
    const family = fontFamilyRef.current;
    setFontSizeState(value);
    try {
      localStorage.setItem(STORAGE_KEY_FONT_SIZE, String(value));
    } catch {
      // localStorage unavailable
    }
    applyTerminalFont(family, value);
    dispatchTerminalFontChange({ fontFamily: family, fontSize: value });
  }, []);

  return { fontFamily, fontSize, setFontFamily, setFontSize };
}
