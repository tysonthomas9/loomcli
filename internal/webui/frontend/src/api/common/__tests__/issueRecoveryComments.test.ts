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
  manifest: "fleet.issue-workspace.v4",
};
const issue = (id: string) => ({
  id,
  workspace: "WS",
  title: "issue",
  type: "task",
  status: "open",
  priority: 1,
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
  created_by: "actor",
  labels: [],
  metadata: {},
});
const comment = () => ({
  id: "C-1",
  issue_id: "WS-1",
  author: " author ",
  body: " body\n",
  created_at: "2026-09-05T01:02:03.123456789-07:00",
});
const document = (comments: unknown) => ({
  manifest: offer.manifest,
  workspace: "WS",
  through: "c2.Zml4dHVyZQ",
  issues: [issue("WS-1"), issue("WS-2")],
  total: 2,
  ready: [],
  blocked: [],
  deferred: [],
  dependencies: [],
  comments,
});
const prepare = (comments: unknown) =>
  prepareIssueRecovery(
    JSON.stringify(document(comments)),
    offer,
    offer.handle,
    now,
  );

describe("native recovery comments", () => {
  it("preserves complete records, source bytes, whitespace and subsecond precision without publication", () => {
    const input = comment();
    const result = prepare([input]);
    expect(result.comments).toEqual([input]);
    expect(result.document).toBe(JSON.stringify(document([input])));
    expect(Object.isFrozen(result.comments)).toBe(true);
    expect(Object.isFrozen(result.comments[0])).toBe(true);
    input.body = "changed";
    expect(result.comments[0].body).toBe(" body\n");
    expect(result.coverage).toContain("comments");
    expect(result).not.toHaveProperty("history");
  });
  it("measures body limit in UTF8 bytes", () => {
    expect(
      prepare([{ ...comment(), body: "😀".repeat(2500) }]).comments,
    ).toHaveLength(1);
    expect(() =>
      prepare([{ ...comment(), body: "😀".repeat(2500) + "a" }]),
    ).toThrow();
  });
  it.each(["id", "author", "body"])(
    "matches Go whitespace for %s without trimming",
    (field) => {
      for (const value of [
        "",
        "\t\n\r ",
        "\u0085",
        "\u00A0",
        "\u1680\u2000\u200A\u2028\u2029\u202F\u205F\u3000",
      ]) {
        expect(() => prepare([{ ...comment(), [field]: value }])).toThrow();
      }
      for (const value of ["\uFEFF", "\u200B", " \u0085x\u0085 "]) {
        const result = prepare([{ ...comment(), [field]: value }]);
        expect(result.comments[0][field as "id" | "author" | "body"]).toBe(
          value,
        );
      }
    },
  );
  it("requires fields, membership, and global exact ID uniqueness", () => {
    for (const field of Object.keys(comment())) {
      const row: Record<string, unknown> = comment();
      delete row[field];
      expect(() => prepare([row])).toThrow();
    }
    expect(() => prepare([{ ...comment(), issue_id: "missing" }])).toThrow();
    expect(() =>
      prepare([comment(), { ...comment(), issue_id: "WS-2" }]),
    ).toThrow();
    expect(() => prepare([{ ...comment(), extra: "forbidden" }])).toThrow();
    expect(
      prepare([comment(), { ...comment(), id: " C-1 " }]).comments,
    ).toHaveLength(2);
  });
  it.each([
    "0001-01-01T00:00:00Z",
    "0001-01-01T01:00:00+01:00",
    "2026-02-30T00:00:00Z",
    "2026-09-05T1:00:00Z",
    "2026-09-05T01:00:00,123Z",
    "2026-09-05T01:00:00.1234567899Z",
    "2026-09-05T01:00:00+24:00",
  ])("rejects invalid timestamp %s", (created_at) => {
    expect(() => prepare([{ ...comment(), created_at }])).toThrow();
  });
  it("preserves valid nanoseconds above Go zero", () => {
    expect(
      prepare([{ ...comment(), created_at: "0001-01-01T00:00:00.000000001Z" }])
        .comments[0].created_at,
    ).toBe("0001-01-01T00:00:00.000000001Z");
  });
  it("rejects absent/null collection and legacy v3, accepts explicit empty", () => {
    expect(prepare([]).comments).toEqual([]);
    expect(() => prepare(null)).toThrow();
    expect(() => prepare(undefined)).toThrow();
    expect(() =>
      prepareIssueRecovery(
        JSON.stringify({
          ...document([]),
          manifest: "fleet.issue-workspace.v3",
        }),
        offer,
        offer.handle,
        now,
      ),
    ).toThrow();
  });
});
