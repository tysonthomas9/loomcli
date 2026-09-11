import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { prepareIssueRecovery } from "../issueRecovery";
import type { RecoveryHandle } from "../recoveryHandle";
import { prepareIssueRecoveryTimeline } from "../issueRecoveryTimeline";
const now = Date.parse("2026-09-05T00:00:00Z");
const offer: RecoveryHandle = {
  handle: "A".repeat(43),
  source_identity: "s1.Zml4dHVyZQ",
  workspace: "WS",
  source_repos: [],
  expires_at: "2026-09-05T00:01:00Z",
  manifest: "fleet.issue-workspace.v6",
};
function formatterHistory() {
  return JSON.parse(
    readFileSync(
      new URL(
        "../../../../../../backend/fleet/testdata/issue_recovery_timeline.json",
        import.meta.url,
      ),
      "utf8",
    ),
  );
}
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
function prepare(history: unknown, selected = "WS-1") {
  return prepareIssueRecovery(
    JSON.stringify({
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
    }),
    offer,
    offer.handle,
    now,
    selected,
  );
}
describe("prepared Fleet timeline adapter", () => {
  it("maps actual Fleet formatting without recomputing summaries, retaining immutable scope and record identity", () => {
    const history = formatterHistory();
    const prepared = prepare(history);
    const result = prepareIssueRecoveryTimeline(prepared)!;
    expect(result.events).toHaveLength(history.timeline.length);
    const expected = JSON.parse(
      readFileSync(
        new URL(
          "../../../../../../backend/fleet/testdata/issue_recovery_timeline_expected.json",
          import.meta.url,
        ),
        "utf8",
      ),
    );
    expect(result.events).toEqual(expected);
    for (const [index, event] of result.events.entries()) {
      const native = history.timeline[index];
      expect(event.id).toBe(native.id);
      expect(event.event_type).toBe(native.action);
      expect(event.category).toBe(native.category);
      if (native.summary) expect(event.summary).toBe(native.summary);
      expect(event.created_at).toBe(native.timestamp);
    }
    expect(result.workspace).toBe("WS");
    expect(result.issueId).toBe("WS-1");
    expect(result.present).toBe(true);
    expect(result.hasOlder).toBe(false);
    expect(result.through).toBe(prepared.through);
    expect(result.sourceIdentity).toBe(offer.source_identity);
    expect(Object.isFrozen(result)).toBe(true);
    expect(Object.isFrozen(result.events)).toBe(true);
    expect(Object.isFrozen(result.events[1].changes)).toBe(true);
    expect(Object.isFrozen(result.events[1].changes![0])).toBe(true);
    expect(() => {
      result.events[0].id = "mutated";
    }).toThrow();
  });
  it("matches optional empty-field omission in ordinary Loom JSON", () => {
    const history = formatterHistory();
    history.events = history.events.slice(0, 1);
    history.events[0].metadata = {};
    history.timeline = history.timeline.slice(0, 1);
    Object.assign(history.timeline[0], {
      summary: "",
      changes: [],
      metadata: {},
    });
    const event = prepareIssueRecoveryTimeline(prepare(history))!.events[0];
    expect(event).not.toHaveProperty("summary");
    expect(event).not.toHaveProperty("changes");
    expect(event).not.toHaveProperty("metadata");
    expect(event).not.toHaveProperty("target");
    history.timeline[0].changes = [{ field: "", before: "", after: "" }];
    expect(
      prepareIssueRecoveryTimeline(prepare(history))!.events[0].changes,
    ).toEqual([{ field: "" }]);
  });
  it("compares field ordering by Go UTF8 bytes instead of JS UTF16 code units", () => {
    const history = formatterHistory();
    history.timeline[0].changes = ["", "a", "\uE000", "\u{10000}"].map(
      (field) => ({ field, before: "", after: "" }),
    );
    expect(prepare(history).history?.timeline[0].changes).toHaveLength(4);
    history.timeline[0].changes.reverse();
    expect(() => prepare(history)).toThrow();
  });
  it("rejects a v5 selected document lacking the paired timeline", () => {
    const history = formatterHistory();
    delete history.timeline;
    expect(() => prepare(history)).toThrow();
  });
});
