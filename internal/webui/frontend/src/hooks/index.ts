/**
 * Hook barrel exports for the beads-web-ui frontend.
 */

export { useSSE } from './useSSE';
export type { UseSSEOptions, UseSSEReturn } from './useSSE';

export { useDebounce } from './useDebounce';

export { useSort } from './useSort';
export type { UseSortOptions, UseSortReturn, SortState, SortDirection } from './useSort';

export { useIssueFilter } from './useIssueFilter';
export type { UseIssueFilterOptions, UseIssueFilterReturn } from './useIssueFilter';

export { useBlockedIssues } from './useBlockedIssues';
export type { UseBlockedIssuesOptions, UseBlockedIssuesResult } from './useBlockedIssues';

export { useMutationHandler } from './useMutationHandler';
export type { UseMutationHandlerOptions, UseMutationHandlerReturn } from './useMutationHandler';

export {
  useFilterState,
  toQueryString,
  parseFromUrl,
  isEmptyFilter,
  DEFAULT_GROUP_BY,
} from './useFilterState';
export type {
  FilterState,
  FilterActions,
  UseFilterStateOptions,
  UseFilterStateReturn,
} from './useFilterState';

export { useSelection } from './useSelection';
export type { UseSelectionOptions, UseSelectionReturn } from './useSelection';

export { useFilteredSelection } from './useFilteredSelection';
export type {
  UseFilteredSelectionOptions,
  UseFilteredSelectionReturn,
} from './useFilteredSelection';

export { useBulkClose } from './useBulkClose';
export type { UseBulkCloseOptions, UseBulkCloseReturn } from './useBulkClose';

export { useIssues } from './useIssues';
export type { UseIssuesOptions, UseIssuesReturn } from './useIssues';

export { useOptimisticStatusUpdate } from './useOptimisticStatusUpdate';
export type {
  UseOptimisticStatusUpdateOptions,
  UseOptimisticStatusUpdateReturn,
} from './useOptimisticStatusUpdate';

export { useFallbackPolling } from './useFallbackPolling';
export type { UseFallbackPollingOptions, UseFallbackPollingReturn } from './useFallbackPolling';

export { useBulkPriority, PRIORITY_OPTIONS } from './useBulkPriority';
export type {
  UseBulkPriorityOptions,
  UseBulkPriorityReturn,
  PriorityOption,
} from './useBulkPriority';

export { useGraphData } from './useGraphData';
export type { UseGraphDataOptions, UseGraphDataReturn } from './useGraphData';

export { useBlockedChain, getBlockedChain, computeAllBlockedCounts } from './useBlockedChain';
export type {
  UseBlockedChainOptions,
  UseBlockedChainReturn,
  BlockedChainResult,
} from './useBlockedChain';

export { useAutoLayout } from './useAutoLayout';
export type {
  UseAutoLayoutOptions,
  UseAutoLayoutReturn,
  LayoutDirection,
  RankAlignment,
} from './useAutoLayout';

export { useViewState } from './useViewState';
export type { UseViewStateOptions, UseViewStateReturn } from './useViewState';

export { useIssueDetail } from './useIssueDetail';
export type { UseIssueDetailReturn } from './useIssueDetail';

export { useAgents } from './useAgents';
export type { UseAgentsOptions, UseAgentsResult } from './useAgents';

export { AgentProvider, useAgentContext } from './useAgentContext';
export type { AgentContextValue, AgentProviderProps } from './useAgentContext';

export { useToast, ToastProvider } from './useToast';
export type {
  ToastType,
  ToastOptions,
  Toast,
  ToastContextValue,
  ToastProviderProps,
} from './useToast';

export { useStats } from './useStats';
export type { UseStatsOptions, UseStatsResult } from './useStats';

export { useRecentAssignees } from './useRecentAssignees';
export type { UseRecentAssigneesReturn } from './useRecentAssignees';

export { useLogStream } from './useLogStream';
export type {
  UseLogStreamOptions,
  UseLogStreamReturn,
  LogLine,
  LogStreamState,
} from './useLogStream';
