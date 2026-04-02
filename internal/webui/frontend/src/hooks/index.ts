/**
 * Hook barrel exports for the beads-web-ui frontend.
 */

export { useDebounce } from "./useDebounce";

export { useDebouncedCallback } from "./useDebouncedCallback";

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

export { useRouteView } from "./useRouteView";
export type { UseRouteViewReturn } from "./useRouteView";

export { useIssueDetail } from "./useIssueDetail";
export type { UseIssueDetailReturn } from "./useIssueDetail";

export { useIssueSearch } from "./useIssueSearch";
export type { UseIssueSearchReturn } from "./useIssueSearch";

export { useIssueDiffStat } from "./useIssueDiffStat";
export type {
  UseIssueDiffStatOptions,
  UseIssueDiffStatReturn,
} from "./useIssueDiffStat";

export { useAgentDiffStat } from "./useAgentDiffStat";
export type {
  UseAgentDiffStatOptions,
  UseAgentDiffStatReturn,
} from "./useAgentDiffStat";

export { useWorkspaceTree } from "./useWorkspaceTree";
export type { EpicWithTasks, UseWorkspaceTreeReturn } from "./useWorkspaceTree";

export { useInlineCreate } from "./useInlineCreate";
export type {
  UseInlineCreateOptions,
  UseInlineCreateReturn,
} from "./useInlineCreate";

export {
  EventProvider,
  useEventContext,
  useEventSubscription,
  EventContext,
  NO_EVENT_CONTEXT,
} from "./useEventProvider";
export type {
  EventContextValue,
  EventProviderProps,
  SubscriptionOptions,
} from "./useEventProvider";

export {
  StoreProvider,
  useIssueStoreInstance,
  useAgentStoreInstance,
  StoreContext,
  NO_STORE_CONTEXT,
} from "./useStoreContext";
export type { StoreContextValue, StoreProviderProps } from "./useStoreContext";

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

export { useRecentOwners } from "./useRecentOwners";
export type { UseRecentOwnersReturn } from "./useRecentOwners";

export { useBackendConfig } from "./useBackendConfig";
export type { UseBackendConfigReturn } from "./useBackendConfig";

export { useDaemonHealth } from "./useDaemonHealth";
export type {
  DaemonConnectionMode,
  UseDaemonHealthReturn,
} from "./useDaemonHealth";

export { usePollingWithBackoff } from "./usePollingWithBackoff";
export type {
  UsePollingWithBackoffOptions,
  UsePollingWithBackoffResult,
} from "./usePollingWithBackoff";

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

export { useTaskSessions } from "./useTaskSessions";
export type { UseTaskSessionsResult } from "./useTaskSessions";

export { useSessionTranscript } from "./useSessionTranscript";
export type { UseSessionTranscriptResult } from "./useSessionTranscript";

export { useSessionDiff } from "./useSessionDiff";
export type { UseSessionDiffResult } from "./useSessionDiff";

export { useObservabilityMetrics } from "./useObservabilityMetrics";
export type {
  UseObservabilityMetricsOptions,
  UseObservabilityMetricsResult,
} from "./useObservabilityMetrics";

export { useElapsedTime } from "./useElapsedTime";

export { useJobPolling } from "./useJobPolling";
export type {
  UseJobPollingCallbacks,
  UseJobPollingReturn,
} from "./useJobPolling";

export { useWorkspaceRepos } from "./useWorkspaceRepos";
export type {
  UseWorkspaceReposReturn,
  WorkspaceConnectionState,
} from "./useWorkspaceRepos";

export { useRepoFilter, parseReposFromUrl } from "./useRepoFilter";
export type {
  UseRepoFilterOptions,
  UseRepoFilterReturn,
} from "./useRepoFilter";

export { useWorkspace } from "./useWorkspace";
export type { UseWorkspaceOptions, UseWorkspaceReturn } from "./useWorkspace";

export {
  WorkspaceProvider,
  useWorkspaceContext,
  WorkspaceContext,
  NO_WORKSPACE_CONTEXT,
} from "./useWorkspaceContext";
export type {
  WorkspaceContextValue,
  WorkspaceProviderProps,
} from "./useWorkspaceContext";

export { useTheme } from "./useTheme";
export type { Theme, UseThemeReturn } from "./useTheme";

export {
  useWorkspaceState,
  clearWorkspaceSnapshots,
} from "./useWorkspaceState";
export type {
  WorkspaceSnapshot,
  UseWorkspaceStateParams,
} from "./useWorkspaceState";

export {
  useRepoFilterParam,
  parseRepoFilterFromUrl,
} from "./useRepoFilterParam";
export type {
  UseRepoFilterParamOptions,
  UseRepoFilterParamReturn,
} from "./useRepoFilterParam";

export { useSearchScope } from "./useSearchScope";
export type { UseSearchScopeReturn } from "./useSearchScope";

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

export { useDiff } from "./useDiff";
export type { UseDiffOptions, UseDiffReturn, SummaryStats } from "./useDiff";

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

export { usePanelManager } from "./usePanelManager";
export type {
  PanelState,
  PanelType,
  UsePanelManagerReturn,
} from "./usePanelManager";

export {
  KeyboardShortcutProvider,
  useKeyboardShortcuts,
  useRegisterEscapeLayer,
  resetEscapeRegistry,
  EscapeRegistryContext,
  LAYER_CONFIRM_DIALOG,
  LAYER_TOAST,
  LAYER_CHEATSHEET,
  LAYER_WORKSPACE_SWITCHER,
  LAYER_MODAL,
  LAYER_TERMINAL_PANEL,
  LAYER_AGENT_PANEL,
  LAYER_ISSUE_PANEL,
  LAYER_TERMINAL_SEARCH,
} from "./useKeyboardShortcuts";
export type {
  KeyboardShortcutProviderProps,
  EscapeRegistryAPI,
} from "./useKeyboardShortcuts";
