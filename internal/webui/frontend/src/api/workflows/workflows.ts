import { get, post, wsUrl } from "@/api/common";

export const EPIC_RUNNER_WORKFLOW_NAME = "epic-runner";

export type WorkflowRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "needs_review"
  | "cancelled";

export interface WorkflowRun {
  workspace_key: string;
  run_id: string;
  driver_id: string;
  driver_version_id: string;
  entrypoint?: string;
  source_kind?: string;
  source_ref?: string;
  epic_id?: string;
  status: WorkflowRunStatus;
  node_id?: string;
  lease_id?: string;
  fencing_token?: number;
  idempotency_key?: string;
  payload?: unknown;
  output?: Record<string, string>;
  summary?: string;
  error_class?: string;
  started_at?: string;
  last_heartbeat?: string;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export async function startWorkflowRun(
  workspaceId: string,
  workflowName: string,
  payload: unknown,
): Promise<WorkflowRun> {
  return post<WorkflowRun>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(workflowName)}`),
    payload,
  );
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
  driver_id: string;
  active_version_id: string;
  versions: DriverVersion[];
}

/** POST /workflows/{name}/versions body. */
export interface CreateWorkflowVersionRequest {
  files: Record<string, string>;
  entrypoint: string;
  /** Point the driver at the new version once built (default true server-side). */
  activate?: boolean;
}

/**
 * POST /workflows/{name}/versions result. `build_diagnostics` is the redacted
 * flue build output; `activated` says whether the driver now points at this
 * version. A build FAILURE never reaches here — it surfaces as an `ApiError`
 * (status 400) whose message carries the diagnostics.
 *
 * NOTE (honesty): versions built via this HTTP path are stamped UNTRUSTED. Even
 * with `activated: true`, the version must be approved (see `approveWorkflowVersion`)
 * before the runtime will actually execute it.
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
  action: "approve" | "activate";
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
 * success the result carries `build_diagnostics` + `activated`. flue toolchain
 * must be present on the serve host.
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
