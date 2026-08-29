import type { Event } from "@/types";
import { formatStatusLabel } from "@/utils/issue";

import { detectAgent } from "./AuthorAvatar";

export type JourneyActorKind = "agent" | "operator" | "system";

export function actorKindFor(
  actor: string | null | undefined,
): JourneyActorKind {
  const normalized = actor?.trim().toLowerCase() ?? "";
  if (normalized === "system") return "system";
  if (detectAgent(normalized)) return "agent";
  return "operator";
}

function displayChanges(event: Event) {
  return (
    event.changes?.filter(
      ({ field }) => field.trim().toLowerCase() !== "updated_at",
    ) ?? []
  );
}

function changedFieldList(event: Event): string {
  const fields = [
    ...new Set(displayChanges(event).map(({ field }) => field.trim())),
  ].filter(Boolean);
  if (fields.length < 2) return fields[0] ?? "";
  if (fields.length === 2) return `${fields[0]} and ${fields[1]}`;
  return `${fields.slice(0, -1).join(", ")}, and ${fields[fields.length - 1]}`;
}

function displaySummary(summary: string | undefined): string | undefined {
  const trimmed = summary?.trim();
  if (!trimmed) return undefined;

  return (
    trimmed
      .replace(/\s+and\s+updated_at\b/gi, "")
      .replace(/,\s*updated_at\b/gi, "")
      .replace(/\bupdated_at\s+and\s+/gi, "")
      .replace(/\bupdated_at,\s*/gi, "")
      .replace(/\bupdated_at\b/gi, "")
      .replace(/\s{2,}/g, " ")
      .replace(/,\s*$/, "")
      .trim() || undefined
  );
}

/** Human-readable description for issue audit events. */
export function describeEvent(event: Event): string {
  const { event_type, actor, old_value, new_value } = event;
  const who = actor || "Someone";

  if (event_type === "issue.update" || event_type === "issue.updated") {
    const statusChange = displayChanges(event).find(
      ({ field }) => field.trim().toLowerCase() === "status",
    );
    const before = statusChange?.before?.trim();
    const after = statusChange?.after?.trim();
    if (after) {
      const destination = formatStatusLabel(after);
      return before
        ? `${who} changed status from ${formatStatusLabel(before)} to ${destination}`
        : `${who} changed status to ${destination}`;
    }

    const changedFields = changedFieldList(event);
    if (changedFields) return `Updated ${changedFields}`;
  }

  if (
    event_type === "issue.assign" &&
    event.metadata &&
    "assignee" in event.metadata &&
    event.metadata.assignee.trim() === ""
  ) {
    return "Unassigned issue";
  }

  if (event_type === "issue.release") {
    if (actor?.trim().toLowerCase() === "system") {
      return "System released the claim: no active lock or live agent session was vouching for it, so the task returned to the pool";
    }
    return `${who} released the claim`;
  }

  const summary = displaySummary(event.summary);
  if (summary) return summary;

  switch (event_type) {
    case "issue.created":
      return `${who} created this issue`;
    case "issue.status_changed":
      if (old_value && new_value) {
        return `${who} changed status from ${formatStatusLabel(old_value)} to ${formatStatusLabel(new_value)}`;
      }
      return `${who} changed the status`;
    case "issue.closed":
      return `${who} closed this issue`;
    case "issue.reopened":
      return `${who} reopened this issue`;
    case "issue.update":
    case "issue.updated":
      if (old_value && new_value) {
        return `${who} updated ${old_value} to ${new_value}`;
      }
      return `${who} updated this issue`;
    case "issue.dependency_added":
      return `${who} added dependency ${new_value || ""}`.trim();
    case "issue.dependency_removed":
      return `${who} removed dependency ${old_value || ""}`.trim();
    case "issue.label_added":
      return `${who} added label ${new_value || ""}`.trim();
    case "issue.label_removed":
      return `${who} removed label ${old_value || ""}`.trim();
    case "issue.commented":
      return `${who} commented`;
    case "issue.compacted":
      return "Earlier activity was summarized";
    default:
      return `${who} performed an action`;
  }
}
