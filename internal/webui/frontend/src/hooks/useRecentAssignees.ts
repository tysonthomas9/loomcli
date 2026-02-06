/**
 * Hook for managing recent assignee names in localStorage.
 * Used by AssigneePrompt to remember previously entered names.
 */

import { useState, useCallback, useEffect } from 'react';

const STORAGE_KEY = 'beads-recent-assignees';
const MAX_RECENT = 5;

/**
 * Return type for useRecentAssignees hook.
 */
export interface UseRecentAssigneesReturn {
  /** Array of recent assignee names (most recent first) */
  recentAssignees: string[];
  /** Add a name to the front of the list (dedupes, trims to max) */
  addRecentAssignee: (name: string) => void;
  /** Clear all recent assignees */
  clearRecentAssignees: () => void;
}

/**
 * Read recent assignees from localStorage.
 * Returns empty array on error or if localStorage unavailable.
 */
function loadFromStorage(): string[] {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (!stored) return [];
    const parsed = JSON.parse(stored);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is string => typeof item === 'string');
  } catch {
    return [];
  }
}

/**
 * Save recent assignees to localStorage.
 * Silently fails if localStorage unavailable.
 */
function saveToStorage(names: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(names));
  } catch {
    // Graceful degradation - no persistence
  }
}

/**
 * Hook for managing recent assignee names.
 * Persists to localStorage for cross-session memory.
 *
 * @example
 * ```tsx
 * const { recentAssignees, addRecentAssignee } = useRecentAssignees();
 *
 * // Show dropdown of recent names
 * {recentAssignees.map(name => <option key={name}>{name}</option>)}
 *
 * // Add new name when user confirms
 * addRecentAssignee('Tyson');
 * ```
 */
export function useRecentAssignees(): UseRecentAssigneesReturn {
  const [recentAssignees, setRecentAssignees] = useState<string[]>(() => loadFromStorage());

  // Sync state to localStorage when it changes
  useEffect(() => {
    saveToStorage(recentAssignees);
  }, [recentAssignees]);

  const addRecentAssignee = useCallback((name: string) => {
    if (!name.trim()) return;

    setRecentAssignees((prev) => {
      // Remove duplicates (case-insensitive comparison but preserve original case)
      const trimmedName = name.trim();
      const filtered = prev.filter((n) => n.toLowerCase() !== trimmedName.toLowerCase());
      // Add to front and trim to max
      const updated = [trimmedName, ...filtered].slice(0, MAX_RECENT);
      return updated;
    });
  }, []);

  const clearRecentAssignees = useCallback(() => {
    setRecentAssignees([]);
  }, []);

  return {
    recentAssignees,
    addRecentAssignee,
    clearRecentAssignees,
  };
}
