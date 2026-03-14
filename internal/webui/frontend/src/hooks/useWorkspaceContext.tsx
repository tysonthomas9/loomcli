/**
 * useWorkspaceContext - React context for sharing workspace data across components.
 * Centralizes workspace state with workspace-level selection, repo-level filtering,
 * and localStorage persistence. This is THE canonical workspace hook.
 * Follows useAgentContext pattern.
 */

import {
  createContext,
  useContext,
  useCallback,
  useState,
  useEffect,
  useMemo,
  type ReactNode,
} from "react";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

import { useWorkspace as useWorkspaceData } from "./useWorkspace";
import type { UseWorkspaceReturn } from "./useWorkspace";

// localStorage keys
const LS_ACTIVE_WORKSPACE = "loom-active-workspace";
const LS_SELECTED_REPOS = "loom-selected-repos";

/**
 * Safe localStorage getter.
 */
function lsGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

/**
 * Safe localStorage setter.
 */
function lsSet(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Private browsing or quota exceeded — silently ignore
  }
}

/**
 * Context value exposed by WorkspaceProvider.
 * Extends UseWorkspaceReturn with workspace/repo selection and helpers.
 */
export interface WorkspaceContextValue extends UseWorkspaceReturn {
  /** Look up a repo by name. Returns undefined if not found. */
  getRepoByName: (name: string) => RepoInfo | undefined;
  /** Get all repos belonging to a specific group. */
  getReposByGroup: (group: string) => RepoInfo[];
  /** Get agent info by name. Returns undefined if not found. */
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;

  /** Name of the active workspace */
  activeWorkspaceName: string | null;
  /** Switch workspace (future multi-workspace) */
  setActiveWorkspace: (name: string) => void;

  /** Selected repo names. Empty Set = "all repos" */
  selectedRepoNames: Set<string>;
  /** Filtered repos (or all if none selected) */
  activeRepos: RepoInfo[];
  /** Convenience: just the names of active repos */
  activeRepoNames: string[];
  /** True when no repo filter applied */
  isAllSelected: boolean;

  /** Set specific repos as selected */
  selectRepos: (names: string[]) => void;
  /** Clear repo filter — show all */
  selectAll: () => void;
  /** Toggle single repo in/out of selection */
  toggleRepo: (name: string) => void;

  /** Repo names for API filtering, undefined = no filter */
  sourceReposFilter: string[] | undefined;
  /** True when workspace has 2+ repos */
  isMultiRepo: boolean;
}

const WorkspaceContext = createContext<WorkspaceContextValue | undefined>(
  undefined,
);

/**
 * Props for WorkspaceProvider.
 */
export interface WorkspaceProviderProps {
  children: ReactNode;
}

/**
 * Read initial selected repos from localStorage.
 * Returns empty Set (meaning "all") if not found or invalid.
 */
function readStoredRepoSelection(): Set<string> {
  const raw = lsGet(LS_SELECTED_REPOS);
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

/**
 * WorkspaceProvider wraps the app and provides workspace data to all children.
 * Manages workspace-level selection, repo-level filtering, and localStorage persistence.
 */
export function WorkspaceProvider({
  children,
}: WorkspaceProviderProps): JSX.Element {
  const workspaceResult = useWorkspaceData({ pollInterval: 60000 });

  // Workspace-level selection
  const [activeWorkspaceName, setActiveWorkspaceNameRaw] = useState<
    string | null
  >(() => lsGet(LS_ACTIVE_WORKSPACE));

  // Repo-level selection
  const [selectedRepoNames, setSelectedRepoNames] = useState<Set<string>>(
    () => readStoredRepoSelection(),
  );

  // Sync activeWorkspaceName when workspace data loads
  useEffect(() => {
    if (workspaceResult.workspace) {
      const wsName = workspaceResult.workspace.name;
      setActiveWorkspaceNameRaw((prev) => {
        if (prev !== wsName) {
          lsSet(LS_ACTIVE_WORKSPACE, wsName);
          return wsName;
        }
        return prev;
      });
    }
  }, [workspaceResult.workspace]);

  // Stable key for repo list — avoids re-running cleanup on every poll tick
  const repoNamesKey = useMemo(
    () => workspaceResult.repos.map((r) => r.name).join(","),
    [workspaceResult.repos],
  );

  // Validate selected repos against actual repo list — discard stale entries
  useEffect(() => {
    if (workspaceResult.repos.length === 0) return;

    setSelectedRepoNames((prev) => {
      if (prev.size === 0) return prev; // "all" — nothing to validate
      const validNames = new Set(workspaceResult.repos.map((r) => r.name));
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
      // If all selected repos were stale, fall back to "all"
      if (cleaned.size === 0) {
        lsSet(LS_SELECTED_REPOS, JSON.stringify([]));
        return new Set<string>();
      }
      lsSet(LS_SELECTED_REPOS, JSON.stringify([...cleaned]));
      return cleaned;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoNamesKey]);

  // Workspace selection action
  const setActiveWorkspace = useCallback((name: string) => {
    setActiveWorkspaceNameRaw(name);
    lsSet(LS_ACTIVE_WORKSPACE, name);
  }, []);

  // Repo selection actions
  const selectRepos = useCallback((names: string[]) => {
    const next = new Set(names);
    setSelectedRepoNames(next);
    lsSet(LS_SELECTED_REPOS, JSON.stringify(names));
  }, []);

  const selectAll = useCallback(() => {
    setSelectedRepoNames(new Set<string>());
    lsSet(LS_SELECTED_REPOS, JSON.stringify([]));
  }, []);

  const toggleRepo = useCallback((name: string) => {
    setSelectedRepoNames((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        next.delete(name);
      } else {
        next.add(name);
      }
      lsSet(LS_SELECTED_REPOS, JSON.stringify([...next]));
      return next;
    });
  }, []);

  // Derived: active repos
  const isAllSelected = useMemo(
    () => selectedRepoNames.size === 0,
    [selectedRepoNames],
  );
  const activeRepos = useMemo(() => {
    if (isAllSelected) return workspaceResult.repos;
    return workspaceResult.repos.filter((r) => selectedRepoNames.has(r.name));
  }, [workspaceResult.repos, selectedRepoNames, isAllSelected]);

  const activeRepoNames = useMemo(
    () => activeRepos.map((r) => r.name),
    [activeRepos],
  );

  // Derived: sourceReposFilter for API filtering
  const sourceReposFilter = useMemo(() => {
    if (isAllSelected) return undefined;
    return activeRepos.map((r) => r.name);
  }, [isAllSelected, activeRepos]);

  const isMultiRepo = useMemo(
    () => workspaceResult.repos.length >= 2,
    [workspaceResult.repos],
  );

  // Existing helper methods
  const getRepoByName = useCallback(
    (name: string): RepoInfo | undefined => {
      return workspaceResult.repos.find((r) => r.name === name);
    },
    [workspaceResult.repos],
  );

  const getReposByGroup = useCallback(
    (group: string): RepoInfo[] => {
      return workspaceResult.repos.filter(
        (r) => r.groups && r.groups.includes(group),
      );
    },
    [workspaceResult.repos],
  );

  const getAgentByName = useCallback(
    (name: string): WorkspaceAgentInfo | undefined => {
      return workspaceResult.agents.find((a) => a.name === name);
    },
    [workspaceResult.agents],
  );

  const value = useMemo<WorkspaceContextValue>(
    () => ({
      ...workspaceResult,
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      activeWorkspaceName,
      setActiveWorkspace,
      selectedRepoNames,
      activeRepos,
      activeRepoNames,
      isAllSelected,
      selectRepos,
      selectAll,
      toggleRepo,
      sourceReposFilter,
      isMultiRepo,
    }),
    [
      workspaceResult,
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      activeWorkspaceName,
      setActiveWorkspace,
      selectedRepoNames,
      activeRepos,
      activeRepoNames,
      isAllSelected,
      selectRepos,
      selectAll,
      toggleRepo,
      sourceReposFilter,
      isMultiRepo,
    ],
  );

  return (
    <WorkspaceContext.Provider value={value}>
      {children}
    </WorkspaceContext.Provider>
  );
}

/** Default no-op value returned when useWorkspaceContext is called outside a provider. */
const NO_WORKSPACE_CONTEXT: WorkspaceContextValue = {
  workspace: null,
  repos: [],
  groups: [],
  agents: [],
  isLoading: false,
  error: null,
  refetch: () => {},
  getRepoByName: () => undefined,
  getReposByGroup: () => [],
  getAgentByName: () => undefined,
  activeWorkspaceName: null,
  setActiveWorkspace: () => {},
  selectedRepoNames: new Set<string>(),
  activeRepos: [],
  activeRepoNames: [],
  isAllSelected: true,
  selectRepos: () => {},
  selectAll: () => {},
  toggleRepo: () => {},
  sourceReposFilter: undefined,
  isMultiRepo: false,
};

/**
 * Hook to access workspace context.
 * Returns safe defaults when used outside a WorkspaceProvider (e.g., in tests).
 */
export function useWorkspaceContext(): WorkspaceContextValue {
  const context = useContext(WorkspaceContext);
  return context ?? NO_WORKSPACE_CONTEXT;
}
