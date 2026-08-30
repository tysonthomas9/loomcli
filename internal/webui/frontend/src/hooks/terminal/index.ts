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

export { useLogStream, getLogStreamUrl } from "./useLogStream";
export type { UseLogStreamOptions, UseLogStreamResult } from "./useLogStream";

export { useTaskSessions } from "./useTaskSessions";
export type { UseTaskSessionsResult } from "./useTaskSessions";

export {
  useTerminalFont,
  applyTerminalFont,
  TERMINAL_FONT_CHANGE_EVENT,
  TERMINAL_FONT_FAMILY_VAR,
  TERMINAL_FONT_SIZE_VAR,
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
  FONT_FAMILY_OPTIONS,
  FONT_SIZE_OPTIONS,
  CUSTOM_FONT_SENTINEL,
} from "./useTerminalFont";
export type {
  UseTerminalFontReturn,
  TerminalFontChangeDetail,
} from "./useTerminalFont";

export { useTerminalMetadata } from "./useTerminalMetadata";
export type { UseTerminalMetadataReturn } from "./useTerminalMetadata";
