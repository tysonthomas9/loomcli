/**
 * PerWorkspacePrefsProvider — holds per-workspace preference state.
 *
 * Mounted with key={workspaceId} by WorkspaceProvider, so on workspace switch
 * this subtree remounts cleanly and `selectedRepoNames` re-initializes from
 * the new workspace's scoped localStorage without an effect-sync dance.
 *
 * The provider is intentionally narrow — TerminalView, the WebSocket tree,
 * and anything with expensive remount cost live OUTSIDE this boundary in the
 * parent `WorkspaceProvider`, so the keyed remount is cheap (resets a `Set`
 * and reads localStorage).
 *
 * Extracted from useWorkspaceContext.tsx to keep that file under the
 * project's 500-LOC file ceiling (check-loc.mjs).
 */

import {
  createContext,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import type { RepoInfo } from "@/api/workspace";
import { wsGet, wsSet } from "@/utils/scopedStorage";

const SK_SELECTED_REPOS = "selected-repos";

export interface PerWorkspacePrefsContextValue {
  selectedRepoNames: Set<string>;
  activeRepos: RepoInfo[];
  activeRepoNames: string[];
  isAllSelected: boolean;
  selectRepos: (names: string[]) => void;
  selectAll: () => void;
  toggleRepo: (name: string) => void;
  sourceReposFilter: string[] | undefined;
}

export const PerWorkspacePrefsContext = createContext<
  PerWorkspacePrefsContextValue | undefined
>(undefined);

/**
 * Read initial selected repos from scoped localStorage.
 */
function readStoredRepoSelection(wsId: string): Set<string> {
  const raw = wsGet(wsId, SK_SELECTED_REPOS);
  if (raw === null) return new Set<string>();
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return new Set(
        parsed.filter((s: unknown): s is string => typeof s === "string"),
      );
    }
  } catch {
    // Invalid JSON
  }
  return new Set<string>();
}

export interface PerWorkspacePrefsProviderProps {
  workspaceId: string;
  repos: RepoInfo[];
  children: ReactNode;
}

export function PerWorkspacePrefsProvider({
  workspaceId,
  repos,
  children,
}: PerWorkspacePrefsProviderProps): JSX.Element {
  const [selectedRepoNames, setSelectedRepoNames] = useState<Set<string>>(() =>
    readStoredRepoSelection(workspaceId),
  );

  // Validate selected repos against the server-reported repo list. This is a
  // legitimate effect: state responds to external data (polled repo list),
  // not a prop-mirror dance. It only runs when the actual repo list content
  // changes — upstream equality check in the loader ensures stable references.
  useEffect(() => {
    if (repos.length === 0) return;

    setSelectedRepoNames((prev) => {
      if (prev.size === 0) return prev;
      const validNames = new Set(repos.map((r) => r.name));
      const cleaned = new Set<string>();
      let changed = false;
      for (const name of prev) {
        if (validNames.has(name)) {
          cleaned.add(name);
        } else {
          changed = true;
        }
      }
      if (!changed) return prev;
      if (cleaned.size === 0) {
        wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([]));
        return new Set<string>();
      }
      wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([...cleaned]));
      return cleaned;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repos]);

  const selectRepos = useCallback(
    (names: string[]) => {
      const next = new Set(names);
      setSelectedRepoNames(next);
      wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify(names));
    },
    [workspaceId],
  );

  const selectAll = useCallback(() => {
    setSelectedRepoNames(new Set<string>());
    wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([]));
  }, [workspaceId]);

  const toggleRepo = useCallback(
    (name: string) => {
      setSelectedRepoNames((prev) => {
        const next = new Set(prev);
        if (next.has(name)) {
          next.delete(name);
        } else {
          next.add(name);
        }
        wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([...next]));
        return next;
      });
    },
    [workspaceId],
  );

  const isAllSelected = useMemo(
    () => selectedRepoNames.size === 0,
    [selectedRepoNames],
  );

  const activeRepos = useMemo(() => {
    if (isAllSelected) return repos;
    return repos.filter((r) => selectedRepoNames.has(r.name));
  }, [repos, selectedRepoNames, isAllSelected]);

  const activeRepoNames = useMemo(
    () => activeRepos.map((r) => r.name),
    [activeRepos],
  );

  const sourceReposFilter = useMemo(
    () => (isAllSelected ? undefined : activeRepoNames),
    [isAllSelected, activeRepoNames],
  );

  const value = useMemo<PerWorkspacePrefsContextValue>(
    () => ({
      selectedRepoNames,
      activeRepos,
      activeRepoNames,
      isAllSelected,
      selectRepos,
      selectAll,
      toggleRepo,
      sourceReposFilter,
    }),
    [
      selectedRepoNames,
      activeRepos,
      activeRepoNames,
      isAllSelected,
      selectRepos,
      selectAll,
      toggleRepo,
      sourceReposFilter,
    ],
  );

  return (
    <PerWorkspacePrefsContext.Provider value={value}>
      {children}
    </PerWorkspacePrefsContext.Provider>
  );
}
