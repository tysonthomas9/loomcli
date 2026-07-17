export type TerminalRendererKind = "wterm" | "xterm";

/** Keep renderer routing narrow: only the canonical Claude backend gets xterm. */
export function terminalRendererForBackend(
  backendName: string | undefined,
): TerminalRendererKind {
  return backendName?.trim().toLowerCase() === "claude" ? "xterm" : "wterm";
}
