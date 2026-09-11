import type { Event } from "@/types";
import type { PreparedIssueRecovery } from "./issueRecovery";

/** Selected window already formatted by Fleet, still private preparation.
 * The cursor belongs to the whole manifest; event IDs remain timeline IDs. */
export interface PreparedIssueRecoveryTimeline {
  readonly workspace: string;
  readonly issueId: string;
  readonly present: boolean;
  readonly hasOlder: boolean;
  readonly through: string;
  readonly sourceIdentity: string;
  readonly events: readonly Event[];
}

/** Field mapping only, matching eventDataToTypesEvent and its JSON omitempty
 * behavior. Fleet owns summaries, categories and field-change formatting. */
export function prepareIssueRecoveryTimeline(
  prepared: PreparedIssueRecovery,
): PreparedIssueRecoveryTimeline | null {
  const history = prepared.history;
  if (!history) return null;
  const events = history.timeline.map((row): Event => {
    const event: Event = {
      id: row.id,
      issue_id: history.issue_id,
      event_type: row.action,
      actor: row.actor,
      category: row.category,
      created_at: row.timestamp,
      ...(row.summary ? { summary: row.summary } : {}),
      ...(row.changes.length
        ? {
            changes: row.changes.map((change) =>
              Object.freeze({
                field: change.field,
                ...(change.before ? { before: change.before } : {}),
                ...(change.after ? { after: change.after } : {}),
              }),
            ),
          }
        : {}),
      ...(Object.keys(row.metadata).length
        ? { metadata: Object.freeze({ ...row.metadata }) }
        : {}),
    };
    if (event.changes) Object.freeze(event.changes);
    return Object.freeze(event);
  });
  return Object.freeze({
    workspace: prepared.workspace,
    issueId: history.issue_id,
    present: history.present,
    hasOlder: history.has_older,
    through: prepared.through,
    sourceIdentity: prepared.offerSourceIdentity,
    events: Object.freeze(events),
  });
}
