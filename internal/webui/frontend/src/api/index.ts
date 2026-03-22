export {
  ApiError,
  get,
  post,
  put,
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
  moveIssue,
  fetchGraphIssues,
  addDependency,
  removeDependency,
  addComment,
} from "./issues";
export type {
  CreateIssueRequest,
  UpdateIssueRequest,
  MoveIssueResult,
  GraphFilter,
  AddCommentRequest,
} from "./issues";

// Event API functions
export { getIssueEvents } from "./events";

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

// Workspace API functions
export {
  fetchWorkspace,
  refreshWorkspace,
  getCachedWorkspace,
} from "./workspace";
export type {
  WorkspaceData,
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceSummary,
} from "./workspace";

// Editor API functions
export {
  fetchEditors,
  refreshEditors,
  getCachedEditors,
  openInEditor,
} from "./editors";

// Diff API functions (agent worktree diffs)
export { fetchDiffCommits, fetchDiffFiles, fetchDiffFile } from "./diff";
export type { DiffCommit, DiffFile, DiffFilePatch } from "./diff";

// Issue diff stat API
export { fetchIssueDiffStat } from "./diff-stat";
export type { IssueDiffStat } from "./diff-stat";

// Git API functions
export {
  fetchGitStatus,
  gitPush,
  gitPull,
  gitSync,
  gitCreatePR,
  gitReset,
  gitUpdateTarget,
} from "./git";
export type {
  GitStatus,
  GitPushResult,
  GitPullResult,
  GitSyncResult,
  GitPRResult,
  GitResetResult,
  GitResetLockedResponse,
  GitTargetResult,
} from "./git";

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

// Backend health API functions
export { fetchBackends, refreshBackends } from "./backends";
export type { BackendHealthData } from "./backends";

// Session API functions (session audit trail)
export {
  getTaskSessions,
  getSession,
  getSessionTranscript,
  getSessionDiff,
} from "./sessions";

// File API functions (agent worktree file operations)
export { listWorktreeDir, readWorktreeFile, writeWorktreeFile } from "./files";
export type { FileEntry, DirListData, FileReadData } from "./files";
