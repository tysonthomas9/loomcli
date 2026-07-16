/**
 * Common utility hooks barrel.
 */

export { useDebounce } from "./useDebounce";

export { useDebouncedCallback } from "./useDebouncedCallback";

export { useEditors } from "./useEditors";
export type { UseEditorsResult } from "./useEditors";

export { useElapsedTime } from "./useElapsedTime";

export {
  EventProvider,
  useEventContext,
  useEventSubscription,
  EventContext,
  NO_EVENT_CONTEXT,
} from "./useEventProvider";
export type {
  ConnectionState,
  EventContextValue,
  EventProviderProps,
  SubscriptionOptions,
} from "./useEventProvider";

export {
  FileDocumentRegistryProvider,
  useFileDocument,
  useFileDocumentRegistry,
  useFileDocumentRegistryRevision,
} from "./useFileDocument";
export type { UseFileDocumentReturn } from "./useFileDocument";

export {
  FileCapabilitiesProvider,
  useFileCapabilities,
} from "./useFileCapabilities";
export type { FileCapabilitiesState } from "./useFileCapabilities";

export { useScopedFileTree } from "./useScopedFileTree";
export type { UseScopedFileTreeReturn } from "./useScopedFileTree";

export { useFolderPicker } from "./useFolderPicker";
export type {
  UseFolderPickerOptions,
  UseFolderPickerReturn,
} from "./useFolderPicker";

export { useFileEditorBuffer } from "./useFileEditorBuffer";
export type {
  UseFileEditorBufferOptions,
  UseFileEditorBufferReturn,
} from "./useFileEditorBuffer";

export { usePollingWithBackoff } from "./usePollingWithBackoff";
export type {
  UsePollingWithBackoffOptions,
  UsePollingWithBackoffResult,
} from "./usePollingWithBackoff";

export { useRouteView } from "./useRouteView";
export type { UseRouteViewReturn } from "./useRouteView";

export { useSort } from "./useSort";
export type {
  UseSortOptions,
  UseSortReturn,
  SortState,
  SortDirection,
} from "./useSort";

export {
  StoreProvider,
  useIssueStoreInstance,
  useAgentStoreInstance,
  StoreContext,
  NO_STORE_CONTEXT,
} from "./useStoreContext";
export type { StoreContextValue, StoreProviderProps } from "./useStoreContext";

export {
  agentFileBrowserTabsStorageKey,
  FileBrowserStoreProvider,
  fileBrowserTabsStorageKey,
  useFileBrowserStore,
  useFileBrowserStoreInstance,
} from "@/stores";
export type {
  FileBrowserGroup,
  FileBrowserStore,
  FileBrowserTab,
} from "@/stores";
