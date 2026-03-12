export {
  ApiError,
  get,
  post,
  patch,
  del,
  initAuth,
  getAuthToken,
  getAuthState,
  onAuthStateChange,
} from "./client";
export type { RequestOptions, AuthState } from "./client";

// SSE client for real-time updates (recommended)
export { BeadsSSEClient, getSSEUrl } from "./sse";
export type { SSEClientOptions } from "./sse";

// Re-export common types from SSE
export type { ConnectionState, MutationType, MutationPayload } from "./sse";

// Issue API functions
export {
  getIssue,
  getReadyIssues,
  getKanbanIssues,
  getStats,
  createIssue,
  updateIssue,
  closeIssue,
  fetchGraphIssues,
  addDependency,
  removeDependency,
  addComment,
} from "./issues";
export type {
  CreateIssueRequest,
  UpdateIssueRequest,
  GraphFilter,
  AddCommentRequest,
} from "./issues";

// Agent API functions (loom server)
export {
  fetchAgents,
  checkLoomHealth,
  fetchStatus,
  fetchTasks,
} from "./agents";
export type { FetchStatusResult } from "./agents";

// Backend config API functions
export { getBackendConfig, updateBackendConfig } from "./config";
export type {
  BackendConfigData,
  AgentBackendOverride,
  BackendConfigPatchRequest,
} from "./config";

// Usage API functions (loom server)
export { fetchUsage } from "./usage";

// Observability API functions (loom server)
export { fetchObservabilityMetrics } from "./observability";

// Editor API functions
export {
  fetchEditors,
  refreshEditors,
  getCachedEditors,
  openInEditor,
} from "./editors";

// Diff API functions (agent worktree diffs)
export { fetchDiffCommits, fetchDiffFiles, fetchDiffFile } from "./diff";
export type { DiffCommit, DiffFile, DiffFileContent } from "./diff";

// Log streaming API functions
export {
  getTaskLogPhases,
  getTaskLogContent,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
  getAgentLogArchive,
} from "./logs";

// Terminal API functions
export {
  listTerminalSessions,
  fetchTerminalToken,
  buildTerminalWsUrl,
} from "./terminal";
export type { TerminalSessionInfo } from "./terminal";
