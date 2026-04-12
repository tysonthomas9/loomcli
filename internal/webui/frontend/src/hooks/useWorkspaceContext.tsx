/**
 * useWorkspaceContext — React context for sharing workspace data across
 * components.
 *
 * Refactored per docs/design/workspace-provider-refactor.md:
 *
 *   - Workspace data is loaded by the router loader (WorkspaceLayout/loader.ts).
 *     WorkspaceProvider receives `workspace: WorkspaceData` as a prop, fully
 *     loaded. There is no internal useState/useEffect dance to sync derived
 *     fields — everything is derived from the prop. Satisfies the Vercel
 *     react-best-practices rule `rerender-derived-state-no-effect`.
 *
 *   - Per-workspace preferences (selectedRepoNames) live in
 *     PerWorkspacePrefsProvider keyed on workspace.id. That provider is the
 *     only thing that remounts on workspace switch, so per-workspace state
 *     resets automatically while TerminalView, agent state, and the WebSocket
 *     tree — which live outside the keyed boundary — are preserved.
 *
 *   - defaultWorkspaceName is a cross-workspace preference. It's derived from
 *     the workspace prop (`workspace.default_workspace`) with a ref-based
 *     optimistic override for in-flight user updates — no effect-sync.
 *
 *   - refetch() delegates to React Router's useRevalidator so the loader is
 *     the single source of truth for freshness.
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
import { useNavigate, useRevalidator } from "react-router-dom";

import type {
  RepoInfo,
  WorkspaceData,
  WorkspaceAgentInfo,
} from "@/api/workspace";
import {
  setDefaultWorkspace as setDefaultWorkspaceApi,
  clearDefaultWorkspace as clearDefaultWorkspaceApi,
  invalidateWorkspaceCache,
} from "@/api/workspace";
import { setLastWorkspaceId } from "@/utils/scopedStorage";
import { buildWorkspaceSwitchUrl } from "@/utils/workspaceUrl";

import {
  PerWorkspacePrefsContext,
  PerWorkspacePrefsProvider,
} from "./PerWorkspacePrefsProvider";

// localStorage keys
const LS_DEFAULT_WORKSPACE = "loom-default-workspace";

// Stable empty arrays — avoid new [] references when workspace fields are empty
const EMPTY_REPOS: RepoInfo[] = [];
const EMPTY_GROUPS: string[] = [];
const EMPTY_AGENTS: WorkspaceAgentInfo[] = [];

// -------------------- localStorage helpers --------------------

function lsGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function lsSet(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Private browsing or quota exceeded — silently ignore
  }
}

// -------------------- Public value shape --------------------

/**
 * Public context value exposed by useWorkspaceContext(). Merged from the
 * outer (workspace data) and inner (per-workspace prefs) contexts, but UI
 * consumers don't care about the split.
 */
export interface WorkspaceContextValue {
  // --- Workspace data (derived from router loader) ---
  workspace: WorkspaceData | null;
  repos: RepoInfo[];
  groups: string[];
  agents: WorkspaceAgentInfo[];
  isLoading: boolean;
  error: string | null;
  refetch: () => void;

  // --- Helpers ---
  getRepoByName: (name: string) => RepoInfo | undefined;
  getReposByGroup: (group: string) => RepoInfo[];
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;

  // --- Workspace identity ---
  workspaceId: string;
  activeWorkspaceName: string | null;
  setActiveWorkspace: (name: string) => void;

  // --- Cross-workspace preferences ---
  defaultWorkspaceName: string | null;
  setDefaultWorkspace: (name: string | null) => Promise<void>;

  // --- Per-workspace repo selection ---
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

// -------------------- Outer context (workspace data + global prefs) --------------------

interface OuterContextValue {
  workspace: WorkspaceData;
  workspaceId: string;
  activeWorkspaceName: string;
  repos: RepoInfo[];
  groups: string[];
  agents: WorkspaceAgentInfo[];
  getRepoByName: (name: string) => RepoInfo | undefined;
  getReposByGroup: (group: string) => RepoInfo[];
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;
  setActiveWorkspace: (name: string) => void;
  defaultWorkspaceName: string | null;
  setDefaultWorkspace: (name: string | null) => Promise<void>;
  isMultiRepo: boolean;
  refetch: () => void;
}

const OuterWorkspaceContext = createContext<OuterContextValue | undefined>(
  undefined,
);

// -------------------- Provider: workspace data (unkeyed, derived) --------------------

export interface WorkspaceProviderProps {
  /** Fully-loaded workspace data from the route loader */
  workspace: WorkspaceData;
  children: ReactNode;
}

/**
 * Outer workspace provider. Derives all workspace metadata from the
 * `workspace` prop (supplied by the router loader). Holds no useState for
 * derived workspace fields. The only mutable state is a ref-based optimistic
 * override for `defaultWorkspaceName` during in-flight user updates, which
 * clears on success (loader revalidation) or failure (rollback).
 *
 * Wraps PerWorkspacePrefsProvider with key={workspace.id} so per-workspace
 * preferences reset automatically on workspace change.
 */
export function WorkspaceProvider({
  workspace,
  children,
}: WorkspaceProviderProps): JSX.Element {
  const navigate = useNavigate();
  const revalidator = useRevalidator();
  const workspaceId = workspace.id;

  // ─── Latest-prop refs for stable callbacks ──────────────────────────────
  // Callbacks below read from these refs so their identities can stay
  // empty-deps stable across renders. Without this, every change to
  // `workspace.default_workspace` or `workspace.workspaces` would re-create
  // the callbacks, which would cascade through the outer context value memo
  // and re-render every consumer (including PerWorkspacePrefsProvider) for
  // no real reason. Reviewer finding (a) — re-render efficiency.
  const workspaceRef = useRef(workspace);
  workspaceRef.current = workspace;

  // Track last-workspace-id for the root redirect, synchronized with route
  // changes. Event-driven side effect (navigation commit), fires once per
  // workspace switch.
  useEffect(() => {
    setLastWorkspaceId(workspaceId);
  }, [workspaceId]);

  // Default workspace is derived from workspace.default_workspace (server
  // ground truth) with a ref-based optimistic override for in-flight user
  // updates. No useState + effect-sync dance; satisfies
  // `rerender-derived-state-no-effect`.
  //
  //   - On render: if an optimistic override is active, use it; otherwise
  //     read from workspace.default_workspace (loader) or localStorage
  //     fast-path.
  //   - On setDefaultWorkspace success: clear override and trigger loader
  //     revalidation so the next render picks up fresh server truth.
  //   - On setDefaultWorkspace failure: revert override to previous value,
  //     UI snaps back.
  //
  // overrideTick exists to bump a render when the override ref mutates;
  // nothing reads it directly.
  const optimisticOverrideRef = useRef<{ value: string | null } | null>(null);
  const [, setOverrideTick] = useState(0);
  const bumpOverride = useCallback(() => {
    setOverrideTick((t) => t + 1);
  }, []);

  const defaultWorkspaceName = useMemo<string | null>(() => {
    if (optimisticOverrideRef.current !== null) {
      return optimisticOverrideRef.current.value;
    }
    const serverValue = workspace.default_workspace;
    if (serverValue && serverValue.length > 0) return serverValue;
    return lsGet(LS_DEFAULT_WORKSPACE);
    // The override is mutated outside React's tracking; bumpOverride() forces
    // a re-render and we re-read the ref here. workspace.default_workspace
    // covers the loader-driven refresh path.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace.default_workspace]);

  // Keep localStorage fast-path in sync with server truth. Event-driven by
  // server data, not a prop-mirror: we only write; we don't mirror into
  // React state.
  useEffect(() => {
    const serverValue = workspace.default_workspace;
    if (serverValue !== undefined) {
      lsSet(LS_DEFAULT_WORKSPACE, serverValue);
    }
  }, [workspace.default_workspace]);

  // setDefaultWorkspace reads server truth via workspaceRef so its identity
  // is stable across renders (empty deps). Without this, every server-pushed
  // change to default_workspace would create a new callback and cascade
  // re-renders through outerValue.
  const setDefaultWorkspace = useCallback(
    async (name: string | null) => {
      const currentServer = workspaceRef.current.default_workspace;
      const previous = optimisticOverrideRef.current
        ? optimisticOverrideRef.current.value
        : currentServer || lsGet(LS_DEFAULT_WORKSPACE);

      // Optimistic update
      optimisticOverrideRef.current = { value: name };
      lsSet(LS_DEFAULT_WORKSPACE, name ?? "");
      bumpOverride();

      try {
        if (name) {
          await setDefaultWorkspaceApi(name);
        } else {
          await clearDefaultWorkspaceApi();
        }
        // Clear override; loader revalidation will bring fresh server truth.
        optimisticOverrideRef.current = null;
        bumpOverride();
        revalidator.revalidate();
      } catch (err) {
        // Rollback optimistic update
        optimisticOverrideRef.current = { value: previous };
        lsSet(LS_DEFAULT_WORKSPACE, previous ?? "");
        bumpOverride();
        throw err;
      }
    },
    // workspaceRef and revalidator are stable; bumpOverride is stable (empty
    // deps). Empty dep array gives setDefaultWorkspace a permanent identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // Workspace switch: build the destination URL via buildWorkspaceSwitchUrl
  // (preserves `view=`, drops everything else) and navigate with
  // flushSync: true.
  //
  // flushSync: true is REQUIRED here, not optional. React Router v7 wraps
  // navigate() in startTransition by default. When the user is on the
  // terminal view, xterm.js + WebSocket-driven re-renders feed React's
  // urgent-work queue continuously, and the transition gets indefinitely
  // deferred. The browser URL updates via history.pushState (visible in the
  // address bar), but useParams() never commits the new value, so
  // WorkspaceLayout keeps reading the old workspaceId and the UI freezes on
  // the old workspace. This is the original bug that motivated the refactor.
  // useViewState.ts:98 has the same workaround for the same reason.
  //
  // Reads workspace.workspaces via workspaceRef so the callback identity is
  // stable across loader-driven workspace updates. navigate is stable.
  const setActiveWorkspace = useCallback(
    (name: string) => {
      const target = workspaceRef.current.workspaces.find(
        (w) => w.name === name,
      );
      if (!target || target.id === workspaceRef.current.id) return;
      invalidateWorkspaceCache();
      navigate(buildWorkspaceSwitchUrl(target.id), { flushSync: true });
    },
    // navigate is stable; workspaceRef is read at call time, not at create time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // refetch delegates to the router loader — one source of truth
  const refetch = useCallback(() => {
    revalidator.revalidate();
    // revalidator identity is stable in React Router v7
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Derived read-only values from workspace prop. Zero useState.
  const activeWorkspaceName = workspace.name;
  const repos = workspace.repos ?? EMPTY_REPOS;
  const groups = workspace.groups ?? EMPTY_GROUPS;
  const agents = workspace.agents ?? EMPTY_AGENTS;
  const isMultiRepo = repos.length >= 1;

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
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      setActiveWorkspace,
      defaultWorkspaceName,
      setDefaultWorkspace,
      isMultiRepo,
      refetch,
    }),
    [
      workspace,
      workspaceId,
      activeWorkspaceName,
      repos,
      groups,
      agents,
      getRepoByName,
      getReposByGroup,
      getAgentByName,
      setActiveWorkspace,
      defaultWorkspaceName,
      setDefaultWorkspace,
      isMultiRepo,
      refetch,
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
    // The loader owns initial-load state; once the route renders, data is
    // always present. Polling-in-flight state could be surfaced via
    // useNavigation() / useRevalidator().state, but today's consumers only
    // use isLoading/error for the initial-load path, which no longer exists
    // here. Wire these to useNavigation if a consumer genuinely needs them.
    isLoading: false,
    error: null,
    refetch: outer.refetch,
    getRepoByName: outer.getRepoByName,
    getReposByGroup: outer.getReposByGroup,
    getAgentByName: outer.getAgentByName,
    workspaceId: outer.workspaceId,
    activeWorkspaceName: outer.activeWorkspaceName,
    setActiveWorkspace: outer.setActiveWorkspace,
    defaultWorkspaceName: outer.defaultWorkspaceName,
    setDefaultWorkspace: outer.setDefaultWorkspace,
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
