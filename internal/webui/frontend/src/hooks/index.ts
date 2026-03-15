/**
 * Hook barrel exports for the beads-web-ui frontend.
 */

export { useSSE } from "./useSSE";
export type { UseSSEOptions, UseSSEReturn } from "./useSSE";

export { useDebounce } from "./useDebounce";

export { useSort } from "./useSort";
export type {
  UseSortOptions,
  UseSortReturn,
  SortState,
  SortDirection,
} from "./useSort";

export { useIssueFilter } from "./useIssueFilter";
export type {
  UseIssueFilterOptions,
  UseIssueFilterReturn,
} from "./useIssueFilter";

export { useBlockedIssues } from "./useBlockedIssues";
export type {
  UseBlockedIssuesOptions,
  UseBlockedIssuesResult,
} from "./useBlockedIssues";

export { useMutationHandler } from "./useMutationHandler";
export type {
  UseMutationHandlerOptions,
  UseMutationHandlerReturn,
} from "./useMutationHandler";

export {
  useFilterState,
  toQueryString,
  parseFromUrl,
  isEmptyFilter,
  DEFAULT_GROUP_BY,
} from "./useFilterState";
export type {
  FilterState,
  FilterActions,
  UseFilterStateOptions,
  UseFilterStateReturn,
} from "./useFilterState";

export { useSelection } from "./useSelection";
export type { UseSelectionOptions, UseSelectionReturn } from "./useSelection";

export { useBulkClose } from "./useBulkClose";
export type { UseBulkCloseOptions, UseBulkCloseReturn } from "./useBulkClose";

export { useIssues } from "./useIssues";
export type { UseIssuesOptions, UseIssuesReturn } from "./useIssues";

export { useGraphData } from "./useGraphData";
export type { UseGraphDataOptions, UseGraphDataReturn } from "./useGraphData";

export {
  useBlockedChain,
  getBlockedChain,
  computeAllBlockedCounts,
} from "./useBlockedChain";
export type {
  UseBlockedChainOptions,
  UseBlockedChainReturn,
  BlockedChainResult,
} from "./useBlockedChain";

export { useAutoLayout } from "./useAutoLayout";
export type {
  UseAutoLayoutOptions,
  UseAutoLayoutReturn,
  LayoutDirection,
  RankAlignment,
} from "./useAutoLayout";

export { useViewState } from "./useViewState";
export type { UseViewStateOptions, UseViewStateReturn } from "./useViewState";

export { useIssueDetail } from "./useIssueDetail";
export type { UseIssueDetailReturn } from "./useIssueDetail";

export { useIssueSearch } from "./useIssueSearch";
export type { UseIssueSearchReturn } from "./useIssueSearch";

export { useAgents } from "./useAgents";
export type { UseAgentsOptions, UseAgentsResult } from "./useAgents";

export { AgentProvider, useAgentContext } from "./useAgentContext";
export type { AgentContextValue, AgentProviderProps } from "./useAgentContext";

export { useToast, ToastProvider } from "./useToast";
export type {
  ToastType,
  ToastOptions,
  Toast,
  ToastContextValue,
  ToastProviderProps,
} from "./useToast";

export { useRecentAssignees } from "./useRecentAssignees";
export type { UseRecentAssigneesReturn } from "./useRecentAssignees";

export { useBackendConfig } from "./useBackendConfig";
export type { UseBackendConfigReturn } from "./useBackendConfig";

export { useDaemonHealth } from "./useDaemonHealth";
export type {
  DaemonConnectionMode,
  UseDaemonHealthReturn,
} from "./useDaemonHealth";

export { useTaskLogPolling } from "./useTaskLogPolling";
export type {
  UseTaskLogPollingOptions,
  UseTaskLogPollingReturn,
} from "./useTaskLogPolling";
export type { LogChunk, LogStreamState } from "./logTypes";

export { useAgentTerminalLogs } from "./useAgentTerminalLogs";
export type {
  AgentLogTransportMode,
  UseAgentTerminalLogsOptions,
  UseAgentTerminalLogsReturn,
} from "./useAgentTerminalLogs";

export { useUsage } from "./useUsage";
export type { UseUsageOptions, UseUsageResult } from "./useUsage";

export { useObservabilityMetrics } from "./useObservabilityMetrics";
export type {
  UseObservabilityMetricsOptions,
  UseObservabilityMetricsResult,
} from "./useObservabilityMetrics";

export { useWorkspaceRepos } from "./useWorkspaceRepos";
export type { UseWorkspaceReposReturn } from "./useWorkspaceRepos";

export { useRepoFilter, parseReposFromUrl } from "./useRepoFilter";
export type {
  UseRepoFilterOptions,
  UseRepoFilterReturn,
} from "./useRepoFilter";

export { useWorkspace } from "./useWorkspace";
export type { UseWorkspaceOptions, UseWorkspaceReturn } from "./useWorkspace";

export { WorkspaceProvider, useWorkspaceContext } from "./useWorkspaceContext";
export type {
  WorkspaceContextValue,
  WorkspaceProviderProps,
} from "./useWorkspaceContext";

export { useTheme } from "./useTheme";
export type { Theme, UseThemeReturn } from "./useTheme";

export { useWorkspaceState } from "./useWorkspaceState";
export type {
  WorkspaceSnapshot,
  UseWorkspaceStateParams,
  UseWorkspaceStateReturn,
} from "./useWorkspaceState";

export { useWorkspaceParam, parseWorkspaceFromUrl } from "./useWorkspaceParam";
export type {
  UseWorkspaceParamOptions,
  UseWorkspaceParamReturn,
} from "./useWorkspaceParam";

export { useVirtualList } from "./useVirtualList";
export type {
  UseVirtualListOptions,
  UseVirtualListReturn,
} from "./useVirtualList";

export { useScrollRestore, clearScrollPositions } from "./useScrollRestore";
export type { UseScrollRestoreOptions } from "./useScrollRestore";

export { useEditors } from "./useEditors";
export type { UseEditorsResult } from "./useEditors";

export { useTerminalSessions } from "./useTerminalSessions";
export type { UseTerminalSessionsReturn } from "./useTerminalSessions";

export { useIssueSessionMap } from "./useIssueSessionMap";
export type { UseIssueSessionMapReturn } from "./useIssueSessionMap";

export { useGitStatus } from "./useGitStatus";
export type { UseGitStatusOptions, UseGitStatusReturn } from "./useGitStatus";

export { useGitActions } from "./useGitActions";
export type {
  UseGitActionsOptions,
  UseGitActionsReturn,
  GitActionState,
} from "./useGitActions";

export { useFileTree } from "./useFileTree";
export type { UseFileTreeReturn } from "./useFileTree";

export { useFileContent } from "./useFileContent";
export type { UseFileContentReturn } from "./useFileContent";

export {
  useTerminalFont,
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
  FONT_FAMILY_OPTIONS,
  FONT_SIZE_OPTIONS,
  CUSTOM_FONT_SENTINEL,
} from "./useTerminalFont";
export type { UseTerminalFontReturn } from "./useTerminalFont";

export { useFocusReturn } from "./useFocusReturn";
export type { UseFocusReturnOptions } from "./useFocusReturn";

export { useFocusTrap } from "./useFocusTrap";
export type { UseFocusTrapOptions } from "./useFocusTrap";
