/**
 * The minimal, scoped capability loom injects into a runner. Everything else
 * (task design, status, artifacts) is pulled/pushed via the TaskRunClient — see
 * docs/product/loom-typescript-sdk-spec.md ("Bootstrap handshake").
 */
export interface TaskRunBootstrap {
  /** Reachable `loom serve` base URL, e.g. https://loom.example.com */
  serverUrl: string;
  /** Workspace identifier the task lives in. */
  workspace: string;
  /** The task/issue to implement. */
  taskId: string;
  /** The AgentSession/TaskRun this run reports against (optional until Phase C). */
  sessionId?: string;
  /** Lease fencing token; sent on mutations so the server can reject stale writers. */
  fencingToken?: string;
  /** Scoped, short-lived capability token (Phase C). Optional in local/dev. */
  token?: string;
  /** X-Actor value for fleetdb dev-mode auth compatibility (local/dev only). */
  actor?: string;
}

/** Env var names loom uses to inject the bootstrap (see spawn.go / the runner). */
export const BOOTSTRAP_ENV = {
  serverUrl: "LOOM_SERVER_URL",
  workspace: "LOOM_WORKSPACE",
  taskId: "LOOM_TASK_ID",
  taskIdFallback: "LOOM_ASSIGNED_TASK_ID",
  sessionId: "LOOM_SESSION_ID",
  fencingToken: "LOOM_FENCING_TOKEN",
  token: "LOOM_TASKRUN_TOKEN",
  actor: "LOOM_FLEET_DB_ACTOR",
} as const;

type EnvLike = Record<string, string | undefined>;

function readProcessEnv(): EnvLike {
  const proc = (globalThis as { process?: { env?: EnvLike } }).process;
  return proc?.env ?? {};
}

/**
 * Build a bootstrap from environment variables. Throws if the required vars
 * (server URL, workspace, task id) are missing.
 */
export function bootstrapFromEnv(env: EnvLike = readProcessEnv()): TaskRunBootstrap {
  const serverUrl = required(env, BOOTSTRAP_ENV.serverUrl);
  const workspace = required(env, BOOTSTRAP_ENV.workspace);
  const taskId = env[BOOTSTRAP_ENV.taskId] ?? env[BOOTSTRAP_ENV.taskIdFallback];
  if (!taskId) {
    throw new Error(
      `loom bootstrap: ${BOOTSTRAP_ENV.taskId} (or ${BOOTSTRAP_ENV.taskIdFallback}) is required`,
    );
  }
  return {
    serverUrl,
    workspace,
    taskId,
    sessionId: env[BOOTSTRAP_ENV.sessionId],
    fencingToken: env[BOOTSTRAP_ENV.fencingToken],
    token: env[BOOTSTRAP_ENV.token],
    actor: env[BOOTSTRAP_ENV.actor],
  };
}

function required(env: EnvLike, name: string): string {
  const v = env[name]?.trim();
  if (!v) throw new Error(`loom bootstrap: ${name} is required`);
  return v;
}
