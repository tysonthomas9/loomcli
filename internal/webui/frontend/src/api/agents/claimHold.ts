/**
 * Workspace claim-hold API client.
 *
 * A claim hold is a persistent, explicitly-owned refusal to START new work:
 * the daemon stops claiming tasks and spawning agents while every run already
 * in flight continues untouched. It exists so an operator (or the deploy
 * script) can quiesce a workspace before redeploying loom itself.
 *
 * These routes share the daemon control socket — and the same authz — with
 * agent stop/start. A hold is OWNED, so releasing someone else's hold is
 * refused with 409 unless `force` is set.
 */

import { del, get, post, wsUrl } from "@/api/common";

export interface ClaimHold {
  held: boolean;
  actor: string;
  reason: string;
  /** RFC3339 timestamp the hold was taken. */
  since: string;
  /** RFC3339 timestamp the hold self-releases; absent = indefinite. */
  expires_at?: string;
}

/** An agent whose run was already in flight when the hold went up. */
export interface ClaimHoldRunningAgent {
  agent: string;
  task_id?: string;
  pid: number;
  started_at?: string;
}

export interface ClaimHoldStatus {
  /** null when claims are free. */
  hold: ClaimHold | null;
  running: ClaimHoldRunningAgent[];
  /** How many agents are cycling but gated by the hold. */
  gated: number;
}

export interface SetClaimHoldBody {
  reason: string;
  ttl_seconds?: number;
  actor?: string;
  force?: boolean;
}

export interface ReleaseClaimHoldBody {
  actor?: string;
  force?: boolean;
}

/** Read the current hold (hold: null when claims are free). */
export async function fetchClaimHold(
  workspaceId: string,
): Promise<ClaimHoldStatus> {
  return await get<ClaimHoldStatus>(wsUrl(workspaceId, "/claims/hold"), {
    signal: AbortSignal.timeout(15000),
  });
}

/** Take (or refresh) the hold. 409 when another actor holds it without force. */
export async function setClaimHold(
  workspaceId: string,
  body: SetClaimHoldBody,
): Promise<ClaimHoldStatus> {
  return await post<ClaimHoldStatus>(wsUrl(workspaceId, "/claims/hold"), body);
}

/**
 * Release the hold. 409 when it belongs to another actor and force is unset.
 *
 * actor/force travel as query parameters, not a body: the shared fetch helper
 * cannot attach a body to a DELETE, and the route accepts either form.
 */
export async function releaseClaimHold(
  workspaceId: string,
  body: ReleaseClaimHoldBody = {},
): Promise<ClaimHoldStatus> {
  const params = new URLSearchParams();
  if (body.actor) params.set("actor", body.actor);
  if (body.force) params.set("force", "true");
  const query = params.toString();
  return await del<ClaimHoldStatus>(
    wsUrl(workspaceId, `/claims/hold${query ? `?${query}` : ""}`),
  );
}
