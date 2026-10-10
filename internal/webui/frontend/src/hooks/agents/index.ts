/**
 * Agent observability hooks barrel.
 */

export { useAgentDiffStat } from "./useAgentDiffStat";
export type {
  UseAgentDiffStatOptions,
  UseAgentDiffStatReturn,
} from "./useAgentDiffStat";

export {
  useCreateLead,
  useCreateWorkspaceAgent,
  useLeadHarnesses,
} from "./useCreateWorkspaceAgent";

export { useInteractivePrompts } from "./useInteractivePrompts";
export type { UseInteractivePromptsReturn } from "./useInteractivePrompts";

export { useIssueSessionMap } from "./useIssueSessionMap";
export type { UseIssueSessionMapReturn } from "./useIssueSessionMap";

export { useJobPolling } from "./useJobPolling";
export type {
  UseJobPollingCallbacks,
  UseJobPollingReturn,
} from "./useJobPolling";

export { useObservabilityMetrics } from "./useObservabilityMetrics";
export type {
  UseObservabilityMetricsOptions,
  UseObservabilityMetricsResult,
} from "./useObservabilityMetrics";

export { useUsage } from "./useUsage";
export type { UseUsageOptions, UseUsageResult } from "./useUsage";

export { usePendingInput } from "./usePendingInput";
export type {
  PendingAnswerBody,
  PendingInput,
  UsePendingInputReturn,
} from "./usePendingInput";

export { useClaimHold } from "./useClaimHold";
export type {
  ClaimHold,
  ClaimHoldRunningAgent,
  UseClaimHoldReturn,
} from "./useClaimHold";

export { useAgentChat, useArchiveAgent, useDeleteAgent } from "./useAgentChat";
export type { DeleteRefusal, UseAgentChatReturn } from "./useAgentChat";
export { latestTurnError, ownSender, senderAgent } from "./agentChatModel";
export type {
  ChatItem,
  Delivery,
  StartedChild,
  TaskCompleted,
  ToolCall,
  ToolStatus,
} from "./agentChatModel";
export {
  useAgentRoster,
  useRoster,
  useRosterActivity,
  useRosterAgent,
} from "./useAgentRoster";
export {
  elapsed,
  startedAgo,
  trayCounts,
  trayLabel,
  trayRows,
  trayWaves,
} from "./agentTray";
export type { TrayCounts, TrayRow, TrayStatus, TrayWave } from "./agentTray";
export { applyActivity, childrenByParent } from "./agentRoster";
export type { Activities, Activity } from "./agentRoster";
export {
  AGENT_COLOR_COUNT,
  agentColor,
  agentColorIndex,
  agentInitials,
} from "./agentColor";
