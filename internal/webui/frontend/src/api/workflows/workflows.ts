import { del, get, patch, post, wsUrl } from "@/api/common";

export const EPIC_RUNNER_WORKFLOW_NAME = "epic-runner";

export type WorkflowRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "needs_review"
  | "cancelled"
  | "suspended_awaiting_event";

export type WorkflowRunStepStatus =
  | "queued"
  | "running"
  | "waiting"
  | "completed"
  | "failed"
  | "skipped";

export interface WorkflowRunStep {
  id: string;
  step_kind: string;
  task_run_id?: string;
  task_id?: string;
  status: WorkflowRunStepStatus;
}

export interface WorkflowRun {
  workspace_key: string;
  run_id: string;
  driver_id: string;
  driver_version_id: string;
  entrypoint?: string;
  source_kind?: string;
  source_ref?: string;
  epic_id?: string;
  trigger_binding_id?: string;
  status: WorkflowRunStatus;
  node_id?: string;
  lease_id?: string;
  fencing_token?: number;
  idempotency_key?: string;
  payload?: unknown;
  output?: Record<string, string>;
  steps?: WorkflowRunStep[];
  summary?: string;
  error_class?: string;
  started_at?: string;
  last_heartbeat?: string;
  finished_at?: string | null;
  parent_run_id?: string;
  created_at: string;
  updated_at: string;
}

export async function startWorkflowRun(
  workspaceId: string,
  workflowName: string,
  payload: unknown,
): Promise<WorkflowRun> {
  const path = wsUrl(
    workspaceId,
    `/workflows/${encodeURIComponent(workflowName)}`,
  );
  return post<WorkflowRun>(path, payload);
}

export async function getWorkflowRun(
  workspaceId: string,
  runId: string,
): Promise<WorkflowRun> {
  return get<WorkflowRun>(
    wsUrl(workspaceId, `/runs/${encodeURIComponent(runId)}`),
  );
}

export function isTerminalWorkflowRunStatus(
  status: WorkflowRunStatus | undefined,
): boolean {
  return (
    status === "completed" ||
    status === "failed" ||
    status === "needs_review" ||
    status === "cancelled"
  );
}

/** GET /workflows/{name}/runs response. */
export interface WorkflowRunsResponse {
  driver_id: string;
  active_version_id: string;
  runs: WorkflowRun[];
}

/**
 * GET /trigger-bindings/{id}/runs response. Deliberately NOT driver-rooted
 * (no driver_id/active_version_id envelope): this is the AGENT's run history —
 * driver identity is per-run provenance and lives on the binding the caller
 * already holds. Future home: /agents/{id}/runs (agent identity record design).
 */
export interface TriggerBindingRunsResponse {
  binding_id: string;
  runs: WorkflowRun[];
}

/** List a workflow's runs, newest-first, optionally filtered by `status` and capped by `limit`. */
export async function listWorkflowRuns(
  workspaceId: string,
  workflowName: string,
  opts?: { status?: string; limit?: number },
): Promise<WorkflowRunsResponse> {
  const params = new URLSearchParams();
  if (opts?.status) params.set("status", opts.status);
  if (opts?.limit !== undefined) params.set("limit", String(opts.limit));
  const query = params.toString();
  return get<WorkflowRunsResponse>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(workflowName)}/runs${query ? `?${query}` : ""}`,
    ),
  );
}

// ============= Automations: workflow catalog + trigger bindings =============

export interface WorkflowSummary {
  name: string;
  builtin: boolean;
}

/** List the workflows that can be started or bound to a trigger. */
export async function listWorkflows(
  workspaceId: string,
): Promise<WorkflowSummary[]> {
  const res = await get<{ workflows: WorkflowSummary[] }>(
    wsUrl(workspaceId, `/workflows`),
  );
  return res.workflows ?? [];
}

export interface TriggerBinding {
  workspace_key: string;
  binding_id: string;
  name: string;
  source_kind: string;
  route_key: string;
  driver_id: string;
  driver_version_id: string;
  event_type_patterns?: string[];
  enabled: boolean;
  /** 5-field cron expression — present when source_kind is "cron". */
  schedule?: string;
  /** IANA timezone for the schedule (UTC when omitted). */
  schedule_timezone?: string;
  /**
   * Free-form source config the dispatch source merges into each fired run's
   * payload (the binding's run-input). For a prompt agent it holds the JSON
   * {"roleName":..,"backend":..}; parse it with promptAgentRoleName().
   */
  source_config_ref?: string;
  /**
   * The owning Agent identity record's id, when this binding is ATTACHED
   * config on an agent (the serve-start migration attaches every prompt-agent
   * binding). Attached bindings reject direct enable/disable with 409
   * ("managed by agent") — callers must drive the agent-scoped routes instead;
   * see setTriggerBindingEnabled's dispatch in useAutomations.
   */
  target_agent_service_id?: string;
  /** Computed next fire time (ISO 8601) for an enabled cron binding. */
  next_fire_at?: string;
  /**
   * Newest run's status (incl. queued/running), for display. Present only in
   * the list view, which computes run-failure health per binding.
   */
  last_run_status?: WorkflowRunStatus;
  /**
   * Count of failed runs from newest until the first non-failed terminal run
   * (Decision 7: 1 → amber dot, 2+ → red "failing"). List view only.
   */
  consecutive_failures?: number;
  created_at?: string;
  updated_at?: string;
}

export interface CreateTriggerBindingRequest {
  /** Workflow name (e.g. "github-review-agent"); alternative to driver_id. */
  workflow?: string;
  driver_id?: string;
  driver_version_id?: string;
  /**
   * Routing address of the binding. Required for event sources (e.g.
   * github.pull_request.opened); omit for cron — the backend derives it from
   * binding_id so scheduled bindings never collide on a shared route.
   */
  route_key?: string;
  source_kind?: string;
  name?: string;
  binding_id?: string;
  /** Webhook HMAC secret — required to enable a github binding. */
  secret?: string;
  entrypoint?: string;
  event_type_patterns?: string[];
  enabled?: boolean;
  /** 5-field cron expression — required when source_kind is "cron". */
  schedule?: string;
  /** IANA timezone for the schedule (defaults to UTC when omitted). */
  schedule_timezone?: string;
  /**
   * Per-binding run-input the dispatch source merges into each fired run's
   * payload (stored on the binding's source_config_ref). A prompt agent passes
   * {"roleName":..,"backend":..} so the fired run wears the role.
   */
  run_input?: Record<string, unknown>;
}

/**
 * Parse a binding's run-input object out of its source_config_ref. The dispatch
 * source (CronScheduler) merges this into each fired run's payload; this is the
 * single place the run-input JSON convention is decoded on the frontend. Returns
 * {} when the binding carries no run-input (not a prompt agent, or a real
 * webhook source-config ref).
 */
export function parseBindingRunInput(
  binding: TriggerBinding,
): Record<string, unknown> {
  const raw = (binding.source_config_ref ?? "").trim();
  if (!raw.startsWith("{")) return {};
  try {
    const cfg = JSON.parse(raw) as Record<string, unknown>;
    return cfg && typeof cfg === "object" ? cfg : {};
  } catch {
    return {};
  }
}

/**
 * A prompt agent's roleName (from its run-input), or "" when the binding is not
 * a prompt agent.
 */
export function promptAgentRoleName(binding: TriggerBinding): string {
  const role = parseBindingRunInput(binding).roleName;
  return typeof role === "string" ? role : "";
}

export async function listTriggerBindings(
  workspaceId: string,
): Promise<TriggerBinding[]> {
  const res = await get<{ bindings: TriggerBinding[] }>(
    wsUrl(workspaceId, `/trigger-bindings`),
  );
  return res.bindings ?? [];
}

export async function createTriggerBinding(
  workspaceId: string,
  req: CreateTriggerBindingRequest,
): Promise<TriggerBinding> {
  return post<TriggerBinding>(wsUrl(workspaceId, `/trigger-bindings`), req);
}

/**
 * Run a binding on demand ("Run now"). Config-by-reference: the server creates a
 * DriverRun for the binding's driver STAMPED with the binding, carrying NO
 * client-supplied run-input — the run resolves its own config (e.g. a prompt
 * agent's role) via the binding-config driver op. This is why Run-now no longer
 * merges the binding's run-input into the payload on the client.
 */
export async function runTriggerBinding(
  workspaceId: string,
  bindingId: string,
): Promise<WorkflowRun> {
  return post<WorkflowRun>(
    wsUrl(
      workspaceId,
      `/trigger-bindings/${encodeURIComponent(bindingId)}/run`,
    ),
    {},
  );
}

/** List runs attributed to one trigger binding, newest-first and capped by `limit`. */
export async function listTriggerBindingRuns(
  workspaceId: string,
  bindingId: string,
  opts?: { limit?: number },
): Promise<TriggerBindingRunsResponse> {
  const params = new URLSearchParams();
  if (opts?.limit !== undefined) params.set("limit", String(opts.limit));
  const query = params.toString();
  return get<TriggerBindingRunsResponse>(
    wsUrl(
      workspaceId,
      `/trigger-bindings/${encodeURIComponent(bindingId)}/runs${query ? `?${query}` : ""}`,
    ),
  );
}

export async function setTriggerBindingEnabled(
  workspaceId: string,
  bindingId: string,
  enabled: boolean,
): Promise<TriggerBinding> {
  return post<TriggerBinding>(
    wsUrl(
      workspaceId,
      `/trigger-bindings/${encodeURIComponent(bindingId)}/${
        enabled ? "enable" : "disable"
      }`,
    ),
    {},
  );
}

/** PATCH body for editing a trigger binding: rename + reschedule only. */
export interface UpdateTriggerBindingRequest {
  name?: string;
  /** 5-field cron expression — only valid on a cron binding. */
  schedule?: string;
  /** IANA timezone — only valid on a cron binding. */
  schedule_timezone?: string;
  /** Replace the per-binding workflow input object (including an empty object). */
  run_input?: Record<string, unknown>;
}

/**
 * Edit a binding's name and/or cron schedule. Schedule/timezone changes on a
 * non-cron binding, or a malformed cron, are rejected server-side (400 —
 * surfaced as an ApiError whose message the caller can show).
 */
export async function updateTriggerBinding(
  workspaceId: string,
  bindingId: string,
  req: UpdateTriggerBindingRequest,
): Promise<TriggerBinding> {
  return patch<TriggerBinding>(
    wsUrl(workspaceId, `/trigger-bindings/${encodeURIComponent(bindingId)}`),
    req,
  );
}

/** Result of deleting a binding (Decision 6 — grants revoked alongside). */
export interface DeleteTriggerBindingResult {
  binding_id: string;
  deleted: boolean;
  grants_revoked: number;
}

/**
 * Delete a binding and revoke its connector grants (Decision 6: no orphaned
 * credentials).
 *
 * NOTE (honesty): against a fleet-db-backed store the DELETE currently fails —
 * the fleet-db server does not yet register a DELETE handler on this route
 * (405). The client wiring + memstore + grant revocation are complete; the
 * error is surfaced (never faked) until the server adds the route.
 */
export async function deleteTriggerBinding(
  workspaceId: string,
  bindingId: string,
): Promise<DeleteTriggerBindingResult> {
  return del<DeleteTriggerBindingResult>(
    wsUrl(workspaceId, `/trigger-bindings/${encodeURIComponent(bindingId)}`),
  );
}

// ============= Workflow source + version lifecycle (Phase B) =============

/**
 * A builtin workflow's TypeScript source tree. Only builtins expose source
 * (custom driver versions persist a compiled bundle + digest, not source text),
 * so a non-builtin name is a real 404 — the UI shows "no editable source"
 * rather than a fake empty editor.
 */
export interface WorkflowSource {
  name: string;
  builtin: boolean;
  /** Relative path of the entrypoint file within `files`. */
  entrypoint: string;
  /** Map of relative file path → file contents. */
  files: Record<string, string>;
}

/** One built driver version. Matches Go `domain.DriverVersion`. */
export interface DriverVersion {
  workspace_key: string;
  version_id: string;
  driver_id: string;
  version: number;
  source_ref: string;
  source_digest: string;
  bundle_ref: string;
  bundle_digest: string;
  runtime?: string;
  manifest?: Record<string, string>;
  build_diagnostics?: string;
  validation_status: "pending" | "passed" | "failed";
  created_by?: string;
  created_at: string;
}

/** A workflow's driver row. Matches the subset of Go `domain.Driver` the UI reads. */
export interface WorkflowDriver {
  workspace_key: string;
  driver_id: string;
  name: string;
  active_version_id?: string;
  status: string;
  trust_level?: string;
  metadata?: Record<string, string>;
  created_at: string;
  updated_at: string;
}

/** GET /workflows/{name}/versions response. */
export interface WorkflowVersionsResponse {
  /** Canonical aggregate snapshot carrying revision and approval metadata. */
  driver?: WorkflowDriver;
  driver_id: string;
  active_version_id: string;
  revision?: number;
  versions: DriverVersion[];
}

/** POST /workflows/{name}/versions body. */
export interface CreateWorkflowVersionRequest {
  files: Record<string, string>;
  entrypoint: string;
  /** Legacy compatibility field; the Workflow Catalog build path sends false. */
  activate?: boolean;
}

/**
 * POST /workflows/{name}/versions result. `build_diagnostics` is the redacted
 * flue build output; `activated` says whether the driver now points at this
 * version. A build FAILURE never reaches here — it surfaces as an `ApiError`
 * (status 400) whose message carries the diagnostics.
 *
 * NOTE (honesty): versions built via this HTTP path are inactive and stamped
 * UNTRUSTED. They must be approved and then activated through the Workflow
 * Catalog lifecycle commands before the runtime will execute them.
 */
export interface CreateWorkflowVersionResult {
  driver: WorkflowDriver;
  version: DriverVersion;
  bundle?: unknown;
  created_driver: boolean;
  created_version: boolean;
  reused_version: boolean;
  activated: boolean;
  build_diagnostics: string;
}

/** POST approve/activate result. */
export interface WorkflowVersionActionResult {
  action: "approve" | "unapprove" | "activate";
  driver: WorkflowDriver;
  version: DriverVersion;
}

/**
 * Read a builtin workflow's TS source. Throws `ApiError` with status 404 when
 * the workflow has no editable source (non-builtin) — surface that honestly.
 */
export async function getWorkflowSource(
  workspaceId: string,
  name: string,
): Promise<WorkflowSource> {
  return get<WorkflowSource>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(name)}/source`),
  );
}

/** List the driver versions built for a workflow. 404 when no driver exists yet. */
export async function listWorkflowVersions(
  workspaceId: string,
  name: string,
): Promise<WorkflowVersionsResponse> {
  return get<WorkflowVersionsResponse>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(name)}/versions`),
  );
}

/**
 * Build + register a new workflow version from edited source. A build failure
 * throws `ApiError` (400) with the redacted diagnostics in its message; on
 * success the result carries `build_diagnostics` + `activated` (false for the
 * supported draft path). flue toolchain must be present on the serve host.
 */
export async function createWorkflowVersion(
  workspaceId: string,
  name: string,
  req: CreateWorkflowVersionRequest,
): Promise<CreateWorkflowVersionResult> {
  return post<CreateWorkflowVersionResult>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(name)}/versions`),
    req,
    { timeout: 300_000 },
  );
}

/**
 * Approve a version — flips an HTTP-built (untrusted) version to trusted so the
 * runtime will run it. Pure metadata update, no rebuild.
 */
export async function approveWorkflowVersion(
  workspaceId: string,
  name: string,
  versionId: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(name)}/versions/${encodeURIComponent(versionId)}/approve`,
    ),
    {},
  );
}

/** Remove a version's explicit approval without changing the active pointer. */
export async function unapproveWorkflowVersion(
  workspaceId: string,
  name: string,
  versionId: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(name)}/versions/${encodeURIComponent(versionId)}/unapprove`,
    ),
    {},
  );
}

/**
 * Activate a version — points the driver at an existing version without a
 * rebuild. Pure metadata update.
 */
export async function activateWorkflowVersion(
  workspaceId: string,
  name: string,
  versionId: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(name)}/versions/${encodeURIComponent(versionId)}/activate`,
    ),
    {},
  );
}
