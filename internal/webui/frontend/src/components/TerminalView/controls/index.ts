/**
 * TerminalView controls sub-barrel.
 */

export { CopyToast } from "./CopyToast";
export { HelpPopover } from "./HelpPopover";
export { NotesBar } from "./NotesBar";
export type { NotesBarProps } from "./NotesBar";
export { PasteConfirmDialog } from "./PasteConfirmDialog";
export { SearchBar } from "./SearchBar";
export { TerminalContextMenu } from "./TerminalContextMenu";
export type { TerminalContextMenuProps } from "./TerminalContextMenu";

export { SlashCommandInterceptor } from "./slashCommandInterceptor";
export {
  parseSlashCommand,
  formatSystemMessage,
  COMMAND_REGISTRY,
} from "./slashCommands";
export type { SlashCommand } from "./slashCommands";

export { useContextMenuActions } from "./useContextMenuActions";
export { useTerminalKeyboardShortcuts } from "./useTerminalKeyboardShortcuts";
export type { UseTerminalKeyboardShortcutsOptions } from "./useTerminalKeyboardShortcuts";
export { useTerminalSearch } from "./useTerminalSearch";
export type {
  UseTerminalSearchOptions,
  UseTerminalSearchReturn,
} from "./useTerminalSearch";
