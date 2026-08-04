/**
 * Barrel exports for Zustand stores.
 */

export { createIssueStore, issuesAreEqual } from "./issueStore";
export type {
  IssueStoreState,
  IssueStoreActions,
  IssueStore,
  FetchIssuesParams,
  IssueStoreConfig,
  SubscribeFn,
} from "./issueStore";

export {
  createAgentStore,
  INITIAL_STATE as AGENT_INITIAL_STATE,
} from "./agentStore";
export type {
  AgentStoreState,
  AgentStoreActions,
  AgentStore,
  AgentStoreConfig,
  PollingOptions,
} from "./agentStore";

export {
  editorStore,
  createEditorStore,
  INITIAL_EDITOR_STATE,
} from "./editorStore";
export type {
  EditorStore,
  EditorStoreState,
  EditorStoreActions,
} from "./editorStore";

export {
  backendsStore,
  createBackendsStore,
  INITIAL_BACKENDS_STATE,
} from "./backendsStore";
export type {
  BackendsStore,
  BackendsStoreState,
  BackendsStoreActions,
} from "./backendsStore";

export {
  createWorkspaceStore,
  INITIAL_WORKSPACE_STATE,
} from "./workspaceStore";
export type {
  WorkspaceStore,
  WorkspaceStoreState,
  WorkspaceStoreActions,
  WorkspacePollingOptions,
} from "./workspaceStore";

export {
  agentFileBrowserTabsStorageKey,
  createFileBrowserStore,
  fileBrowserTabsStorageKey,
  FileBrowserStoreProvider,
  useFileBrowserStore,
  useFileBrowserStoreInstance,
} from "./fileBrowserStore";

export { FileDocumentRegistry } from "./fileDocumentRegistry";
export type {
  ExternalFileConflict,
  FileDocumentOperations,
  FileDocumentRef,
  FileDocumentState,
} from "./fileDocumentRegistry";
export type {
  FileBrowserStore,
  FileBrowserStoreState,
  FileBrowserStoreActions,
  FileBrowserTab,
  FileBrowserGroup,
  FileBrowserStoreConfig,
} from "./fileBrowserStore";
