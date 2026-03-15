/**
 * Strip ANSI escape codes from a string.
 * Handles CSI sequences (colors, cursor), OSC sequences (title, hyperlinks),
 * character set designations, and simple escape sequences.
 */

/* eslint-disable no-control-regex -- ANSI escape codes are control characters by definition */
const ANSI_REGEX =
  /\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?(?:\x1b\\|\x07)|\x1b[()][A-Z0-9]|\x1b[=><%#]|\x1b./g;
/* eslint-enable no-control-regex */

export function stripAnsi(text: string): string {
  return text.replace(ANSI_REGEX, "");
}
