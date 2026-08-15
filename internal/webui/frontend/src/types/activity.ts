/** A workspace audit event returned by the locked audit API contract. */
export interface AuditEvent {
  cursor: string;
  timestamp: string;
  actor: string;
  action: string;
  entity_type: string;
  entity_id: string;
  details: Record<string, unknown>;
}

/** One page from GET /api/workspaces/{ws}/audit. */
export interface AuditPage {
  events: AuditEvent[];
  next_cursor: string;
}

export interface ActivityFilters {
  actor?: string;
  entity?: string;
}
