/**
 * Main entry point for frontend types.
 * Re-exports all types for convenient imports.
 *
 * Usage:
 *   import { Issue, Status, IssueType } from '@/types';
 *   import type { ApiResponse, ApiErrorResponse } from '@/types';
 */

export * from "./agent";
export * from "./common";
export * from "./issue";
export * from "./workspace";

// Workspace API types (hand-written, live in src/api/workspace.ts — out of scope to move).
export type {
  WorkspaceData,
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceSummary,
} from "../api/workspace";
