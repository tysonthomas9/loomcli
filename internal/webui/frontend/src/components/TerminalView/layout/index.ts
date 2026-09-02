/**
 * TerminalView layout sub-barrel.
 */

export { SplitDivider } from "./SplitDivider";
export { SplitPaneSelector } from "./SplitPaneSelector";
export { useSplitView } from "./useSplitView";
export {
  useTabEditorGroups,
  reconcileTabEditorGroups,
} from "./useTabEditorGroups";
export type { TabEditorGroup } from "./useTabEditorGroups";

export { BackendPickerPrompt } from "./BackendPickerPrompt";
export type { BackendPickerPromptProps } from "./BackendPickerPrompt";
export { NewTerminalTabMenu } from "./NewTerminalTabMenu";
export type { NewTerminalTabMenuProps } from "./NewTerminalTabMenu";
export { NoBackendsEmptyState } from "./NoBackendsEmptyState";
export { TabMetadataErrorState } from "./TabMetadataErrorState";

export { SessionNamePrompt } from "./SessionNamePrompt";
export type { SessionNamePromptProps } from "./SessionNamePrompt";
