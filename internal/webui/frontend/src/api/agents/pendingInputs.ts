/**
 * Pending interactive inputs API client.
 *
 * An agent whose role input_policy says "ask" parks on a harness prompt and
 * waits for a person. These endpoints surface that prompt and deliver the
 * person's decision; they share the daemon control socket — and the same
 * authz — with agent stop/start.
 */

import { get, post, wsUrl } from "@/api/common";

export interface PendingInputOption {
  id: string;
  label?: string;
}

export interface PendingInput {
  request_id: string;
  agent: string;
  kind: string;
  prompt: string;
  options?: PendingInputOption[];
  asked_at: string;
}

export interface AnswerInputBody {
  request_id?: string;
  option_id?: string;
  text?: string;
  decline?: boolean;
}

/** Fetch every pending prompt in the workspace (monitor badge). */
export async function fetchPendingInputs(
  workspaceId: string,
): Promise<PendingInput[]> {
  return await get<PendingInput[]>(wsUrl(workspaceId, "/pending-inputs"), {
    signal: AbortSignal.timeout(15000),
  });
}

/** Fetch the named agent's pending prompt ([] when it is not waiting). */
export async function fetchAgentPendingInput(
  workspaceId: string,
  agentName: string,
): Promise<PendingInput[]> {
  return await get<PendingInput[]>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentName)}/input`),
    { signal: AbortSignal.timeout(15000) },
  );
}

/** Deliver a decision for the agent's pending prompt. */
export async function answerAgentInput(
  workspaceId: string,
  agentName: string,
  body: AnswerInputBody,
): Promise<void> {
  await post<unknown>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentName)}/answer`),
    body,
  );
}
