import { useCallback, useRef, useState } from "react";

/**
 * Favorite and recently used models, kept in this browser (T3 keeps
 * favorites in its client settings). Keys are harness:model, so each
 * harness's lists stay apart.
 */
export interface ModelPickerPrefs {
  favorites: string[];
  recent: string[];
}

const STORAGE_KEY = "loom.agentChat.modelPicker";
const RECENT_LIMIT = 5;

export const modelPrefKey = (harness: string, modelId: string) =>
  `${harness}:${modelId}`;

function load(): ModelPickerPrefs {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const p = raw ? (JSON.parse(raw) as Partial<ModelPickerPrefs>) : {};
    return {
      favorites: Array.isArray(p.favorites) ? p.favorites : [],
      recent: Array.isArray(p.recent) ? p.recent : [],
    };
  } catch {
    return { favorites: [], recent: [] };
  }
}

function save(p: ModelPickerPrefs) {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(p));
  } catch {
    // Storage full or blocked: the lists last for this page only.
  }
}

export function useModelPickerPrefs() {
  const [prefs, setPrefs] = useState<ModelPickerPrefs>(load);
  const prefsRef = useRef(prefs);
  const update = useCallback(
    (fn: (p: ModelPickerPrefs) => ModelPickerPrefs) => {
      const next = fn(prefsRef.current);
      prefsRef.current = next;
      save(next);
      setPrefs(next);
    },
    [],
  );
  const toggleFavorite = useCallback(
    (key: string) =>
      update((p) => ({
        ...p,
        favorites: p.favorites.includes(key)
          ? p.favorites.filter((k) => k !== key)
          : [...p.favorites, key],
      })),
    [update],
  );
  const addRecent = useCallback(
    (key: string) =>
      update((p) => ({
        ...p,
        recent: [key, ...p.recent.filter((k) => k !== key)].slice(
          0,
          RECENT_LIMIT,
        ),
      })),
    [update],
  );
  return { prefs, toggleFavorite, addRecent };
}
