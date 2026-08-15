/**
 * TerminalView tabs sub-barrel.
 */

export { TerminalTabBar } from "./TerminalTabBar";
export type { TerminalTabBarProps, TerminalTab } from "./TerminalTabBar";

export { TabContextMenu } from "./TabContextMenu";
export { SortableTab } from "./SortableTab";

export {
  MAX_TABS,
  MIN_SPLIT_RATIO,
  MAX_SPLIT_RATIO,
  DEFAULT_SPLIT_RATIO,
  MIN_SPLIT_WIDTH_PX,
  BACKEND_BRAND_COLORS,
  getBackendFromSessionName,
  generateTabName,
  sanitizeSessionName,
  extractBaseName,
  getNextDuplicateName,
  isAgentTab,
  isAgentMetadata,
} from "./terminalTabUtils";
export type { TabState } from "./terminalTabUtils";

export { useTabActions } from "./useTabActions";
export { useTabInit, tabStateFromMetadata } from "./useTabInit";
export { useTabOrdering } from "./useTabOrdering";
export { useWorkspaceTabState } from "./useWorkspaceTabState";
export { useUnreadTracking } from "./useUnreadTracking";
