import { describe, expect, it } from "vitest";

import type { FileHistoryEntry } from "@/api/workspace";

import { buildHistoryTimeline } from "../historyTimeline";

function commit(id: string): FileHistoryEntry {
  return {
    kind: "commit",
    sha: id,
    author: "Test User",
    time: `2026-01-01T00:0${id.length}:00Z`,
    summary: `commit ${id}`,
  };
}

function save(id: string): FileHistoryEntry {
  return {
    kind: "save",
    id,
    author: "browser",
    time: `2026-01-01T00:0${id.length}:30Z`,
    summary: "Browser save",
    content: `${id}\n`,
  };
}

describe("historyTimeline", () => {
  it.each([
    {
      name: "leaves single saves as individual rows",
      entries: [save("a")],
      expected: ["save:1"],
    },
    {
      name: "clusters consecutive saves",
      entries: [save("a"), save("bb"), save("ccc")],
      expected: ["save-cluster:3"],
    },
    {
      name: "commits break save clusters",
      entries: [save("a"), save("bb"), commit("abc"), save("ccc")],
      expected: ["save-cluster:2", "commit:1", "save:1"],
    },
    {
      name: "preserves commit order around clusters",
      entries: [commit("abc"), save("a"), save("bb"), commit("def")],
      expected: ["commit:1", "save-cluster:2", "commit:1"],
    },
  ])("$name", ({ entries, expected }) => {
    expect(
      buildHistoryTimeline(entries).map((node) =>
        node.kind === "save-cluster"
          ? `${node.kind}:${node.entries.length}`
          : `${node.kind}:1`,
      ),
    ).toEqual(expected);
  });
});
