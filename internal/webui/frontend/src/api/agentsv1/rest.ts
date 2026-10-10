// Typed REST calls for the Agent API (design v2 §9.1). Every write carries
// the caller's RequestID as Idempotency-Key: make one per user action with
// newRequestId() and reuse it on retry.

import { get, post, patch, put, del, wsUrl } from "../common/client";
import type {
  Agent,
  AgentList,
  CreateAgentBody,
  CustomModels,
  Delivery,
  EventPage,
  HarnessInfo,
  ModelCatalog,
  Preset,
  RespondBody,
  SendResult,
  UpdateAgentBody,
  WithdrawResult,
} from "./types";

export function newRequestId(): string {
  return crypto.randomUUID();
}

const v1 = (ws: string, path: string) => wsUrl(ws, `/v1${path}`);
const agentPath = (ws: string, id: string, rest = "") =>
  v1(ws, `/agents/${encodeURIComponent(id)}${rest}`);
const idem = (requestId: string) => ({
  headers: { "Idempotency-Key": requestId },
});

function query(params: Record<string, string | number | boolean | undefined>) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

export const createAgent = (
  ws: string,
  body: CreateAgentBody,
  requestId: string,
) => post<Agent>(v1(ws, "/agents"), body, idem(requestId));

export interface ListAgentsQuery {
  owner_kind?: string;
  owner_id?: string;
  parent?: string;
  root?: string;
  preset?: string;
  mode?: string;
  harness?: string;
  role_kind?: string;
  state?: string;
  subject_type?: string;
  subject_id?: string;
  external_key_prefix?: string;
  name?: string;
  include_archived?: boolean;
  after?: string;
  limit?: number;
}

export const listAgents = (ws: string, q: ListAgentsQuery = {}) =>
  get<AgentList>(v1(ws, "/agents") + query({ ...q }));

export const getAgent = (ws: string, id: string) =>
  get<Agent>(agentPath(ws, id));

export const updateAgent = (
  ws: string,
  id: string,
  body: UpdateAgentBody,
  requestId: string,
) => patch<Agent>(agentPath(ws, id), body, idem(requestId));

export const deleteAgent = (
  ws: string,
  id: string,
  requestId: string,
  opts: { cascade?: boolean; fingerprint?: string } = {},
) => del<void>(agentPath(ws, id) + query(opts), idem(requestId));

export const archiveAgent = (
  ws: string,
  id: string,
  requestId: string,
  reason?: "done" | "cancelled",
) =>
  post<void>(
    agentPath(ws, id, "/archive"),
    reason ? { reason } : undefined,
    idem(requestId),
  );

export const unarchiveAgent = (ws: string, id: string, requestId: string) =>
  post<void>(agentPath(ws, id, "/unarchive"), undefined, idem(requestId));

/** Stop is sendMessage(ws, id, "", requestId, "interrupt"). */
export const sendMessage = (
  ws: string,
  id: string,
  text: string,
  requestId: string,
  delivery?: Delivery,
) =>
  post<SendResult>(
    agentPath(ws, id, "/messages"),
    delivery ? { text, delivery } : { text },
    idem(requestId),
  );

export const withdrawMessage = (ws: string, id: string, requestId: string) =>
  del<WithdrawResult>(agentPath(ws, id, "/messages/waiting"), idem(requestId));

export const respondToAsk = (
  ws: string,
  id: string,
  askId: string,
  body: RespondBody,
  requestId: string,
) =>
  post<void>(
    agentPath(ws, id, `/asks/${encodeURIComponent(askId)}`),
    body,
    idem(requestId),
  );

export interface ListEventsQuery {
  after?: number;
  snapshot?: number;
  limit?: number;
  kinds?: string[];
}

export const listEvents = (ws: string, id: string, q: ListEventsQuery = {}) =>
  get<EventPage>(
    agentPath(ws, id, "/events") +
      query({
        after: q.after,
        snapshot: q.snapshot,
        limit: q.limit,
        kind: q.kinds?.join(","),
      }),
  );

/**
 * Pages every committed event after `after`, pinned at the first page's
 * snapshot so events appended meanwhile wait for the live stream.
 */
export async function listEventsAfter(
  ws: string,
  id: string,
  after: number,
  kinds?: string[],
) {
  const events = [];
  let q: ListEventsQuery = { after, ...(kinds ? { kinds } : {}) };
  for (;;) {
    const page = await listEvents(ws, id, q);
    events.push(...page.events);
    if (!page.more) return events;
    q = { ...q, after: page.next, snapshot: page.snapshot_seq };
  }
}

export const listPresets = (ws: string) =>
  get<{ presets: Preset[] }>(v1(ws, "/presets")).then((r) => r.presets);

export const getPreset = (ws: string, name: string) =>
  get<Preset>(v1(ws, `/presets/${encodeURIComponent(name)}`));

/** The harness's health: why it is unavailable, or that it is newer than tested. */
export const getHarness = (ws: string, harness: string) =>
  get<HarnessInfo>(v1(ws, `/harnesses/${encodeURIComponent(harness)}`));

/** The harness's connected providers and models, with each model's options. */
export const listHarnessModels = (ws: string, harness: string) =>
  get<ModelCatalog>(v1(ws, `/harnesses/${encodeURIComponent(harness)}/models`));

const customPath = (ws: string, harness: string) =>
  v1(ws, `/harnesses/${encodeURIComponent(harness)}/custom`);

/** The model ids this workspace adds to the harness's catalog (MCS3). */
export const getCustomModels = (ws: string, harness: string) =>
  get<CustomModels>(customPath(ws, harness)).then((r) => r.models);

/** Replaces the workspace's custom model ids for the harness. */
export const setCustomModels = (
  ws: string,
  harness: string,
  models: string[],
) =>
  put<CustomModels>(customPath(ws, harness), { models }).then((r) => r.models);
