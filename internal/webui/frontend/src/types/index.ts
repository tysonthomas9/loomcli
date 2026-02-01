/**
 * Main entry point for frontend types.
 * Re-exports all types for convenient imports.
 *
 * Usage:
 *   import { Issue, Status, IssueType } from '@/types';
 *   import type { ApiResponse, ApiError } from '@/types';
 */

// Common types
export type { ISODateString, Priority, Duration } from './common';

// Status types
export type { Status, KnownStatus } from './status';
export {
  StatusOpen,
  StatusInProgress,
  StatusBlocked,
  StatusDeferred,
  StatusClosed,
  StatusReview,
  StatusTombstone,
  StatusPinned,
  StatusHooked,
  KNOWN_STATUSES,
  USER_SELECTABLE_STATUSES,
  isKnownStatus,
  isValidStatus,
} from './status';

// Issue type types
export type { IssueType, KnownIssueType } from './issueType';
export {
  TypeBug,
  TypeFeature,
  TypeTask,
  TypeEpic,
  TypeChore,
  KNOWN_ISSUE_TYPES,
  isKnownIssueType,
  isValidIssueType,
} from './issueType';

// Dependency types
export type {
  Dependency,
  DependencyType,
  KnownDependencyType,
  DependencyCounts,
} from './dependency';
export {
  DepBlocks,
  DepParentChild,
  DepConditionalBlocks,
  DepWaitsFor,
  DepRelated,
  DepDiscoveredFrom,
  DepRepliesTo,
  DepRelatesTo,
  DepDuplicates,
  DepSupersedes,
  DepAuthoredBy,
  DepAssignedTo,
  DepApprovedBy,
  DepAttests,
  DepTracks,
  DepUntil,
  DepCausedBy,
  DepValidates,
  DepDelegatedFrom,
} from './dependency';

// Comment types
export type { Comment } from './comment';

// Label types
export type { Label } from './label';

// Entity types (HOP)
export type { EntityRef, ValidationOutcome, Validation, BondRef } from './entity';
export {
  BondTypeSequential,
  BondTypeParallel,
  BondTypeConditional,
  BondTypeRoot,
} from './entity';

// Agent types
export type {
  AgentState,
  MolType,
  WorkType,
  LoomAgentStatus,
  ParsedLoomStatus,
  LoomAgentsResponse,
  LoomTaskInfo,
  LoomTaskSummary,
  LoomSyncInfo,
  LoomStats,
  LoomStatusResponse,
  LoomAgentTasks,
  LoomTasksResponse,
  LoomTaskLists,
  LoomConnectionState,
} from './agent';
export {
  StateIdle,
  StateSpawning,
  StateRunning,
  StateWorking,
  StateStuck,
  StateDone,
  StateStopped,
  StateDead,
  MolTypeSwarm,
  MolTypePatrol,
  MolTypeWork,
  WorkTypeMutex,
  WorkTypeOpenCompetition,
  parseLoomStatus,
} from './agent';

// Issue types
export type {
  Issue,
  IssueWithDependencyMetadata,
  IssueWithCounts,
  IssueDetails,
  BlockedIssue,
  TreeNode,
  MoleculeProgressStats,
  GraphDependency,
  GraphIssue,
} from './issue';

// Statistics types
export type { Statistics, EpicStatus } from './statistics';

// Filter types
export type { SortPolicy, IssueFilter, WorkFilter, StaleFilter } from './filter';
export {
  SortPolicyHybrid,
  SortPolicyPriority,
  SortPolicyOldest,
} from './filter';

// API types
export type {
  ApiResponse,
  ApiError,
  ApiResult,
  PaginatedResponse,
} from './api';
export { isApiSuccess, isApiError } from './api';

// Event types
export type { EventType, Event } from './event';
export {
  EventCreated,
  EventUpdated,
  EventStatusChanged,
  EventCommented,
  EventClosed,
  EventReopened,
  EventDependencyAdded,
  EventDependencyRemoved,
  EventLabelAdded,
  EventLabelRemoved,
  EventCompacted,
} from './event';

// Mutation types (for real-time sync)
export type { MutationType, MutationPayload, MutationEvent } from './mutation';
export {
  MutationCreate,
  MutationUpdate,
  MutationDelete,
  MutationComment,
  MutationStatus,
  MutationBonded,
  MutationSquashed,
  MutationBurned,
  createMutationEvent,
  isCreateMutation,
  isUpdateMutation,
  isDeleteMutation,
  isCommentMutation,
  isStatusMutation,
  isBondedMutation,
  isSquashedMutation,
  isBurnedMutation,
} from './mutation';

// Graph types (React Flow)
export type {
  IssueNodeData,
  IssueNode,
  DependencyEdgeData,
  DependencyEdge,
  GraphNodeType,
  GraphEdgeType,
} from './graph';
