import { get, post, put, unwrapResponse, wsUrl } from "@/api/common";
import type {
  EvalCronState,
  EvalRejudgeResult,
  EvalRollupData,
  SessionEvalState,
} from "@/types";

type Envelope<T> = {
  success: boolean;
  data?: T;
  error?: string;
};

export type EvalRollupWindowDays = 7 | 30;

export interface EvalRollupWindow {
  since: string;
  until: string;
}

function appendQuery(
  path: string,
  params: Record<string, string | undefined>,
): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (!value) continue;
    query.set(key, value);
  }
  const qs = query.toString();
  return qs ? `${path}?${qs}` : path;
}

export function buildEvalRollupWindow(
  windowDays: EvalRollupWindowDays,
  now = new Date(),
): EvalRollupWindow {
  const until = now.toISOString();
  const since = new Date(
    now.getTime() - windowDays * 24 * 60 * 60 * 1000,
  ).toISOString();
  return { since, until };
}

export function buildEvalRollupUrl(
  workspaceId: string,
  window: EvalRollupWindow,
): string {
  return appendQuery(wsUrl(workspaceId, "/eval-rollup"), {
    since: window.since,
    until: window.until,
  });
}

export async function fetchEvalRollup(
  workspaceId: string,
  window: EvalRollupWindow,
): Promise<EvalRollupData> {
  const envelope = await get<Envelope<EvalRollupData>>(
    buildEvalRollupUrl(workspaceId, window),
  );
  return unwrapResponse(envelope);
}

export async function getEvalCronState(
  workspaceId: string,
): Promise<EvalCronState> {
  const envelope = await get<Envelope<EvalCronState>>(
    wsUrl(workspaceId, "/evals/cron"),
  );
  return unwrapResponse(envelope);
}

export async function setEvalCronEnabled(
  workspaceId: string,
  enabled: boolean,
): Promise<EvalCronState> {
  const envelope = await put<Envelope<EvalCronState>>(
    wsUrl(workspaceId, "/evals/cron"),
    { enabled },
  );
  return unwrapResponse(envelope);
}

export async function getSessionEval(
  workspaceId: string,
  sessionId: string,
): Promise<SessionEvalState> {
  const envelope = await get<Envelope<SessionEvalState>>(
    wsUrl(workspaceId, `/sessions/${encodeURIComponent(sessionId)}/eval`),
  );
  return unwrapResponse(envelope);
}

export async function rejudgeSession(
  workspaceId: string,
  sessionId: string,
): Promise<EvalRejudgeResult> {
  const envelope = await post<Envelope<EvalRejudgeResult>>(
    wsUrl(workspaceId, `/sessions/${encodeURIComponent(sessionId)}/rejudge`),
    {},
  );
  return unwrapResponse(envelope);
}
