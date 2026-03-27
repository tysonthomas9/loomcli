/**
 * useWorkspaceContext - React context for sharing workspace data across components.
 * Centralizes workspace state with workspace-level selection, repo-level filtering,
 * and localStorage persistence. This is THE canonical workspace hook.
 * Follows useAgentContext pattern.
 *
 * T12 changes: accepts workspaceId prop from route params instead of reading
 * from localStorage/URL. Removed _ws URL param hack and full-page reload switching.
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
import { useNavigate } from "react-router-dom";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";
import {
  setDefaultWorkspace as setDefaultWorkspaceApi,
  clearDefaultWorkspace as clearDefaultWorkspaceApi,
  refreshWorkspace,
  invalidateWorkspaceCache,
} from "@/api/workspace";
import { wsGet, wsSet, setLastWorkspaceId } from "@/utils/scopedStorage";

import { useWorkspace as useWorkspaceData } from "./useWorkspace";
import type { UseWorkspaceReturn } from "./useWorkspace";

// localStorage keys
const LS_DEFAULT_WORKSPACE = "loom-default-workspace";
// Scoped key suffix for selected repos (stored as loom:{wsId}:selected-repos)
const SK_SELECTED_REPOS = "selected-repos";

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

  /** Stable workspace UUID from route params. */
  workspaceId: string;

  /** Name of the active workspace */
  activeWorkspaceName: string | null;
  /** Navigate to a different workspace (SPA navigation, no page reload) */
  setActiveWorkspace: (name: string) => void;

  /** Name of the default workspace, null if none set */
  defaultWorkspaceName: string | null;
  /** Set or clear the default workspace (null = clear) */
  setDefaultWorkspace: (name: string | null) => Promise<void>;

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
  /** Workspace UUID from route param */
  workspaceId: string;
  children: ReactNode;
}

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

/**
 * WorkspaceProvider wraps workspace-scoped content and provides workspace data.
 * Receives workspaceId from route params (via WorkspaceLayout).
 */
export function WorkspaceProvider({
  workspaceId,
  children,
}: WorkspaceProviderProps): JSX.Element {
  const navigate = useNavigate();

  // Track last workspace for root redirect
  useEffect(() => {
    setLastWorkspaceId(workspaceId);
  }, [workspaceId]);

  // Set last workspace synchronously on first render
  useState(() => {
    setLastWorkspaceId(workspaceId);
  });

  const workspaceResult = useWorkspaceData({ pollInterval: 60000 });

  // Track workspace name for display and localStorage
  const [activeWorkspaceName, setActiveWorkspaceNameRaw] = useState<
    string | null
  >(null);

  // Default workspace state (fast-path from localStorage, synced from server)
  const [defaultWorkspaceName, setDefaultWorkspaceNameRaw] = useState<
    string | null
  >(() => lsGet(LS_DEFAULT_WORKSPACE));

  // Repo-level selection
  const [selectedRepoNames, setSelectedRepoNames] = useState<Set<string>>(() =>
    readStoredRepoSelection(workspaceId),
  );

  // Sync activeWorkspaceName when workspace data loads
  useEffect(() => {
    if (workspaceResult.workspace) {
      const wsName = workspaceResult.workspace.name;
      setActiveWorkspaceNameRaw((prev) => (prev !== wsName ? wsName : prev));
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
      if (cleaned.size === 0) {
        wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([]));
        return new Set<string>();
      }
      wsSet(workspaceId, SK_SELECTED_REPOS, JSON.stringify([...cleaned]));
      return cleaned;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoNamesKey]);

  // Sync default workspace from server response
  useEffect(() => {
    const serverDefault = workspaceResult.workspace?.default_workspace;
    if (serverDefault !== undefined) {
      setDefaultWorkspaceNameRaw(serverDefault || null);
      lsSet(LS_DEFAULT_WORKSPACE, serverDefault || "");
    }
  }, [workspaceResult.workspace]);

  // Workspace switch — SPA navigation via React Router (no page reload)
  const setActiveWorkspace = useCallback(
    (name: string) => {
      // Find the workspace ID for the given name from the workspace list
      const workspaces = workspaceResult.workspace?.workspaces ?? [];
      const target = workspaces.find((ws) => ws.name === name);
      if (target) {
        // Invalidate cached workspace data before navigating to prevent
        // stale data from the old workspace being served to new components
        invalidateWorkspaceCache();
        navigate(`/ws/${target.id}/`);
      }
    },
    [workspaceResult.workspace, navigate],
  );

  // Set or clear the default workspace with optimistic update
  const setDefaultWorkspace = useCallback(
    async (name: string | null) => {
      const previous = defaultWorkspaceName;
      setDefaultWorkspaceNameRaw(name);
      lsSet(LS_DEFAULT_WORKSPACE, name ?? "");
      try {
        if (name) {
          await setDefaultWorkspaceApi(name);
        } else {
          await clearDefaultWorkspaceApi();
        }
        await refreshWorkspace();
      } catch (err) {
        setDefaultWorkspaceNameRaw(previous);
        lsSet(LS_DEFAULT_WORKSPACE, previous ?? "");
        throw err;
      }
    },
    [defaultWorkspaceName],
  );

  // Repo selection actions — use workspace-scoped storage
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
    () => workspaceResult.repos.length >= 1,
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
      workspaceId,
      activeWorkspaceName,
      setActiveWorkspace,
      defaultWorkspaceName,
      setDefaultWorkspace,
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
      workspaceId,
      activeWorkspaceName,
      setActiveWorkspace,
      defaultWorkspaceName,
      setDefaultWorkspace,
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
  workspaceId: "",
  activeWorkspaceName: null,
  setActiveWorkspace: () => {},
  defaultWorkspaceName: null,
  setDefaultWorkspace: () => Promise.resolve(),
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
