// Agent API v1 wire types (design v2 §9.1). They mirror the snake_case
// boundary types in internal/webui/handlers/agentsv1/wire.go. No body carries
// an actor: the server takes identity from the session, never the browser.

export interface Expect {
  spec_version?: number;
  subject_version?: string;
}

export interface Overrides {
  harness?: string;
  model?: string;
  effort?: string;
  max_budget_usd?: number | null;
  max_run_duration?: number | null; // seconds
  read_only?: boolean;
  allowed_tools?: string[];
  denied_tools?: string[];
}

export interface CreateAgentBody {
  preset: string;
  overrides?: Overrides;
  persona?: { file?: string; text?: string } | null;
  name?: string;
  parent?: string;
  subject?: { type: string; id: string; version?: string };
  repo?: string;
  base_ref?: string;
  external_key?: string;
  first_message?: string;
}

export interface UpdateAgentBody {
  name?: string;
  model?: string;
  harness?: string;
  expect?: Expect;
}

export interface RespondBody {
  decision?: "allow_once" | "allow_always" | "deny";
  answer?: string;
}

export interface WaitingMessage {
  sender: string;
  text: string;
  since: string;
}

export interface Ask {
  id: string;
  type: string; // approval | question
  about: string;
}

export interface Agent {
  agent_id: string;
  workspace_id: string;
  name: string;
  profile_key: string;
  preset: string;
  preset_version: string;
  mode: string;
  interaction_mode: string;
  role_kind: string;
  spec_json: string;
  spec_version: number;
  owner_kind: string;
  owner_id: string;
  created_by_kind: string;
  created_by_id: string;
  parent_agent_id: string | null;
  root_agent_id: string | null;
  subject_type: string | null;
  subject_id: string | null;
  subject_version: string | null;
  external_key: string | null;
  repo: string;
  base_ref: string | null;
  worktree_path: string | null;
  branch: string | null;
  harness: string;
  host: string;
  model: string | null;
  state: string;
  state_reason: string | null;
  waiting_on: string | null;
  attempt: number;
  outcome: string | null;
  archive_reason: string | null;
  attention_reason: string | null;
  running_turn_id: string | null;
  delete_requested: boolean;
  last_active_at: string | null;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
  finished_at: string | null;
  history_purged_at: string | null;
  history_purge_failed_at: string | null; // a due R29 purge failed: expiry incomplete
  deleted_at: string | null;
  compute: string;
  waiting_messages: WaitingMessage[]; // filled by Get only
  open_asks: Ask[]; // filled by Get only
}

export interface AgentList {
  agents: Agent[];
  next: string;
}

/** One saved agent event (seq > 0), or a live-only notice (seq 0). */
export interface AgentEvent {
  agent_id: string;
  seq: number;
  event_id: string;
  kind: string;
  turn_id: string;
  payload: unknown;
  created_at: string;
}

export interface EventPage {
  events: AgentEvent[];
  snapshot_seq: number;
  next: number;
  more: boolean;
}

/** queue (default) or interrupt: stop the running turn; text, if any, goes first. */
export type Delivery = "queue" | "interrupt";

export interface SendResult {
  message_id: string;
  /** handed | waiting | no_op (an interrupt with no text while no turn runs) */
  state: string;
  replaced: boolean;
  turn_id?: string;
  /** Set only for delivery interrupt: whether a running turn was stopped. */
  interrupted?: boolean;
}

export interface WithdrawResult {
  result: "withdrawn" | "nothing_waiting" | "already_handed";
}

export interface PermissionRule {
  action: string;
  resource: string;
  effect: string;
}

export interface Preset {
  name: string;
  version: number;
  mode: string;
  role_kind: string;
  owner_kind: string;
  external_key_fmt: string;
  persona: string;
  harnesses: string[];
  rules: PermissionRule[];
  tools: string[] | null;
  subagents: boolean;
  overridable: string[] | null;
}

/** The body of every Agent API error, and of the stream's error frame. */
export interface AgentApiErrorBody {
  error: string;
  code?: string;
  allowed?: string[];
  paths?: string[];
  fingerprint?: string;
}
