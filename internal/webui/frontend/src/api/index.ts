export { ApiError, get, post, patch, del } from './client';
export type { RequestOptions } from './client';

// SSE client for real-time updates (recommended)
export { BeadsSSEClient, getSSEUrl } from './sse';
export type { SSEClientOptions } from './sse';

// Re-export common types from SSE
export type { ConnectionState, MutationType, MutationPayload } from './sse';

// Issue API functions
export {
  getIssue,
  getReadyIssues,
  getStats,
  createIssue,
  updateIssue,
  closeIssue,
  fetchGraphIssues,
  addDependency,
  removeDependency,
  addComment,
} from './issues';
export type {
  CreateIssueRequest,
  UpdateIssueRequest,
  GraphFilter,
  AddCommentRequest,
} from './issues';

// Agent API functions (loom server)
export { fetchAgents, checkLoomHealth, fetchStatus, fetchTasks } from './agents';
export type { FetchStatusResult } from './agents';

// Log streaming API functions
export { getTaskLogPhases, getAgentLogStreamUrl, getTaskLogStreamUrl } from './logs';
