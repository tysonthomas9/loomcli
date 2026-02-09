/**
 * ANSI escape sequence stripping utility.
 * Processes raw terminal output by converting cursor movements to spaces
 * and removing all remaining escape sequences.
 */

// CSI cursor-forward: \x1b[<n>C or \x1b[C (n defaults to 1)
const cursorForwardRe = /\x1b\[(\d*)C/g;

// CSI sequences (cursor movement, modes, colors, etc.)
const csiRe = /\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]/g;

// OSC sequences: \x1b] ... (terminated by BEL \x07 or ST \x1b\\)
const oscRe = /\x1b\].*?(\x07|\x1b\\)/g;

// Single-character escape sequences (e.g., \x1b= \x1b>)
const singleEscRe = /\x1b[\x20-\x7e]/g;

// Bare ESC characters
const bareEscRe = /\x1b/g;

/**
 * Strip ANSI escape sequences from a line of terminal output.
 * Cursor-forward sequences (\x1b[nC) are converted to spaces to preserve word spacing.
 */
export function stripAnsi(text: string): string {
  // 1. Convert cursor-forward to spaces (preserve word spacing)
  let result = text.replace(cursorForwardRe, (_match, count) => {
    const n = count ? parseInt(count, 10) : 1;
    return ' '.repeat(n);
  });

  // 2. Strip all remaining CSI sequences
  result = result.replace(csiRe, '');

  // 3. Strip OSC sequences
  result = result.replace(oscRe, '');

  // 4. Strip single-character escape sequences
  result = result.replace(singleEscRe, '');

  // 5. Strip bare ESC characters
  result = result.replace(bareEscRe, '');

  return result;
}

/**
 * Check if a line is empty or whitespace-only after stripping ANSI codes.
 */
export function isEmptyAfterStrip(text: string): boolean {
  return stripAnsi(text).trim() === '';
}
