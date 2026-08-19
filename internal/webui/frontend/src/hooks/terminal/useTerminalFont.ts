/**
 * useTerminalFont publishes the stored terminal font preference onto :root as
 * CSS custom properties, which is where the shared xterm renderer reads it at
 * construction. Read-only: the settings panel that wrote these keys is gone, so
 * the preference is fixed for the lifetime of the page and only values written
 * by an earlier build survive in localStorage.
 */

import { useState, useEffect } from "react";

export const DEFAULT_FONT_FAMILY = 'Menlo, Monaco, "Courier New", monospace';
export const DEFAULT_FONT_SIZE = 12;

const STORAGE_KEY_FONT_FAMILY = "cortex:terminal-font-family";
const STORAGE_KEY_FONT_SIZE = "cortex:terminal-font-size";

/** CSS custom properties consumed by the shared xterm renderer. */
export const TERMINAL_FONT_FAMILY_VAR = "--terminal-font-family";
export const TERMINAL_FONT_SIZE_VAR = "--terminal-font-size";

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

/** Push stored terminal font prefs onto :root for the xterm renderer. */
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

export interface UseTerminalFontReturn {
  fontFamily: string;
  fontSize: number;
}

export function useTerminalFont(): UseTerminalFontReturn {
  const [fontFamily] = useState<string>(
    () => getStoredFontFamily() ?? DEFAULT_FONT_FAMILY,
  );

  const [fontSize] = useState<number>(
    () => getStoredFontSize() ?? DEFAULT_FONT_SIZE,
  );

  useEffect(() => {
    applyTerminalFont(fontFamily, fontSize);
  }, [fontFamily, fontSize]);

  return { fontFamily, fontSize };
}
