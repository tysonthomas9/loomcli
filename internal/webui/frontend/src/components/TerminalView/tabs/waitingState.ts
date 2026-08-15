/**
 * "Waiting for input" predicate for terminal tabs.
 *
 * A tab parked on an interactive prompt (a Claude modal, `read -p`, a y/n
 * confirmation, a password prompt) looks exactly like a dead one: the status
 * dot is green, the PTY is alive, and ordinary keystrokes are swallowed by the
 * prompt. This module decides when to badge such a tab as "quiet, and it is
 * your turn".
 *
 * It is deliberately pure and emulator-driven: the cursor facts come from the
 * xterm instance the tab already owns, so no ANSI tail parsing or semantic
 * prompt detection is involved. We never try to decide *what* the prompt is —
 * only that output stopped and the cursor sits somewhere that implies input is
 * expected.
 *
 * Known and accepted: an idle shell sitting at `$ ` is badged. That is not a
 * bug to fix — an idle shell genuinely is waiting for input.
 */

/** Quiet period after the last output before a connected tab counts as waiting. */
export const WAITING_QUIET_MS = 5_000;

export interface CursorProbe {
  /** Cursor sits in column 0 — the shell most likely printed a full line and moved on. */
  cursorAtLineStart: boolean;
  /** Alternate screen buffer is active — a full-screen TUI owns the grid. */
  altScreen: boolean;
}

export interface WaitingInputs {
  connected: boolean;
  hasEverOutput: boolean;
  /** Epoch ms of the last PTY output; 0 = never. */
  lastOutputAt: number;
  /** Epoch ms of the last user input actually delivered to the PTY; 0 = never. */
  lastInputAt: number;
  /** Cursor facts from the emulator; null when the renderer is not mounted yet. */
  probe: CursorProbe | null;
  now: number;
}

/**
 * True when a tab looks like it is parked on a prompt waiting for the user.
 *
 * The cursor test is what separates *waiting* from *busy*: after `sleep 60` or
 * a build the shell has echoed a newline, so the cursor rests in column 0 and
 * the tab is not badged. A prompt leaves the cursor mid-line, and a full-screen
 * TUI that hides its cursor at 0,0 is caught by the alternate-buffer check.
 */
export function isWaitingForInput(
  inputs: WaitingInputs,
  quietMs: number = WAITING_QUIET_MS,
): boolean {
  const { connected, hasEverOutput, lastOutputAt, lastInputAt, probe, now } =
    inputs;

  // A disconnected or ended tab has its own signalling; never badge it.
  if (!connected) return false;
  // A tab that has produced nothing has not proven it is alive at all.
  if (!hasEverOutput) return false;
  // The shell must have spoken after the user last typed.
  if (lastOutputAt <= lastInputAt) return false;
  // The output burst must have stopped.
  if (now - lastOutputAt < quietMs) return false;
  // Never guess without the emulator.
  if (probe === null) return false;

  return !probe.cursorAtLineStart || probe.altScreen;
}
