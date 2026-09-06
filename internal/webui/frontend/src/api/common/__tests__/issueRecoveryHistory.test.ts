import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { prepareIssueRecovery } from "../issueRecovery";
import type { RecoveryHandle } from "../recoveryHandle";
const now = Date.parse("2026-09-05T00:00:00Z");
const offer: RecoveryHandle = {
  handle: "A".repeat(43),
  source_identity: "s1.Zml4dHVyZQ",
  workspace: "WS",
  source_repos: [],
  expires_at: "2026-09-05T00:01:00Z",
  manifest: "fleet.issue-workspace.v5",
};
const issue = {
  id: "WS-1",
  workspace: "WS",
  title: "issue",
  status: "open",
  type: "task",
  priority: 1,
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
  created_by: "actor",
  labels: [],
  metadata: {},
};
const event = (id = "1-0") => ({
  id,
  workspace_id: "WS",
  timestamp: "2026-09-05T01:02:03.123456789-07:00",
  actor: "actor",
  action: "issue.create",
  entity_type: "issue",
  entity_id: "WS-1",
  before: "",
  after: '{"title":"native"}',
  metadata: { preserved: "value" },
});
const history = (events: unknown[] = [event()]) => ({
  issue_id: "WS-1",
  present: true,
  events,
  has_older: false,
});
const document = (history: unknown) => ({
  manifest: offer.manifest,
  workspace: "WS",
  through: "c2.Zml4dHVyZQ",
  issues: [issue],
  total: 1,
  ready: [],
  blocked: [],
  deferred: [],
  dependencies: [],
  comments: [],
  history,
});
const prepare = (value: unknown, expected: string | undefined = "WS-1") =>
  prepareIssueRecovery(
    JSON.stringify(document(value)),
    offer,
    offer.handle,
    now,
    expected,
  );
describe("selected native recovery history", () => {
  it("preserves and freezes native source records independently of the SSE checkpoint", () => {
    const input = history([
      event("9007199254740992-0"),
      event("9223372036854775807-0"),
    ]);
    const result = prepare(input);
    expect(result.history).toEqual(input);
    expect(result.through).toBe("c2.Zml4dHVyZQ");
    expect(result.coverage).toContain("history");
    expect(Object.isFrozen(result.history?.events)).toBe(true);
    expect(Object.isFrozen(result.history?.events[0].metadata)).toBe(true);
    expect(result.document).toBe(JSON.stringify(document(input)));
  });
  it("requires explicit selection and distinguishes authoritative absence from empty present history", () => {
    expect(() =>
      prepareIssueRecovery(
        JSON.stringify(document(history())),
        offer,
        offer.handle,
        now,
      ),
    ).toThrow();
    expect(() => prepare(null)).toThrow();
    expect(() => prepare(history(), "WS-2")).toThrow();
    expect(() => prepare(history(), "\u0085")).toThrow();
    expect(prepare(history([])).history?.present).toBe(true);
    const absent = {
      ...document({
        issue_id: "WS-1",
        present: false,
        events: [],
        has_older: false,
      }),
      issues: [],
      total: 0,
    };
    expect(
      prepareIssueRecovery(
        JSON.stringify(absent),
        offer,
        offer.handle,
        now,
        "WS-1",
      ).history?.present,
    ).toBe(false);
    expect(() => prepare({ ...history([]), present: false })).toThrow();
  });
  it.each([
    "0-0",
    "0",
    "01-0",
    "+1-0",
    "1-00",
    "1-1",
    "9223372036854775808-0",
    "c2.Zml4dHVyZQ",
    "1-0 ",
  ])("rejects noncanonical or out-of-range record id %s", (id) => {
    expect(() => prepare(history([event(id)]))).toThrow();
  });
  it("rejects duplicate and descending positions without comparing timestamps", () => {
    expect(() => prepare(history([event("2-0"), event("2-0")]))).toThrow();
    expect(() => prepare(history([event("2-0"), event("1-0")]))).toThrow();
    expect(
      prepare(
        history([
          event("1-0"),
          { ...event("2-0"), timestamp: "2025-01-01T00:00:00Z" },
        ]),
      ).history?.events,
    ).toHaveLength(2);
  });
  it("requires the window boundary for has_older", () => {
    const events = Array.from({ length: 200 }, (_, index) =>
      event(`${index + 1}-0`),
    );
    expect(
      prepare({ ...history(events), has_older: true }).history?.has_older,
    ).toBe(true);
    expect(() =>
      prepare({ ...history(events.slice(1)), has_older: true }),
    ).toThrow();
    expect(() => prepare(history([...events, event("201-0")]))).toThrow();
  });
  it.each([
    { workspace_id: "OTHER" },
    { entity_id: "WS-2" },
    { actor: "" },
    { action: "issue.unknown" },
    { entity_type: "comment" },
    { timestamp: "0001-01-01T00:00:00Z" },
    { before: null },
    { after: {} },
    { metadata: null },
    { metadata: { key: 1 } },
    { extra: true },
  ])("rejects malformed event patch %j", (patch) => {
    expect(() => prepare(history([{ ...event(), ...patch }]))).toThrow();
  });
  it("requires all event fields and retains blank actor allowed by Fleet's nonempty rule", () => {
    for (const key of Object.keys(event())) {
      const row: Record<string, unknown> = event();
      delete row[key];
      expect(() => prepare(history([row]))).toThrow();
    }
    expect(
      prepare(history([{ ...event(), actor: " " }])).history?.events[0].actor,
    ).toBe(" ");
  });
  it("matches every generated Fleet action/entity pair", () => {
    const fixture = readFileSync(
      new URL(
        "../../../../../../backend/fleet/fleet_action_contract_generated_test.go",
        import.meta.url,
      ),
      "utf8",
    );
    const actions = [
      ...fixture.matchAll(/Action: "([^"]+)", EntityType: "([^"]+)"/g),
    ];
    expect(actions.length).toBeGreaterThan(40);
    for (const [, action, entity_type] of actions) {
      expect(
        prepare(history([{ ...event(), action, entity_type }])).history
          ?.events[0].action,
      ).toBe(action);
      expect(() =>
        prepare(history([{ ...event(), action, entity_type: "wrong" }])),
      ).toThrow();
    }
  });
  it("bounds exact selected scope by UTF8 bytes and rejects unpaired UTF16", () => {
    const atLimit = "😀".repeat(256);
    const absent = (id: string) =>
      JSON.stringify({
        ...document({
          issue_id: id,
          present: false,
          events: [],
          has_older: false,
        }),
        issues: [],
        total: 0,
      });
    expect(
      prepareIssueRecovery(absent(atLimit), offer, offer.handle, now, atLimit)
        .history?.issue_id,
    ).toBe(atLimit);
    for (const invalid of [atLimit + "a", "\uD800", "\uDC00"]) {
      expect(() =>
        prepareIssueRecovery(
          absent(invalid),
          offer,
          offer.handle,
          now,
          invalid,
        ),
      ).toThrow();
    }
  });
  it("rejects legacy v4 even when history is present", () => {
    expect(() =>
      prepareIssueRecovery(
        JSON.stringify({
          ...document(null),
          manifest: "fleet.issue-workspace.v4",
        }),
        offer,
        offer.handle,
        now,
      ),
    ).toThrow();
  });
});
