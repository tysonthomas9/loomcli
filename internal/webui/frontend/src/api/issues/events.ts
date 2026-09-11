/**
 * API functions for issue events (audit trail).
 * Uses openapi-fetch generated client.
 */

import type { Event } from "@/types";

import { api, apiErrorFromResponse } from "@/api/common";

/**
 * Fetch events for an issue.
 * Returns audit trail events (status changes, label changes, dependency changes, etc.)
 */
export async function getIssueEvents(
  workspaceId: string,
  issueId: string,
  limit = 100,
  options: { signal?: AbortSignal } = {},
): Promise<Event[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{id}/events",
    {
      ...(options.signal ? { signal: options.signal } : {}),
      params: {
        path: { ws: workspaceId, id: issueId },
        query: { limit },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  if (!data || data.success !== true) {
    throw new Error("Failed to fetch events");
  }
  if (
    !Array.isArray(data.data) ||
    !data.data.every((event) => validEvent(event, issueId))
  ) {
    throw new Error("Invalid issue events response");
  }
  return data.data;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Validate the fields used to identify, sort and describe history entries. */
function validEvent(value: unknown, issueId: string): value is Event {
  if (!record(value)) return false;
  return (
    validEventIdentity(value, issueId) &&
    stringFields(value, ["summary", "target", "payload", "category"]) &&
    nullableStringFields(value, ["old_value", "new_value", "comment"]) &&
    validChanges(value.changes) &&
    validMetadata(value.metadata)
  );
}

function validEventIdentity(
  value: Record<string, unknown>,
  issueId: string,
): boolean {
  return (
    typeof value.id === "string" &&
    value.id.length > 0 &&
    value.issue_id === issueId &&
    typeof value.event_type === "string" &&
    value.event_type.length > 0 &&
    typeof value.actor === "string" &&
    typeof value.created_at === "string" &&
    Number.isFinite(Date.parse(value.created_at))
  );
}

function stringFields(
  value: Record<string, unknown>,
  fields: readonly string[],
): boolean {
  return fields.every(
    (field) => value[field] === undefined || typeof value[field] === "string",
  );
}

function nullableStringFields(
  value: Record<string, unknown>,
  fields: readonly string[],
): boolean {
  return fields.every(
    (field) => value[field] == null || typeof value[field] === "string",
  );
}

function validChanges(value: unknown): boolean {
  return (
    value === undefined ||
    (Array.isArray(value) &&
      value.every(
        (change: unknown) =>
          record(change) &&
          typeof change.field === "string" &&
          (change.before === undefined || typeof change.before === "string") &&
          (change.after === undefined || typeof change.after === "string"),
      ))
  );
}

function validMetadata(value: unknown): boolean {
  return (
    value === undefined ||
    (record(value) &&
      Object.values(value).every((item) => typeof item === "string"))
  );
}
