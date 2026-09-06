import { describe, expect, it } from "vitest";
import { prepareIssueRecovery } from "../issueRecovery";
import type { RecoveryHandle } from "../recoveryHandle";
const now = Date.parse("2026-09-05T12:00:00Z");
const offer: RecoveryHandle = {
  handle: "A".repeat(43),
  source_identity: "s1.Zml4dHVyZQ",
  workspace: "WS",
  source_repos: [],
  expires_at: "2026-09-05T12:01:00Z",
  manifest: "fleet.issue-workspace.v2",
};
function issue(id = "WS-1"): Record<string, unknown> {
  return {
    id,
    workspace: "WS",
    title: "title",
    status: "custom",
    type: "custom-kind",
    priority: 2,
    created_at: "2026-09-05T01:02:03.123456789-07:00",
    created_by: "alice",
    updated_at: "2026-09-05T01:02:03Z",
    labels: [],
    metadata: { kept: "yes" },
    estimated_minutes: 42,
    future: { nested: [true, "value", 1.5] },
  };
}
function document(): Record<string, unknown> {
  return {
    manifest: offer.manifest,
    workspace: "WS",
    through: "c2.Zml4dHVyZQ",
    issues: [],
    total: 0,
    ready: [],
    blocked: [],
    deferred: [],
  };
}
function prepare(value: unknown) {
  return prepareIssueRecovery(JSON.stringify(value), offer, offer.handle, now);
}
describe("prepareIssueRecovery", () => {
  it("prepares empty native coverage without a cache or ack stamp", () => {
    const raw = JSON.stringify(document());
    const result = prepareIssueRecovery(raw, offer, offer.handle, now);
    expect(result.document).toBe(raw);
    expect(result.offerSourceIdentity).toBe(offer.source_identity);
    expect(result.coverage).toEqual(["issues", "ready", "blocked", "deferred"]);
    expect(result.issues).toEqual([]);
    expect(Object.isFrozen(result)).toBe(true);
  });
  it("preserves every native extension and freezes nested records", () => {
    const row = issue();
    const d = { ...document(), issues: [row], total: 1, ready: [row] };
    const result = prepare(d);
    expect(result.issues[0]).toEqual(row);
    expect(result.issues[0].created_at).toBe(row.created_at);
    expect(Object.isFrozen(result.issues)).toBe(true);
    expect(Object.isFrozen(result.issues[0].metadata)).toBe(true);
    expect(Object.isFrozen(result.issues[0].future)).toBe(true);
    expect(() => {
      (result.issues[0].metadata as Record<string, string>).kept = "changed";
    }).toThrow();
  });
  it("accepts direct blockers and sole parent sentinel", () => {
    for (const parent of [false, true]) {
      const row = issue();
      const other = issue("WS-2");
      const blocker = parent
        ? {
            id: "",
            title: "",
            priority: 0,
            status: "",
            dep_type: "parent-child",
            reason: "parent-blocked",
          }
        : {
            id: "WS-2",
            title: "title",
            priority: 2,
            status: "custom",
            dep_type: "blocks",
            reason: "direct",
          };
      const result = prepare({
        ...document(),
        issues: [row, other],
        total: 2,
        blocked: [{ issue: row, blockers: [blocker] }],
      });
      expect(result.blocked[0].blockers).toEqual([blocker]);
    }
  });
  it.each([
    ["foreign", { workspace: "OTHER" }],
    ["null labels", { labels: null }],
    ["null metadata", { metadata: null }],
    ["bad metadata", { metadata: { bad: 1 } }],
    ["alias", { source_repo: "repo" }],
    ["alias mismatch", { source_repo: "one", repo: "two" }],
    ["bad date", { created_at: "2026-02-30T01:00:00Z" }],
    ["bad clock", { updated_at: "2026-09-05T25:00:00Z" }],
    ["bad estimate", { estimated_minutes: "42" }],
  ])("rejects malformed native issue %s", (_name, patch) => {
    expect(() =>
      prepare({ ...document(), issues: [{ ...issue(), ...patch }], total: 1 }),
    ).toThrow();
  });
  it.each(["issues", "ready", "blocked", "deferred"])(
    "rejects missing/null %s",
    (key) => {
      const d = document();
      delete d[key];
      expect(() => prepare(d)).toThrow();
      d[key] = null;
      expect(() => prepare(d)).toThrow();
    },
  );
  it("rejects duplicate identities and divergent derived records", () => {
    const row = issue();
    expect(() =>
      prepare({ ...document(), issues: [row, row], total: 2 }),
    ).toThrow();
    expect(() =>
      prepare({
        ...document(),
        issues: [row],
        total: 1,
        ready: [{ ...row, metadata: { different: "yes" } }],
      }),
    ).toThrow();
    expect(() => prepare({ ...document(), ready: [row] })).toThrow();
  });
  it("checks the offer expiry and exact echo", () => {
    expect(() =>
      prepareIssueRecovery(
        JSON.stringify(document()),
        offer,
        "B".repeat(43),
        now,
      ),
    ).toThrow();
    expect(() =>
      prepareIssueRecovery(
        JSON.stringify(document()),
        offer,
        offer.handle,
        now + 60000,
      ),
    ).toThrow();
  });
  it.each([
    "0",
    "c1.MTAtMA",
    "c1.MA",
    "c1.JA",
    "c2.!",
    "c2.Zml4dHVyZR",
    "c2.Zml4dHVyZQ==",
  ])("rejects invalid through %s", (through) => {
    expect(() => prepare({ ...document(), through })).toThrow();
  });
  it.each([
    '{"a":1,"a":2}',
    '{"a":{"x":1,"\\u0078":2}}',
    '{"n":9007199254740993}',
    '{"n":1e309}',
    '{"n":-9007199254740993}',
  ])("rejects duplicate/unsafe unknown JSON before parsing", (extension) => {
    const raw = JSON.stringify({
      ...document(),
      issues: [issue()],
      total: 1,
    }).replace(
      '"future":' + JSON.stringify(issue().future),
      '"future":' + extension,
    );
    expect(() => prepareIssueRecovery(raw, offer, offer.handle, now)).toThrow();
  });
  it("rejects oversized UTF-8 before JSON parsing", () => {
    expect(() =>
      prepareIssueRecovery(
        "é".repeat(8 * 1024 * 1024 + 1),
        offer,
        offer.handle,
        now,
      ),
    ).toThrow();
  });
  it("rejects trailing JSON and malformed syntax", () => {
    for (const suffix of ["{}", ",", " NaN"]) {
      expect(() =>
        prepareIssueRecovery(
          JSON.stringify(document()) + suffix,
          offer,
          offer.handle,
          now,
        ),
      ).toThrow();
    }
  });
});

it.each(["1.234567890123456789", "9007199254740991.1", "1e-999"])(
  "rejects rounded native number %s",
  (token) => {
    const raw = JSON.stringify({
      ...document(),
      issues: [issue()],
      total: 1,
    }).replace(
      '"future":' + JSON.stringify(issue().future),
      '"future":{"number":' + token + "}",
    );
    expect(() => prepareIssueRecovery(raw, offer, offer.handle, now)).toThrow();
  },
);
it.each(["0.1", "1.00", "1e0", "-0", "1.5e-5"])(
  "preserves representable native number %s",
  (token) => {
    const raw = JSON.stringify({
      ...document(),
      issues: [issue()],
      total: 1,
    }).replace(
      '"future":' + JSON.stringify(issue().future),
      '"future":{"number":' + token + "}",
    );
    expect(prepareIssueRecovery(raw, offer, offer.handle, now).document).toBe(
      raw,
    );
  },
);
it("retains a frozen defensive offer copy", () => {
  const input = { ...offer, source_repos: ["repo"] };
  const result = prepareIssueRecovery(
    JSON.stringify(document()),
    input,
    input.handle,
    now,
  );
  input.source_repos[0] = "changed";
  expect(result.offer.source_repos).toEqual(["repo"]);
  expect(Object.isFrozen(result.offer)).toBe(true);
  expect(Object.isFrozen(result.offer.source_repos)).toBe(true);
});
