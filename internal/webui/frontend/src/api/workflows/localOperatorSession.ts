/**
 * Local Desktop Workflow Catalog authority.
 *
 * The durable operator credential never enters this module. Desktop puts a
 * 30-second, single-use launch code in the URL fragment; boot removes it before
 * any network request or render, exchanges it, and retains the resulting
 * action-limited bearer only in this module's memory.
 */

const LAUNCH_PARAM = "loom_launch";
const WORKSPACE_PARAM = "loom_workspace";
const TOKEN_PATTERN = /^[0-9a-f]{64}$/;

export interface LocalOperatorLaunch {
  launchCode: string;
  workspace: string;
}

interface LocalOperatorSessionResponse {
  access_token: string;
  token_type: string;
  workspace: string;
  expires_at: string;
}

let lifecycleBearer: string | null = null;
let lifecycleWorkspace: string | null = null;
let lifecycleExpiresAt = 0;

/**
 * Capture and erase a Desktop launch fragment synchronously. This must run
 * before error reporting, config discovery, storage migration, or React boot.
 */
export function captureLocalOperatorLaunchFromFragment(): LocalOperatorLaunch | null {
  if (typeof window === "undefined" || !window.location.hash) return null;

  const fragment = new URLSearchParams(window.location.hash.slice(1));
  const launchCode = fragment.get(LAUNCH_PARAM)?.trim() ?? "";
  const workspace = fragment.get(WORKSPACE_PARAM)?.trim() ?? "";
  const hadLaunchMaterial =
    fragment.has(LAUNCH_PARAM) || fragment.has(WORKSPACE_PARAM);
  if (!hadLaunchMaterial) return null;

  fragment.delete(LAUNCH_PARAM);
  fragment.delete(WORKSPACE_PARAM);
  const cleanHash = fragment.toString();
  const cleanURL = `${window.location.pathname}${window.location.search}${cleanHash ? `#${cleanHash}` : ""}`;
  window.history.replaceState(window.history.state, "", cleanURL);

  if (!TOKEN_PATTERN.test(launchCode) || workspace === "") {
    throw new Error(
      "Loom Desktop supplied an invalid workflow authorization launch.",
    );
  }
  return { launchCode, workspace };
}

export async function exchangeLocalOperatorLaunch(
  launch: LocalOperatorLaunch,
): Promise<void> {
  const response = await fetch(
    `/api/workspaces/${encodeURIComponent(launch.workspace)}/operator-sessions/exchange`,
    {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ launch_code: launch.launchCode }),
      cache: "no-store",
    },
  );
  if (!response.ok) {
    clearLocalWorkflowLifecycleSession();
    throw new Error(
      `Loom Desktop workflow authorization failed (${response.status}).`,
    );
  }
  const payload = (await response.json()) as LocalOperatorSessionResponse;
  const expiresAt = Date.parse(payload.expires_at);
  if (
    payload.token_type !== "Bearer" ||
    payload.workspace !== launch.workspace ||
    !TOKEN_PATTERN.test(payload.access_token ?? "") ||
    !Number.isFinite(expiresAt) ||
    expiresAt <= Date.now()
  ) {
    clearLocalWorkflowLifecycleSession();
    throw new Error("Loom Desktop returned an invalid workflow authorization.");
  }
  lifecycleBearer = payload.access_token;
  lifecycleWorkspace = payload.workspace;
  lifecycleExpiresAt = expiresAt;
}

/**
 * Return the runtime-local bearer only for the exact launch workspace while
 * unexpired. A workspace mismatch clears the session before the server-side
 * workspace/action checks provide their independent fail-closed boundary.
 */
export function getLocalWorkflowLifecycleBearer(
  workspace: string,
): string | null {
  if (
    workspace.trim() === "" ||
    lifecycleBearer === null ||
    lifecycleWorkspace === null ||
    workspace !== lifecycleWorkspace ||
    Date.now() >= lifecycleExpiresAt
  ) {
    clearLocalWorkflowLifecycleSession();
    return null;
  }
  return lifecycleBearer;
}

export function hasLocalWorkflowLifecycleSession(workspace: string): boolean {
  return getLocalWorkflowLifecycleBearer(workspace) !== null;
}

export interface LocalOperatorRequestOptions {
  headers?: Record<string, string>;
  timeout?: number;
}

/**
 * Add the process-local, workspace-bound operator bearer to an explicitly
 * operator-gated mutation. The bearer is never installed as global API auth:
 * reads and non-operator mutations therefore cannot receive it accidentally.
 */
export function localOperatorRequestOptions(
  workspace: string,
  base: LocalOperatorRequestOptions = {},
): LocalOperatorRequestOptions | undefined {
  const bearer = getLocalWorkflowLifecycleBearer(workspace);
  if (!bearer) {
    return Object.keys(base).length > 0 ? base : undefined;
  }
  return {
    ...base,
    headers: {
      ...base.headers,
      Authorization: `Bearer ${bearer}`,
    },
  };
}

export function clearLocalWorkflowLifecycleSession(): void {
  lifecycleBearer = null;
  lifecycleWorkspace = null;
  lifecycleExpiresAt = 0;
}
