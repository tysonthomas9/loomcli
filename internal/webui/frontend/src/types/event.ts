/**
 * Event types for audit trail.
 * Event interface aliased from generated OpenAPI schema: components.schemas.IssueEvent
 * EventType union and constants kept hand-written (runtime values).
 */

import type { components } from "./generated/openapi";

/**
 * Event type values for audit trail.
 * Maps to Go types.EventType.
 */
export type EventType =
  | "issue.created"
  | "issue.updated"
  | "issue.status_changed"
  | "issue.commented"
  | "issue.closed"
  | "issue.reopened"
  | "issue.dependency_added"
  | "issue.dependency_removed"
  | "issue.label_added"
  | "issue.label_removed"
  | "issue.compacted";

/**
 * Event type constants.
 */
export const EventCreated: EventType = "issue.created";
export const EventUpdated: EventType = "issue.updated";
export const EventStatusChanged: EventType = "issue.status_changed";
export const EventCommented: EventType = "issue.commented";
export const EventClosed: EventType = "issue.closed";
export const EventReopened: EventType = "issue.reopened";
export const EventDependencyAdded: EventType = "issue.dependency_added";
export const EventDependencyRemoved: EventType = "issue.dependency_removed";
export const EventLabelAdded: EventType = "issue.label_added";
export const EventLabelRemoved: EventType = "issue.label_removed";
export const EventCompacted: EventType = "issue.compacted";

/**
 * Audit trail event.
 * Aliased from generated OpenAPI schema (IssueEvent).
 */
export type Event = components["schemas"]["IssueEvent"];
