/**
 * TerminalView controls sub-barrel.
 *
 * Slash-command handling, search, copy-toast, paste-confirm, and
 * context-menu actions were dropped during terminal simplification —
 * xterm handles selection/clipboard and users run slash
 * commands directly against the shell.
 */

export { HelpPopover } from "./HelpPopover";
export { NotesBar } from "./NotesBar";
export type { NotesBarProps } from "./NotesBar";
