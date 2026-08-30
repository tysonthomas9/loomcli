/**
 * Terminal/session hooks barrel.
 */

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
  LogChunk,
  LogStreamState,
} from "./useTaskLogPolling";

export { useTaskSessions } from "./useTaskSessions";
export type { UseTaskSessionsResult } from "./useTaskSessions";
export { useAgentSessions } from "./useAgentSessions";
export { useAgentSessionTranscript } from "./useAgentSessionTranscript";
export { useAgentSessionDiff } from "./useAgentSessionDiff";

export {
  useTerminalFont,
  applyTerminalFont,
  TERMINAL_FONT_FAMILY_VAR,
  TERMINAL_FONT_SIZE_VAR,
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
} from "./useTerminalFont";
export type { UseTerminalFontReturn } from "./useTerminalFont";

export { useTerminalMetadata } from "./useTerminalMetadata";
export type { UseTerminalMetadataReturn } from "./useTerminalMetadata";
