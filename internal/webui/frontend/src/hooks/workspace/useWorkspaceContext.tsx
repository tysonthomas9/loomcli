/**
 * useWorkspaceContext — React context for sharing workspace data across
 * components.
 *
 * Refactored per docs/design/workspace-provider-refactor.md (adapted to the
 * v2-refactor zustand-store architecture):
 *
 *   - Workspace data is owned by a per-provider zustand store that polls the
 *     server. WorkspaceProvider derives all workspace metadata from the
 *     store state via useStore selectors — no useState/useEffect dance to
 *     mirror store data into local component state. Satisfies the Vercel
 *     react-best-practices rule `rerender-derived-state-no-effect`.
 *
 *   - Per-workspace preferences (selectedRepoNames) live in
 *     PerWorkspacePrefsProvider keyed on workspaceId. That provider is the
 *     only thing that remounts on workspace switch, so per-workspace state
 *     resets automatically while the outer WorkspaceProvider, TerminalView,
 *     agent state, and the WebSocket tree — which live outside the keyed
 *     boundary — stay mounted. No terminal teardown+reconnect per switch.
 *
 *   - setActiveWorkspace navigates with `flushSync: true` and preserves the
 *     `view=` query param. React Router v7 wraps navigate() in
 *     startTransition by default; during heavy terminal/WebSocket work the
 *     transition gets indefinitely deferred, the URL changes via
 *     history.pushState but useParams never commits, and the UI freezes on
 *     the old workspaceId. flushSync forces a synchronous route commit.
 *
 *   - The public useWorkspaceContext() hook merges both inner contexts into
 *     one WorkspaceContextValue shape — UI consumers see no API change
 *     (composition-patterns rule: state-decouple-implementation).
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate } from "react-router-dom";
import { useStore } from "zustand";

import type {
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceData,
} from "@/api/workspace";
import { setLastWorkspaceId } from "@/utils/scopedStorage";
import { createWorkspaceStore } from "@/stores";
import { buildWorkspaceSwitchUrl } from "@/utils/workspaceUrl";

import type { UseWorkspaceReturn } from "./useWorkspace";
import {
  PerWorkspacePrefsContext,
  PerWorkspacePrefsProvider,
} from "./PerWorkspacePrefsProvider";

// Stable empty arrays — avoid new [] references when workspace fields are empty
const EMPTY_REPOS: RepoInfo[] = [];
const EMPTY_GROUPS: string[] = [];
const EMPTY_AGENTS: WorkspaceAgentInfo[] = [];

// -------------------- Public value shape --------------------

/**
 * Public context value exposed by useWorkspaceContext(). Merged from the
 * outer (workspace data) and inner (per-workspace prefs) contexts, but UI
 * consumers don't care about the split.
 */
export interface WorkspaceContextValue extends UseWorkspaceReturn {
  upsertAgent?: (agent: WorkspaceAgentInfo) => void;
  getRepoByName: (name: string) => RepoInfo | undefined;
  getReposByGroup: (group: string) => RepoInfo[];
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;

  workspaceId: string;
  activeWorkspaceName: string | null;
  setActiveWorkspace: (name: string) => void;

  selectedRepoNames: Set<string>;
  activeRepos: RepoInfo[];
  activeRepoNames: string[];
  isAllSelected: boolean;
  selectRepos: (names: string[]) => void;
  selectAll: () => void;
  toggleRepo: (name: string) => void;
  sourceReposFilter: string[] | undefined;
  isMultiRepo: boolean;
}

export const WorkspaceContext = createContext<
  WorkspaceContextValue | undefined
>(undefined);

// -------------------- Outer context (workspace data + global prefs) --------------------

interface OuterContextValue {
  workspace: WorkspaceData | null;
  workspaceId: string;
  activeWorkspaceName: string | null;
  repos: RepoInfo[];
  groups: string[];
  agents: WorkspaceAgentInfo[];
  isLoading: boolean;
  error: string | null;
  refetch: () => void;
  upsertAgent: (agent: WorkspaceAgentInfo) => void;
  getRepoByName: (name: string) => RepoInfo | undefined;
  getReposByGroup: (group: string) => RepoInfo[];
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;
  setActiveWorkspace: (name: string) => void;
  isMultiRepo: boolean;
}

const OuterWorkspaceContext = createContext<OuterContextValue | undefined>(
  undefined,
);

// -------------------- Provider --------------------

export interface WorkspaceProviderProps {
  /** Workspace UUID from route param */
  workspaceId: string;
  children: ReactNode;
}

/**
 * WorkspaceProvider wraps workspace-scoped content. Receives `workspaceId`
 * from route params (via WorkspaceLayout), owns a zustand store that polls
 * the server for workspace data, and derives all workspace metadata from
 * the store. Wraps PerWorkspacePrefsProvider with key={workspaceId} so
 * per-workspace preferences reset automatically on workspace change.
 */
export function WorkspaceProvider({
  workspaceId,
  children,
}: WorkspaceProviderProps): JSX.Element {
  const navigate = useNavigate();

  // Workspace store — one instance per provider lifetime.
  const storeRef = useRef(createWorkspaceStore());
  const store = storeRef.current;

  // Start/stop polling keyed on workspaceId. When workspaceId changes (SPA
  // workspace switch), we stop the current poll and start a new one. Track
  // last-workspace-id for the root redirect at the same time.
  useEffect(() => {
    setLastWorkspaceId(workspaceId);
    store.getState().startPolling({ workspaceId, pollInterval: 60000 });
    return () => store.getState().stopPolling();
  }, [workspaceId, store]);

  // Set last workspace synchronously on first render for the root redirect.
  useState(() => {
    setLastWorkspaceId(workspaceId);
  });

  const workspace = useStore(store, (s) => s.workspace);
  const wsIsLoading = useStore(store, (s) => s.isLoading);
  const wsError = useStore(store, (s) => s.error);
  const refetch = useCallback(() => store.getState().refetch(), [store]);
  const upsertAgent = useCallback(
    (agent: WorkspaceAgentInfo) => store.getState().upsertAgent(agent),
    [store],
  );

  // Latest-workspace ref for stable callbacks. Callbacks read from this so
  // their identities can stay empty-deps stable across renders. Without it,
  // every change to workspace data would re-create the callbacks, cascade
  // through the outer context value memo, and re-render every consumer
  // (including PerWorkspacePrefsProvider) for no real reason.
  const workspaceRef = useRef<WorkspaceData | null>(workspace);
  workspaceRef.current = workspace;

  // Workspace switch: build the destination URL via buildWorkspaceSwitchUrl
  // (preserves `view=`, drops everything else) and navigate with
  // flushSync: true.
  //
  // flushSync: true is REQUIRED here, not optional. React Router v7 wraps
  // navigate() in startTransition by default. When the user is on the
  // terminal view, renderer + WebSocket-driven re-renders feed React's
  // urgent-work queue continuously, and the transition gets indefinitely
  // deferred. The browser URL updates via history.pushState (visible in the
  // address bar), but useParams() never commits the new value, so
  // WorkspaceLayout keeps reading the old workspaceId and the UI freezes on
  // the old workspace. This is the original bug that motivated the refactor.
  const setActiveWorkspace = useCallback(
    (name: string) => {
      const workspaces = workspaceRef.current?.workspaces ?? [];
      const target = workspaces.find((w) => w.name === name);
      if (!target || target.id === workspaceRef.current?.id) return;
      navigate(buildWorkspaceSwitchUrl(target.id), { flushSync: true });
    },
    // navigate is stable; workspaceRef is read at call time, not create time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // Derived read-only values from store. Zero useState.
  const activeWorkspaceName = workspace?.name ?? null;
  const repos = workspace?.repos ?? EMPTY_REPOS;
  const groups = workspace?.groups ?? EMPTY_GROUPS;
  const agents = workspace?.agents ?? EMPTY_AGENTS;
  const isMultiRepo = repos.length > 1;

  const getRepoByName = useCallback(
    (name: string): RepoInfo | undefined => repos.find((r) => r.name === name),
    [repos],
  );

  const getReposByGroup = useCallback(
    (group: string): RepoInfo[] =>
      repos.filter((r) => r.groups && r.groups.includes(group)),
    [repos],
  );

  const getAgentByName = useCallback(
    (name: string): WorkspaceAgentInfo | undefined =>
      agents.find((a) => a.name === name),
    [agents],
  );

  const outerValue = useMemo<OuterContextValue>(
    () => ({
      workspace,
      workspaceId,
      activeWorkspaceName,
      repos,
      groups,
      agents,
      isLoading: wsIsLoading,
      error: wsError,
      refetch,
      upsertAgent,
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      setActiveWorkspace,
      isMultiRepo,
    }),
    [
      workspace,
      workspaceId,
      activeWorkspaceName,
      repos,
      groups,
      agents,
      wsIsLoading,
      wsError,
      refetch,
      upsertAgent,
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      setActiveWorkspace,
      isMultiRepo,
    ],
  );

  return (
    <OuterWorkspaceContext.Provider value={outerValue}>
      {/*
       * key={workspaceId} forces React to unmount+remount the inner provider
       * on workspace change, which re-initializes `selectedRepoNames` from
       * the new workspace's scoped localStorage without an effect-sync
       * dance. The outer provider and its children (TerminalView, agents,
       * WebSocket tree) stay mounted — only the narrow per-workspace prefs
       * subtree resets.
       */}
      <PerWorkspacePrefsProvider
        key={workspaceId}
        workspaceId={workspaceId}
        repos={repos}
      >
        {children}
      </PerWorkspacePrefsProvider>
    </OuterWorkspaceContext.Provider>
  );
}

// -------------------- Public hook --------------------

/** Default no-op value returned when useWorkspaceContext is called outside a provider. */
export const NO_WORKSPACE_CONTEXT: WorkspaceContextValue = {
  workspace: null,
  repos: [],
  groups: [],
  agents: [],
  isLoading: false,
  error: null,
  refetch: () => {},
  upsertAgent: () => {},
  getRepoByName: () => undefined,
  getReposByGroup: () => [],
  getAgentByName: () => undefined,
  workspaceId: "",
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
 * Access workspace context. Merges the outer (workspace data) and inner
 * (per-workspace prefs) contexts into one WorkspaceContextValue so UI
 * consumers see the same shape as before the split.
 *
 * Returns safe defaults when used outside a WorkspaceProvider (e.g. tests).
 */
export function useWorkspaceContext(): WorkspaceContextValue {
  const outer = useContext(OuterWorkspaceContext);
  const inner = useContext(PerWorkspacePrefsContext);

  if (!outer || !inner) {
    return NO_WORKSPACE_CONTEXT;
  }

  return {
    workspace: outer.workspace,
    repos: outer.repos,
    groups: outer.groups,
    agents: outer.agents,
    isLoading: outer.isLoading,
    error: outer.error,
    refetch: outer.refetch,
    upsertAgent: outer.upsertAgent,
    getRepoByName: outer.getRepoByName,
    getReposByGroup: outer.getReposByGroup,
    getAgentByName: outer.getAgentByName,
    workspaceId: outer.workspaceId,
    activeWorkspaceName: outer.activeWorkspaceName,
    setActiveWorkspace: outer.setActiveWorkspace,
    selectedRepoNames: inner.selectedRepoNames,
    activeRepos: inner.activeRepos,
    activeRepoNames: inner.activeRepoNames,
    isAllSelected: inner.isAllSelected,
    selectRepos: inner.selectRepos,
    selectAll: inner.selectAll,
    toggleRepo: inner.toggleRepo,
    sourceReposFilter: inner.sourceReposFilter,
    isMultiRepo: outer.isMultiRepo,
  };
}
