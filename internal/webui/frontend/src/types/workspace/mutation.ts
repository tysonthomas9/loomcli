/**
 * Mutation types for real-time sync via SSE.
 * Provides strongly-typed representation of mutation events for the React layer.
 *
 * The canonical definitions of MutationType and MutationPayload live here
 * in src/types/ so that the api layer (src/api/sse.ts) and the rest of the
 * app can share them without crossing a types → api edge in the frontend
 * layer DAG.
 */

import type { ISODateString } from "@/types/common";

/**
 * Kinds of mutation events emitted by the backend over SSE.
 * Kept in sync with the Go mutation types in the server.
 */
export type MutationType =
  | "create"
  | "update"
  | "delete"
  | "comment"
  | "status"
  | "bonded"
  | "squashed"
  | "burned"
  | "refresh"
  | "terminal_metadata"
  | "terminal_session_change"
  | "issue_tabs"
  | "session_change";

/**
 * Generic entity kinds carried by the SSE mutation envelope. The string
 * fallback keeps the frontend tolerant of new fleet-db entity types.
 */
export type MutationEntityType =
  | "issue"
  | "dependency"
  | "dep"
  | "comment"
  | "label"
  | "agent"
  | "terminal"
  | "session"
  | "workspace"
  | string;

/**
 * Server → Client mutation payload shape.
 * Matches the JSON delivered by GET /api/sse mutation events.
 */
export interface MutationPayload {
  /** Durable source event ID, shared with issue history when available. */
  cursor?: string;
  type: MutationType;
  entity_type?: MutationEntityType;
  entity_id?: string;
  action?: string;
  issue_id?: string;
  title?: string;
  assignee?: string;
  owner?: string;
  actor?: string;
  timestamp: string;
  old_status?: string;
  new_status?: string;
  parent_id?: string;
  step_count?: number;
  priority?: number;
  source_repo?: string;
  workspace_id?: string;
  pty_alive?: boolean;
  exit_reason?: string;
  kind?: string;
  agent?: boolean;
}

/**
 * Mutation type constants.
 * Maps to Go mutation types in the backend.
 */
export const MutationCreate: MutationType = "create";
export const MutationUpdate: MutationType = "update";
export const MutationDelete: MutationType = "delete";
export const MutationComment: MutationType = "comment";
export const MutationStatus: MutationType = "status";
export const MutationBonded: MutationType = "bonded";
export const MutationSquashed: MutationType = "squashed";
export const MutationBurned: MutationType = "burned";
export const MutationRefresh: MutationType = "refresh";
export const MutationSessionChange: MutationType = "session_change";

/**
 * Application-level mutation event.
 * Wraps MutationPayload with client-side metadata.
 */
export interface MutationEvent {
  /** Core mutation data from SSE */
  mutation: MutationPayload;

  /** When the client received this event (ISO 8601) */
  received_at: ISODateString;

  /** Optional sequence number for ordering (future-proofing) */
  sequence?: number;
}

/**
 * Creates a MutationEvent from a MutationPayload.
 * Adds client-side metadata (received_at timestamp).
 */
export function createMutationEvent(payload: MutationPayload): MutationEvent {
  return {
    mutation: payload,
    received_at: new Date().toISOString(),
  };
}

/**
 * Type guard to check if a mutation event is a create mutation.
 */
export function isCreateMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationCreate;
}

/**
 * Type guard to check if a mutation event is an update mutation.
 */
export function isUpdateMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationUpdate;
}

/**
 * Type guard to check if a mutation event is a delete mutation.
 */
export function isDeleteMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationDelete;
}

/**
 * Type guard to check if a mutation event is a comment mutation.
 */
export function isCommentMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationComment;
}

/**
 * Type guard to check if a mutation event is a status mutation.
 * Status mutations have old_status and new_status fields.
 */
export function isStatusMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationStatus;
}

/**
 * Type guard to check if a mutation event is a bonded mutation.
 * Bonded mutations have parent_id and step_count fields.
 */
export function isBondedMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationBonded;
}

/**
 * Type guard to check if a mutation event is a squashed mutation.
 */
export function isSquashedMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationSquashed;
}

/**
 * Type guard to check if a mutation event is a burned mutation.
 */
export function isBurnedMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationBurned;
}

/**
 * Type guard to check if a mutation event is a refresh mutation.
 * Refresh mutations indicate external DB changes requiring a full re-fetch.
 */
export function isRefreshMutation(event: MutationEvent): boolean {
  return event.mutation.type === MutationRefresh;
}
