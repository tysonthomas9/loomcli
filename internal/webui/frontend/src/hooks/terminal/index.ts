/**
 * Terminal/session hooks barrel.
 */

export { useAgentTerminalLogs } from "./useAgentTerminalLogs";
export type {
  AgentLogTransportMode,
  UseAgentTerminalLogsOptions,
  UseAgentTerminalLogsReturn,
} from "./useAgentTerminalLogs";

export { useDiff } from "./useDiff";
export type { UseDiffOptions, UseDiffReturn, SummaryStats } from "./useDiff";

export { useSessionDiff } from "./useSessionDiff";
export type { UseSessionDiffResult } from "./useSessionDiff";

export { useSessionRestore } from "./useSessionRestore";

export { useSessionTranscript } from "./useSessionTranscript";
export type { UseSessionTranscriptResult } from "./useSessionTranscript";

export { useTaskLogPolling } from "./useTaskLogPolling";
export type {
  UseTaskLogPollingOptions,
  UseTaskLogPollingReturn,
} from "./useTaskLogPolling";

export { useTaskSessions } from "./useTaskSessions";
export type { UseTaskSessionsResult } from "./useTaskSessions";

export {
  useTerminalFont,
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
  FONT_FAMILY_OPTIONS,
  FONT_SIZE_OPTIONS,
  CUSTOM_FONT_SENTINEL,
} from "./useTerminalFont";
export type { UseTerminalFontReturn } from "./useTerminalFont";

export { useTerminalMetadata } from "./useTerminalMetadata";
export type { UseTerminalMetadataReturn } from "./useTerminalMetadata";

export { useTerminalSessions } from "./useTerminalSessions";
export type { UseTerminalSessionsReturn } from "./useTerminalSessions";

export type { LogChunk, LogStreamState } from "./logTypes";
