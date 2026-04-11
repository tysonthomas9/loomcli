/**
 * useTheme hook for dark/light theme management.
 * Reads/writes localStorage, respects OS prefers-color-scheme,
 * and sets data-theme attribute on <html>.
 */

import { useState, useCallback, useEffect } from "react";

export type Theme = "light" | "dark";

const STORAGE_KEY = "cortex:theme";

function getOSTheme(): Theme {
  if (typeof window !== "undefined") {
    if (window.matchMedia("(prefers-color-scheme: dark)").matches)
      return "dark";
    if (window.matchMedia("(prefers-color-scheme: light)").matches)
      return "light";
  }
  return "dark";
}

function getStoredTheme(): Theme | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") {
      return stored;
    }
  } catch {
    // localStorage unavailable (private browsing)
  }
  return null;
}

function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

export interface UseThemeReturn {
  theme: Theme;
  toggleTheme: () => void;
  setTheme: (theme: Theme) => void;
}

export function useTheme(): UseThemeReturn {
  const [theme, setThemeState] = useState<Theme>(() => {
    const stored = getStoredTheme();
    return stored ?? getOSTheme();
  });

  // Track whether user has an explicit preference
  const [hasExplicit, setHasExplicit] = useState(
    () => getStoredTheme() !== null,
  );

  // Apply theme to DOM on mount and changes
  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  // Listen for OS theme changes when no explicit preference
  useEffect(() => {
    if (hasExplicit) return;

    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = (e: MediaQueryListEvent) => {
      const osTheme: Theme = e.matches ? "dark" : "light";
      setThemeState(osTheme);
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [hasExplicit]);

  const setTheme = useCallback((newTheme: Theme) => {
    setThemeState(newTheme);
    setHasExplicit(true);
    try {
      localStorage.setItem(STORAGE_KEY, newTheme);
    } catch {
      // localStorage unavailable
    }
  }, []);

  const toggleTheme = useCallback(() => {
    setThemeState((prev) => {
      const next = prev === "light" ? "dark" : "light";
      try {
        localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // localStorage unavailable
      }
      return next;
    });
    setHasExplicit(true);
  }, []);

  return { theme, toggleTheme, setTheme };
}
