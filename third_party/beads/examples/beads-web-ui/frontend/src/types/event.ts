/**
 * Event types for audit trail.
 */

import type { ISODateString } from './common';

/**
 * Event type values for audit trail.
 * Maps to Go types.EventType.
 */
export type EventType =
  | 'created'
  | 'updated'
  | 'status_changed'
  | 'commented'
  | 'closed'
  | 'reopened'
  | 'dependency_added'
  | 'dependency_removed'
  | 'label_added'
  | 'label_removed'
  | 'compacted';

/**
 * Event type constants.
 */
export const EventCreated: EventType = 'created';
export const EventUpdated: EventType = 'updated';
export const EventStatusChanged: EventType = 'status_changed';
export const EventCommented: EventType = 'commented';
export const EventClosed: EventType = 'closed';
export const EventReopened: EventType = 'reopened';
export const EventDependencyAdded: EventType = 'dependency_added';
export const EventDependencyRemoved: EventType = 'dependency_removed';
export const EventLabelAdded: EventType = 'label_added';
export const EventLabelRemoved: EventType = 'label_removed';
export const EventCompacted: EventType = 'compacted';

/**
 * Audit trail event.
 * Maps to Go types.Event.
 */
export interface Event {
  id: number;
  issue_id: string;
  event_type: EventType;
  actor: string;
  old_value?: string | null;
  new_value?: string | null;
  comment?: string | null;
  created_at: ISODateString;
}
