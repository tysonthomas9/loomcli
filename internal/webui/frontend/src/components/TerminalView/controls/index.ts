/**
 * TerminalView controls sub-barrel.
 *
 * Slash-command handling, search, copy-toast, paste-confirm, and
 * context-menu actions were all dropped with the wterm migration —
 * wterm handles selection/clipboard natively and users run slash
 * commands directly against the shell.
 */

export { HelpPopover } from "./HelpPopover";
export { NotesBar } from "./NotesBar";
export type { NotesBarProps } from "./NotesBar";
export { TerminalContextMenu } from "./TerminalContextMenu";
export type { TerminalContextMenuProps } from "./TerminalContextMenu";

export { useTerminalKeyboardShortcuts } from "./useTerminalKeyboardShortcuts";
export type { UseTerminalKeyboardShortcutsOptions } from "./useTerminalKeyboardShortcuts";
