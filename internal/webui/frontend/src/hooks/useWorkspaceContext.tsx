/**
 * useWorkspaceContext - React context for sharing workspace data across components.
 * Wraps useWorkspace() so a single polling loop serves all consumers.
 * Follows useAgentContext pattern.
 */

import { createContext, useContext, useCallback, type ReactNode } from "react";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

import { useWorkspace } from "./useWorkspace";
import type { UseWorkspaceReturn } from "./useWorkspace";

/**
 * Context value exposed by WorkspaceProvider.
 * Extends UseWorkspaceReturn with helpers for repo/group lookups.
 */
export interface WorkspaceContextValue extends UseWorkspaceReturn {
  /** Look up a repo by name. Returns undefined if not found. */
  getRepoByName: (name: string) => RepoInfo | undefined;
  /** Get all repos belonging to a specific group. */
  getReposByGroup: (group: string) => RepoInfo[];
  /** Get agent info by name. Returns undefined if not found. */
  getAgentByName: (name: string) => WorkspaceAgentInfo | undefined;
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
 * WorkspaceProvider wraps the app and provides workspace data to all children.
 * Internally manages a single useWorkspace() polling loop (60s interval).
 */
export function WorkspaceProvider({
  children,
}: WorkspaceProviderProps): JSX.Element {
  const workspaceResult = useWorkspace({ pollInterval: 60000 });

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

  const value: WorkspaceContextValue = {
    ...workspaceResult,
    getRepoByName,
    getReposByGroup,
    getAgentByName,
  };

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
};

/**
 * Hook to access workspace context.
 * Returns safe defaults when used outside a WorkspaceProvider (e.g., in tests).
 */
export function useWorkspaceContext(): WorkspaceContextValue {
  const context = useContext(WorkspaceContext);
  return context ?? NO_WORKSPACE_CONTEXT;
}
