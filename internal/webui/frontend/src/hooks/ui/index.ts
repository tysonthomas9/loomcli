/**
 * UI substrate hooks barrel.
 */

export { useAnnounce, onAnnounce } from "./useAnnounce";

export { useAutoLayout } from "./useAutoLayout";
export type {
  UseAutoLayoutOptions,
  UseAutoLayoutReturn,
  LayoutDirection,
  RankAlignment,
} from "./useAutoLayout";

export { useFocusReturn } from "./useFocusReturn";
export type { UseFocusReturnOptions } from "./useFocusReturn";

export { useFocusTrap } from "./useFocusTrap";
export type { UseFocusTrapOptions } from "./useFocusTrap";

export { useGraphData } from "./useGraphData";
export type { UseGraphDataOptions, UseGraphDataReturn } from "./useGraphData";

export {
  KeyboardShortcutProvider,
  useKeyboardShortcuts,
  useRegisterEscapeLayer,
  EscapeRegistryContext,
  LAYER_CONFIRM_DIALOG,
  LAYER_TOAST,
  LAYER_CHEATSHEET,
  LAYER_WORKSPACE_SWITCHER,
  LAYER_MODAL,
  LAYER_TERMINAL_PANEL,
  LAYER_AGENT_PANEL,
  LAYER_ISSUE_PANEL,
} from "./useKeyboardShortcuts";
export type {
  KeyboardShortcutProviderProps,
  EscapeRegistryAPI,
} from "./useKeyboardShortcuts";

export { usePanelManager } from "./usePanelManager";
export type {
  PanelState,
  PanelType,
  UsePanelManagerReturn,
} from "./usePanelManager";

export { useScrollRestore, clearScrollPositions } from "./useScrollRestore";
export type { UseScrollRestoreOptions } from "./useScrollRestore";

export { useSplitRatio } from "./useSplitRatio";
export type { UseSplitRatioReturn } from "./useSplitRatio";

export { useTheme } from "./useTheme";
export type { Theme, UseThemeReturn } from "./useTheme";

export { useToast, ToastProvider } from "./useToast";
export type {
  ToastType,
  ToastOptions,
  Toast,
  ToastContextValue,
  ToastProviderProps,
} from "./useToast";

export {
  useOpenQueuePanelWidth,
  OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
  OPEN_QUEUE_PANEL_MIN_WIDTH,
  OPEN_QUEUE_PANEL_MAX_WIDTH,
} from "./useOpenQueuePanelWidth";
export type { UseOpenQueuePanelWidthReturn } from "./useOpenQueuePanelWidth";

export {
  useWorkspaceTreeWidth,
  WORKSPACE_TREE_DEFAULT_WIDTH,
  WORKSPACE_TREE_MIN_WIDTH,
  WORKSPACE_TREE_MAX_WIDTH,
} from "./useWorkspaceTreeWidth";
export type { UseWorkspaceTreeWidthReturn } from "./useWorkspaceTreeWidth";

export { useVirtualList } from "./useVirtualList";
export type {
  UseVirtualListOptions,
  UseVirtualListReturn,
} from "./useVirtualList";
