import { isRecoveryEnvelope } from "./recoveryEnvelope";
import { decodeRecoveryHandle, type RecoveryHandle } from "./recoveryHandle";
import { parseStrictRecoveryJSON } from "./strictRecoveryJSON";

export type NativeRecoveryJSON =
  | null
  | boolean
  | number
  | string
  | readonly NativeRecoveryJSON[]
  | { readonly [key: string]: NativeRecoveryJSON };
export interface NativeRecoveryIssue {
  readonly [key: string]: NativeRecoveryJSON;
  readonly id: string;
  readonly workspace: string;
  readonly title: string;
  readonly status: string;
  readonly type: string;
  readonly priority: number;
  readonly created_at: string;
  readonly created_by: string;
  readonly updated_at: string;
  readonly labels: readonly string[];
  readonly metadata: Readonly<Record<string, string>>;
}
export interface NativeRecoveryBlocker {
  readonly id: string;
  readonly title: string;
  readonly priority: number;
  readonly status: string;
  readonly dep_type: "blocks" | "parent-child";
  readonly reason: "direct" | "parent-blocked";
}
export interface NativeRecoveryBlocked {
  readonly issue: NativeRecoveryIssue;
  readonly blockers: readonly NativeRecoveryBlocker[];
}
export interface NativeRecoveryDependency {
  readonly issue_id: string;
  readonly depends_on_id: string;
  readonly type:
    | "blocks"
    | "parent-child"
    | "related"
    | "duplicate-of"
    | "superseded-by";
  readonly created_at: string;
  readonly created_by: string;
}
export interface NativeRecoveryComment {
  readonly id: string;
  readonly issue_id: string;
  readonly author: string;
  readonly body: string;
  readonly created_at: string;
}
export interface NativeRecoveryEvent {
  readonly id: string;
  readonly workspace_id: string;
  readonly timestamp: string;
  readonly actor: string;
  readonly action: string;
  readonly entity_type: string;
  readonly entity_id: string;
  readonly before: string;
  readonly after: string;
  readonly metadata: Readonly<Record<string, string>>;
}
export interface NativeRecoveryHistory {
  readonly issue_id: string;
  readonly present: boolean;
  readonly events: readonly NativeRecoveryEvent[];
  readonly has_older: boolean;
}
/** Prepared native data only. Coverage names identify this fixed manifest, not
 * mounted UI query coverage, a cache commit, or permission to acknowledge SSE. */
export interface PreparedIssueRecovery {
  readonly offer: RecoveryHandle;
  readonly document: string;
  readonly manifest: "fleet.issue-workspace.v5";
  readonly workspace: string;
  readonly through: string;
  readonly offerSourceIdentity: string;
  readonly total: number;
  readonly issues: readonly NativeRecoveryIssue[];
  readonly ready: readonly NativeRecoveryIssue[];
  readonly blocked: readonly NativeRecoveryBlocked[];
  readonly deferred: readonly NativeRecoveryIssue[];
  readonly dependencies: readonly NativeRecoveryDependency[];
  readonly comments: readonly NativeRecoveryComment[];
  readonly history: NativeRecoveryHistory | null;
  readonly coverage: readonly [
    "issues",
    "ready",
    "blocked",
    "deferred",
    "dependencies",
    "comments",
    "history"?,
  ];
}
function fail(): never {
  throw new Error("Invalid native issue recovery snapshot");
}
function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    fail();
  return value as Record<string, unknown>;
}
function exact(value: Record<string, unknown>, keys: readonly string[]) {
  if (
    Object.keys(value).length !== keys.length ||
    keys.some((key) => !Object.prototype.hasOwnProperty.call(value, key))
  )
    fail();
}
function text(value: unknown, empty = false): string {
  if (typeof value !== "string" || (!empty && value.length === 0)) fail();
  return value;
}
function array(value: unknown): unknown[] {
  if (!Array.isArray(value)) fail();
  return value;
}
function timestamp(value: unknown) {
  const source = text(value);
  const parts =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|([+-])(\d{2}):(\d{2}))$/.exec(
      source,
    );
  if (!parts) fail();
  const [year, month, day, hour, minute, second] = parts
    .slice(1, 7)
    .map(Number) as [number, number, number, number, number, number];
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > (days[month - 1] ?? 0) ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    (parts[7] !== undefined &&
      (Number(parts[8]) > 23 || Number(parts[9]) > 59)) ||
    !Number.isFinite(Date.parse(source))
  )
    fail();
}
function cursor(value: unknown): string {
  const token = text(value);
  if (!isRecoveryEnvelope(token, "c2.")) fail();
  return token;
}
function issue(value: unknown, workspace: string): NativeRecoveryIssue {
  const row = object(value);
  for (const field of [
    "workspace",
    "id",
    "title",
    "status",
    "type",
    "priority",
    "created_at",
    "created_by",
    "updated_at",
    "labels",
    "metadata",
  ]) {
    if (!Object.prototype.hasOwnProperty.call(row, field)) fail();
  }
  for (const field of [
    "workspace",
    "id",
    "title",
    "status",
    "type",
    "created_at",
    "updated_at",
  ])
    text(row[field]);
  text(row.created_by, true);
  if (row.workspace !== workspace) fail();
  if (
    !Number.isInteger(row.priority) ||
    (row.priority as number) < 0 ||
    (row.priority as number) > 4
  )
    fail();
  timestamp(row.created_at);
  timestamp(row.updated_at);
  for (const label of array(row.labels)) text(label, true);
  for (const entry of Object.values(object(row.metadata))) text(entry, true);
  optionalFields(row);
  return row as NativeRecoveryIssue;
}
function optionalFields(row: Record<string, unknown>) {
  for (const field of [
    "description",
    "design",
    "acceptance_criteria",
    "notes",
    "design_format",
    "assignee",
    "owner",
    "external_ref",
    "parent_id",
    "repo",
    "close_reason",
  ]) {
    if (Object.prototype.hasOwnProperty.call(row, field))
      text(row[field], true);
  }
  for (const field of ["defer_until", "due_at", "closed_at"]) {
    if (Object.prototype.hasOwnProperty.call(row, field) && row[field] !== null)
      timestamp(row[field]);
  }
  if (
    Object.prototype.hasOwnProperty.call(row, "estimated_minutes") &&
    row.estimated_minutes !== null &&
    !Number.isSafeInteger(row.estimated_minutes)
  )
    fail();
  for (const [alias, native] of [
    ["source_repo", "repo"],
    ["issue_type", "type"],
    ["parent", "parent_id"],
  ] as const) {
    if (
      Object.prototype.hasOwnProperty.call(row, alias) &&
      (!Object.prototype.hasOwnProperty.call(row, native) ||
        text(row[alias], true) !== text(row[native], true))
    )
      fail();
  }
}
function equal(left: unknown, right: unknown): boolean {
  if (left === right) return true;
  if (
    left === null ||
    right === null ||
    typeof left !== "object" ||
    typeof right !== "object"
  )
    return false;
  if (Array.isArray(left) !== Array.isArray(right)) return false;
  const a = left as Record<string, unknown>,
    b = right as Record<string, unknown>;
  return (
    Object.keys(a).length === Object.keys(b).length &&
    Object.keys(a).every(
      (key) =>
        Object.prototype.hasOwnProperty.call(b, key) && equal(a[key], b[key]),
    )
  );
}
function derived(
  values: unknown[],
  workspace: string,
  all: Map<string, NativeRecoveryIssue>,
): NativeRecoveryIssue[] {
  const seen = new Set<string>();
  return values.map((value) => {
    const row = issue(value, workspace);
    if (seen.has(row.id) || !equal(row, all.get(row.id))) fail();
    seen.add(row.id);
    return row;
  });
}
function blocked(
  values: unknown[],
  workspace: string,
  all: Map<string, NativeRecoveryIssue>,
): NativeRecoveryBlocked[] {
  const seen = new Set<string>();
  return values.map((value) => {
    const row = object(value);
    exact(row, ["issue", "blockers"]);
    const native = derived([row.issue], workspace, all)[0];
    if (!native || seen.has(native.id)) fail();
    seen.add(native.id);
    const blockers = array(row.blockers);
    if (blockers.length === 0) fail();
    const ids = new Set<string>();
    for (const detail of blockers)
      validateBlocker(detail, blockers.length, all, ids);
    return row as unknown as NativeRecoveryBlocked;
  });
}
function validateBlocker(
  value: unknown,
  count: number,
  all: Map<string, NativeRecoveryIssue>,
  seen: Set<string>,
) {
  const row = object(value);
  exact(row, ["id", "title", "priority", "status", "dep_type", "reason"]);
  for (const key of ["id", "title", "status", "dep_type", "reason"])
    text(row[key], true);
  if (!Number.isSafeInteger(row.priority)) fail();
  if (row.reason === "parent-blocked") {
    if (
      count !== 1 ||
      row.dep_type !== "parent-child" ||
      row.id !== "" ||
      row.title !== "" ||
      row.status !== "" ||
      row.priority !== 0
    )
      fail();
    return;
  }
  if (row.reason !== "direct" || row.dep_type !== "blocks") fail();
  const id = text(row.id);
  const native = all.get(id);
  if (
    !native ||
    seen.has(id) ||
    row.title !== native.title ||
    row.status !== native.status ||
    row.priority !== native.priority
  )
    fail();
  seen.add(id);
}
function dependencies(
  values: unknown[],
  all: Map<string, NativeRecoveryIssue>,
): NativeRecoveryDependency[] {
  const seen = new Set<string>();
  const types = new Set([
    "blocks",
    "parent-child",
    "related",
    "duplicate-of",
    "superseded-by",
  ]);
  return values.map((value) => {
    const row = object(value);
    exact(row, [
      "issue_id",
      "depends_on_id",
      "type",
      "created_at",
      "created_by",
    ]);
    const from = text(row.issue_id),
      to = text(row.depends_on_id),
      type = text(row.type);
    if (!all.has(from) || !all.has(to) || from === to || !types.has(type))
      fail();
    const identity = JSON.stringify([from, to, type]);
    if (seen.has(identity)) fail();
    seen.add(identity);
    text(row.created_by);
    nonzeroTimestamp(row.created_at);
    return row as unknown as NativeRecoveryDependency;
  });
}
function nonzeroTimestamp(value: unknown) {
  timestamp(value);
  const source = text(value);
  // Date.parse truncates nanoseconds. Reject Go's zero instant, including
  // offset aliases, while preserving a nonzero sub-millisecond fraction.
  if (
    Date.parse(source) === Date.parse("0001-01-01T00:00:00Z") &&
    !/\.\d*[1-9]\d*(?:Z|[+-]\d{2}:\d{2})$/.test(source)
  )
    fail();
}
function nonblank(value: unknown): string {
  const source = text(value);
  // Exact unicode.IsSpace set used by Go strings.TrimSpace. JS trim also
  // removes BOM and omits NEL; neither behavior matches the native contract.
  if (
    /^[\t\n\v\f\r\u0020\u0085\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000]*$/.test(
      source,
    )
  )
    fail();
  return source;
}
function comments(
  values: unknown[],
  all: Map<string, NativeRecoveryIssue>,
): NativeRecoveryComment[] {
  const seen = new Set<string>();
  const encoder = new TextEncoder();
  return values.map((value) => {
    const row = object(value);
    exact(row, ["id", "issue_id", "author", "body", "created_at"]);
    const id = nonblank(row.id);
    if (seen.has(id) || !all.has(text(row.issue_id))) fail();
    seen.add(id);
    nonblank(row.author);
    if (encoder.encode(nonblank(row.body)).length > 10000) fail();
    nonzeroTimestamp(row.created_at);
    return row as unknown as NativeRecoveryComment;
  });
}
// Canonical Fleet action/entity contract; parity-tested against the generated
// Fleet action fixture. These history record IDs never become SSE checkpoints.
const historyActionEntities = new Map<string, string>([
  ...[
    "create",
    "update",
    "close",
    "reopen",
    "delete",
    "claim",
    "release",
    "assign",
    "defer",
    "undefer",
  ].map((op): [string, string] => [`issue.${op}`, "issue"]),
  ...["add", "remove"].map((op): [string, string] => [
    `dep.${op}`,
    "dependency",
  ]),
  ...["add", "remove"].map((op): [string, string] => [`label.${op}`, "label"]),
  ...["set", "remove"].map((op): [string, string] => [
    `metadata.${op}`,
    "metadata",
  ]),
  ["comment.add", "comment"],
  ...["workspace", "repo", "agent", "role", "skill", "skill_pack"].flatMap(
    (entity) =>
      ["create", "update", "delete"].map((op): [string, string] => [
        `${entity}.${op}`,
        entity,
      ]),
  ),
  ...[
    "create",
    "claim",
    "heartbeat",
    "finish",
    "recover",
    "suspend",
    "resume",
  ].map((op): [string, string] => [`driver_run.${op}`, "driver_run"]),
  ["daemon.update", "daemon_profile"],
]);
export function selectedHistoryIdentity(value: unknown): string {
  const identity = nonblank(value);
  if (
    /[\uD800-\uDFFF]/u.test(identity) ||
    new TextEncoder().encode(identity).length > 1024
  )
    fail();
  return identity;
}
function history(
  value: unknown,
  workspace: string,
  all: Map<string, NativeRecoveryIssue>,
  expectedIssueId: string | undefined,
): NativeRecoveryHistory | null {
  if (expectedIssueId === undefined) {
    if (value !== null) fail();
    return null;
  }
  selectedHistoryIdentity(expectedIssueId);
  const row = object(value);
  exact(row, ["issue_id", "present", "events", "has_older"]);
  if (
    selectedHistoryIdentity(row.issue_id) !== expectedIssueId ||
    typeof row.present !== "boolean" ||
    typeof row.has_older !== "boolean" ||
    row.present !== all.has(expectedIssueId)
  )
    fail();
  const events = array(row.events);
  if (
    events.length > 200 ||
    (row.has_older && events.length !== 200) ||
    (!row.present && (events.length !== 0 || row.has_older))
  )
    fail();
  let previous = 0n;
  for (const value of events) {
    const event = object(value);
    exact(event, [
      "id",
      "workspace_id",
      "timestamp",
      "actor",
      "action",
      "entity_type",
      "entity_id",
      "before",
      "after",
      "metadata",
    ]);
    const id = text(event.id);
    if (!/^[1-9][0-9]{0,18}-0$/.test(id)) fail();
    const position = BigInt(id.slice(0, -2));
    if (position > 9223372036854775807n || position <= previous) fail();
    previous = position;
    if (
      event.workspace_id !== workspace ||
      event.entity_id !== expectedIssueId ||
      historyActionEntities.get(text(event.action)) !== text(event.entity_type)
    )
      fail();
    nonzeroTimestamp(event.timestamp);
    text(event.actor);
    text(event.before, true);
    text(event.after, true);
    for (const item of Object.values(object(event.metadata))) text(item, true);
  }
  return row as unknown as NativeRecoveryHistory;
}
function freeze<T>(value: T): T {
  if (value !== null && typeof value === "object") {
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  }
  return value;
}

export function prepareIssueRecovery(
  document: string,
  offer: RecoveryHandle,
  echoHandle: string,
  now = Date.now(),
  expectedIssueId?: string,
): PreparedIssueRecovery {
  if (
    typeof document !== "string" ||
    document.length > 16 * 1024 * 1024 ||
    new TextEncoder().encode(document).length > 16 * 1024 * 1024
  )
    fail();
  const validatedOffer =
    offer &&
    decodeRecoveryHandle(offer, offer.workspace, offer.source_repos, now);
  if (!validatedOffer || echoHandle !== validatedOffer.handle) fail();
  const root = object(parseStrictRecoveryJSON(document));
  exact(root, [
    "manifest",
    "workspace",
    "through",
    "issues",
    "total",
    "ready",
    "blocked",
    "deferred",
    "dependencies",
    "comments",
    "history",
  ]);
  if (
    root.manifest !== validatedOffer.manifest ||
    root.workspace !== validatedOffer.workspace
  )
    fail();
  const through = cursor(root.through);
  const issues = array(root.issues).map((value) =>
    issue(value, validatedOffer.workspace),
  );
  if (!Number.isSafeInteger(root.total) || root.total !== issues.length) fail();
  const all = new Map<string, NativeRecoveryIssue>();
  for (const row of issues) {
    if (all.has(row.id)) fail();
    all.set(row.id, row);
  }
  const ready = derived(array(root.ready), validatedOffer.workspace, all);
  const deferred = derived(array(root.deferred), validatedOffer.workspace, all);
  const blockedRows = blocked(
    array(root.blocked),
    validatedOffer.workspace,
    all,
  );
  const selectedHistory = history(
    root.history,
    validatedOffer.workspace,
    all,
    expectedIssueId,
  );
  return freeze({
    document,
    offer: validatedOffer,
    manifest: "fleet.issue-workspace.v5",
    workspace: validatedOffer.workspace,
    through,
    offerSourceIdentity: validatedOffer.source_identity,
    total: issues.length,
    issues,
    ready,
    blocked: blockedRows,
    deferred,
    dependencies: dependencies(array(root.dependencies), all),
    comments: comments(array(root.comments), all),
    history: selectedHistory,
    coverage: [
      "issues",
      "ready",
      "blocked",
      "deferred",
      "dependencies",
      "comments",
      ...(selectedHistory ? (["history"] as const) : ([] as const)),
    ],
  }) as PreparedIssueRecovery;
}
