/**
 * Mutation types for real-time sync via SSE.
 * Provides strongly-typed representation of mutation events for the React layer.
 */

import type { ISODateString } from './common';

// Re-export mutation types from SSE for convenient imports
export type { MutationType, MutationPayload } from '../api/sse';
import type { MutationType, MutationPayload } from '../api/sse';

/**
 * Mutation type constants.
 * Maps to Go mutation types in the backend.
 */
export const MutationCreate: MutationType = 'create';
export const MutationUpdate: MutationType = 'update';
export const MutationDelete: MutationType = 'delete';
export const MutationComment: MutationType = 'comment';
export const MutationStatus: MutationType = 'status';
export const MutationBonded: MutationType = 'bonded';
export const MutationSquashed: MutationType = 'squashed';
export const MutationBurned: MutationType = 'burned';

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
