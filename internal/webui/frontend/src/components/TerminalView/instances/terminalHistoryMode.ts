export type TerminalHistoryMode = "virtual" | "classic";

export const TERMINAL_HISTORY_MODE_KEY = "terminal.history.mode";

/**
 * Durable history defaults on, with a local setting that can immediately
 * restore the classic xterm-only path without reverting code.
 */
export function getTerminalHistoryMode(): TerminalHistoryMode {
  if (typeof window === "undefined") return "virtual";
  try {
    return window.localStorage.getItem(TERMINAL_HISTORY_MODE_KEY) === "classic"
      ? "classic"
      : "virtual";
  } catch {
    return "virtual";
  }
}
