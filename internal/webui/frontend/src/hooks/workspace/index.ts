/**
 * Workspace hooks barrel.
 */

export { useBackendConfig } from "./useBackendConfig";
export type { UseBackendConfigReturn } from "./useBackendConfig";

export { useBackends } from "./useBackends";
export type { UseBackendsReturn } from "./useBackends";

export { useDaemonHealth } from "./useDaemonHealth";
export type {
  DaemonConnectionMode,
  UseDaemonHealthReturn,
} from "./useDaemonHealth";

export { useGitActions } from "./useGitActions";
export type {
  UseGitActionsOptions,
  UseGitActionsReturn,
  GitActionState,
} from "./useGitActions";

export { useGitStatus } from "./useGitStatus";
export type { UseGitStatusOptions, UseGitStatusReturn } from "./useGitStatus";

export { useRepoFilter, parseReposFromUrl } from "./useRepoFilter";
export type {
  UseRepoFilterOptions,
  UseRepoFilterReturn,
} from "./useRepoFilter";

export {
  useRepoFilterParam,
  parseRepoFilterFromUrl,
} from "./useRepoFilterParam";
export type {
  UseRepoFilterParamOptions,
  UseRepoFilterParamReturn,
} from "./useRepoFilterParam";

export {
  WorkspaceProvider,
  useWorkspaceContext,
  WorkspaceContext,
  NO_WORKSPACE_CONTEXT,
} from "./useWorkspaceContext";
export type {
  WorkspaceContextValue,
  WorkspaceConnectionState,
  WorkspaceProviderProps,
} from "./useWorkspaceContext";

export {
  useWorkspaceState,
  clearWorkspaceSnapshots,
} from "./useWorkspaceState";
export type {
  WorkspaceSnapshot,
  UseWorkspaceStateParams,
} from "./useWorkspaceState";

export { useWorkspaceTree } from "./useWorkspaceTree";
export type { EpicWithTasks, UseWorkspaceTreeReturn } from "./useWorkspaceTree";
