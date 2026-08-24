import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { getIssueEvents } from "@/api";
import { useEventSubscription } from "@/hooks/common";
import type { Event, Issue, LoomAgentStatus, MutationPayload } from "@/types";

const ACTIVITY_BUFFER_SIZE = 50;
/** How many of the most recently updated tasks seed the feed on load. */
export const SEED_ISSUE_COUNT = 5;

export type ActivityMarker = "op" | "ok" | "bad" | "rev" | "default";

export interface RecentActivityItem {
  id: string;
  timestamp: string;
  actor: string;
  issueId?: string;
  sourceRepo?: string;
  text: string;
  marker: ActivityMarker;
  /** Status the event moved the issue to, when it did; drives the marker. */
  status?: string;
  /** True when the actor is not a known agent — an operator or a harness. */
  isOperator?: boolean;
}

export interface ActivityDescription {
  text: string;
  marker: ActivityMarker;
  status?: string;
}

type MaybeSummarized = Event & {
  summary?: string;
  changes?: {
    field: string;
    before?: string | null;
    after?: string | null;
  }[];
  metadata?: Record<string, string | undefined>;
};

const ISSUE_STATUSES = new Set([
  "open",
  "in_progress",
  "review",
  "blocked",
  "deferred",
  "closed",
]);

function statusFrom(value: string | null | undefined): string | undefined {
  return value && ISSUE_STATUSES.has(value) ? value : undefined;
}

function cleanSummary(summary: string): string {
  return summary
    .replace(/(?:\s*(?:,|and)\s*)?updated_at\b/gi, "")
    .replace(/\s{2,}/g, " ")
    .replace(/,\s*$/g, "")
    .trim()
    .replace(/^[A-Z]/, (first) => first.toLowerCase());
}

function markerFor(
  status: string | null | undefined,
  actor: string,
  knownAgentNames: ReadonlySet<string>,
): ActivityMarker {
  if (status === "closed") return "ok";
  if (status === "blocked") return "bad";
  if (status === "review") return "rev";
  if (actor && !knownAgentNames.has(actor)) return "op";
  return "default";
}

/** Describe a workspace SSE mutation in the compact Home activity vocabulary. */
export function describeMutation(
  mutation: MutationPayload,
  knownAgentNames: ReadonlySet<string> = new Set(),
): ActivityDescription {
  const actor = mutation.actor?.trim() ?? "";
  const status = mutation.new_status;
  let text = "updated the workspace";

  if (status) {
    text = `set to ${status.replace(/_/g, " ")}`;
  } else if (mutation.type === "create") {
    text = "created";
  } else if (mutation.type === "comment") {
    text = "commented";
  } else if (mutation.action?.includes("label")) {
    text = "changed labels";
  } else if (mutation.action?.includes("issue")) {
    text = "updated";
  }

  return {
    text,
    marker: markerFor(status, actor, knownAgentNames),
    ...(status ? { status } : {}),
  };
}

/**
 * Use the source event cursor when present so an SSE delivery and its
 * subsequently seeded issue-history entry have one stable Activity identity.
 */
export function activityIdForMutation(mutation: MutationPayload): string {
  if (mutation.cursor) return `event-${mutation.cursor}`;
  return `mutation-${mutation.entity_id ?? mutation.issue_id ?? "workspace"}-${mutation.timestamp}-${mutation.action ?? mutation.type}`;
}

export function describeIssueEvent(
  event: Event,
  knownAgentNames: ReadonlySet<string>,
): RecentActivityItem {
  const actor = event.actor || "Someone";
  const summarizedEvent = event as MaybeSummarized;
  const statusChange = summarizedEvent.changes?.find(
    (change) => change.field === "status" && change.after,
  );
  const changedStatus = statusFrom(statusChange?.after);
  // Summaries restate the actor for most fleet actions ("Claimed by x"), so
  // they are only used for field changes, where they name the field that moved.
  const isFieldChange =
    event.event_type === "issue.update" || event.event_type === "issue.updated";
  const summary =
    isFieldChange && summarizedEvent.summary
      ? cleanSummary(summarizedEvent.summary)
      : "";
  let text: string;
  let status: string | undefined = changedStatus;

  if (changedStatus) {
    text = `set to ${changedStatus.replace(/_/g, " ")}`;
  } else if (summary) {
    text = summary;
  } else {
    switch (event.event_type) {
      case "issue.created":
      case "issue.create":
        text = "created";
        break;
      case "issue.status_changed":
        status = statusFrom(event.new_value);
        text = status
          ? `set to ${status.replace(/_/g, " ")}`
          : "changed status";
        break;
      case "issue.updated":
      case "issue.update":
        status = statusFrom(event.new_value);
        text = status ? `set to ${status.replace(/_/g, " ")}` : "updated";
        break;
      case "issue.closed":
      case "issue.close":
        status = "closed";
        text = "closed";
        break;
      case "issue.reopened":
      case "issue.reopen":
        text = "reopened";
        break;
      case "issue.claim":
        text = "claimed";
        break;
      case "issue.assign": {
        const assignee =
          event.new_value || summarizedEvent.metadata?.assignee || "";
        text = assignee ? `assigned to ${assignee}` : "unassigned";
        break;
      }
      case "issue.release":
        text = "released";
        break;
      case "issue.defer":
        text = "deferred";
        break;
      case "issue.undefer":
        text = "undeferred";
        break;
      case "issue.comment":
      case "issue.commented":
        text = "commented";
        break;
      case "issue.label":
        text = "changed labels";
        break;
      case "issue.dependency_added":
        text = `added dependency ${event.new_value ?? ""}`.trim();
        break;
      case "issue.dependency_removed":
        text = `removed dependency ${event.old_value ?? ""}`.trim();
        break;
      case "issue.label_added":
        text = `added label ${event.new_value ?? ""}`.trim();
        break;
      case "issue.label_removed":
        text = `removed label ${event.old_value ?? ""}`.trim();
        break;
      case "issue.compacted":
        text = "summarized earlier activity";
        break;
      default:
        text = "updated";
    }
  }

  return {
    id: `event-${event.id}`,
    timestamp: event.created_at,
    actor,
    issueId: event.issue_id,
    text,
    marker: markerFor(status, actor, knownAgentNames),
    ...(status ? { status } : {}),
  };
}

/** Merge, deduplicate, sort, and cap Home's in-memory activity buffer. */
export function mergeRecentActivity(
  existing: readonly RecentActivityItem[],
  incoming: readonly RecentActivityItem[],
): RecentActivityItem[] {
  const merged: RecentActivityItem[] = [];

  for (const item of [...existing, ...incoming]) {
    const isDuplicate = merged.some((known) => known.id === item.id);
    if (!isDuplicate) merged.push(item);
  }

  return merged
    .sort(
      (a, b) => Date.parse(b.timestamp || "") - Date.parse(a.timestamp || ""),
    )
    .slice(0, ACTIVITY_BUFFER_SIZE);
}

export function useRecentActivity(
  workspaceId: string,
  issues: readonly Issue[],
  agents: readonly LoomAgentStatus[],
): RecentActivityItem[] {
  const [activity, setActivity] = useState<RecentActivityItem[]>([]);
  const seededWorkspaceRef = useRef<string | null>(null);
  const knownAgentNames = useMemo(
    () => new Set(agents.map((agent) => agent.name)),
    [agents],
  );
  const knownAgentNamesRef = useRef(knownAgentNames);
  knownAgentNamesRef.current = knownAgentNames;
  const sourceRepoByIssueId = useMemo(
    () => new Map(issues.map((issue) => [issue.id, issue.source_repo])),
    [issues],
  );
  const seedIssueIds = useMemo(
    () =>
      issues
        .filter((issue) => issue.issue_type !== "epic")
        .slice()
        .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
        .slice(0, SEED_ISSUE_COUNT)
        .map((issue) => issue.id),
    [issues],
  );
  const seedKey = seedIssueIds.join(",");

  const appendActivity = useCallback((incoming: RecentActivityItem[]) => {
    if (incoming.length === 0) return;
    setActivity((current) => mergeRecentActivity(current, incoming));
  }, []);

  useEffect(() => {
    seededWorkspaceRef.current = null;
    setActivity([]);
  }, [workspaceId]);

  useEventSubscription(
    useCallback(
      (mutation: MutationPayload) => {
        if (mutation.workspace_id !== workspaceId) return;

        const description = describeMutation(mutation, knownAgentNames);
        appendActivity([
          {
            id: activityIdForMutation(mutation),
            timestamp: mutation.timestamp,
            actor: mutation.actor?.trim() || "Someone",
            ...(mutation.issue_id || mutation.entity_id
              ? { issueId: mutation.issue_id ?? mutation.entity_id }
              : {}),
            ...description,
          },
        ]);
      },
      [appendActivity, knownAgentNames, workspaceId],
    ),
  );

  useEffect(() => {
    if (!workspaceId || seedIssueIds.length === 0) return;
    if (seededWorkspaceRef.current === workspaceId) return;
    seededWorkspaceRef.current = workspaceId;

    // Deliberately not cancelled on re-render: the seed is a one-shot per
    // workspace, and agent or issue updates arriving mid-fetch must not
    // abort it (rows are real events and dedupe by id). Only a workspace
    // switch discards the result.
    const seed = async (): Promise<void> => {
      const seeded: RecentActivityItem[] = [];
      for (const issueId of seedIssueIds) {
        try {
          const events = await getIssueEvents(workspaceId, issueId, 5);
          seeded.push(
            ...events.map((event) =>
              describeIssueEvent(event, knownAgentNamesRef.current),
            ),
          );
        } catch {
          // Activity is ambient context; one unavailable issue trail should not
          // prevent the rest of the live Home surface from rendering.
        }
      }
      if (seededWorkspaceRef.current === workspaceId) appendActivity(seeded);
    };

    void seed();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- seedKey stands in for seedIssueIds
  }, [appendActivity, seedKey, workspaceId]);

  // Agents may load after the seed; re-derive operator-vs-agent markers so a
  // known agent is never shown as an operator just because it raced the fetch.
  return useMemo(
    () =>
      activity.map((item) => {
        const sourceRepo = item.issueId
          ? sourceRepoByIssueId.get(item.issueId)
          : undefined;

        return {
          ...item,
          ...(sourceRepo ? { sourceRepo } : {}),
          marker: markerFor(item.status, item.actor, knownAgentNames),
          isOperator: !knownAgentNames.has(item.actor),
        };
      }),
    [activity, knownAgentNames, sourceRepoByIssueId],
  );
}
