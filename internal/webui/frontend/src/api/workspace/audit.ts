import { get, unwrapResponse, wsUrl } from "@/api/common";
import type { AuditPage } from "@/types/activity";

export interface FetchAuditEventsOptions {
  since?: string;
  limit?: number;
  entity?: string;
  actor?: string;
}

interface AuditEnvelope {
  success: boolean;
  data?: AuditPage;
  error?: string;
}

/** Fetch a filtered page from the workspace audit stream. */
export async function fetchAuditEvents(
  workspaceId: string,
  options: FetchAuditEventsOptions = {},
): Promise<AuditPage> {
  const query = new URLSearchParams();
  if (options.since) query.set("since", options.since);
  query.set("limit", String(options.limit ?? 50));
  if (options.entity) query.set("entity", options.entity);
  if (options.actor) query.set("actor", options.actor);

  const envelope = await get<AuditEnvelope>(
    `${wsUrl(workspaceId, "/audit")}?${query.toString()}`,
  );
  const page = unwrapResponse<AuditPage>(envelope);
  // The wire omits `details` when empty; normalize so consumers can index it.
  return {
    ...page,
    events: (page.events ?? []).map((event) => ({
      ...event,
      details: event.details ?? {},
    })),
  };
}
