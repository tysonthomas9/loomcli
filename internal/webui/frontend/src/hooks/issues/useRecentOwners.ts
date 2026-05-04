/**
 * Hook for managing recent owner names in localStorage.
 * Used by OwnerDropdown to remember previously entered names.
 */

import { useState, useCallback, useEffect } from "react";

const STORAGE_KEY = "loom-recent-owners";
const MAX_RECENT = 5;

/**
 * Return type for useRecentOwners hook.
 */
export interface UseRecentOwnersReturn {
  /** Array of recent owner names (most recent first) */
  recentOwners: string[];
  /** Add a name to the front of the list (dedupes, trims to max) */
  addRecentOwner: (name: string) => void;
  /** Clear all recent owners */
  clearRecentOwners: () => void;
}

/**
 * Read recent owners from localStorage.
 */
function loadFromStorage(): string[] {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (!stored) return [];
    const parsed = JSON.parse(stored);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is string => typeof item === "string");
  } catch {
    return [];
  }
}

/**
 * Save recent owners to localStorage.
 */
function saveToStorage(names: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(names));
  } catch {
    // Graceful degradation - no persistence
  }
}

/**
 * Hook for managing recent owner names.
 * Persists to localStorage for cross-session memory.
 */
export function useRecentOwners(): UseRecentOwnersReturn {
  const [recentOwners, setRecentOwners] = useState<string[]>(() =>
    loadFromStorage(),
  );

  useEffect(() => {
    saveToStorage(recentOwners);
  }, [recentOwners]);

  const addRecentOwner = useCallback((name: string) => {
    if (!name.trim()) return;

    setRecentOwners((prev) => {
      const trimmedName = name.trim();
      const filtered = prev.filter(
        (n) => n.toLowerCase() !== trimmedName.toLowerCase(),
      );
      const updated = [trimmedName, ...filtered].slice(0, MAX_RECENT);
      return updated;
    });
  }, []);

  const clearRecentOwners = useCallback(() => {
    setRecentOwners([]);
  }, []);

  return {
    recentOwners,
    addRecentOwner,
    clearRecentOwners,
  };
}
