import { describe, expect, it } from "vitest";

import type { KanbanColumnConfig } from "../types";
import { visibleKanbanColumns } from "../columnVisibility";

const columns: KanbanColumnConfig[] = [
  { id: "backlog", label: "Backlog", filter: () => false },
  { id: "open", label: "Open", filter: () => true },
  { id: "blocked", label: "Blocked", filter: () => false },
];

describe("visibleKanbanColumns", () => {
  it("returns all columns when compact mode is off", () => {
    const issuesByColumn = new Map([
      ["backlog", []],
      ["open", [{}]],
      ["blocked", []],
    ]);

    expect(visibleKanbanColumns(columns, issuesByColumn, false)).toEqual(
      columns,
    );
  });

  it("returns only columns with issues when compact mode is on", () => {
    const issuesByColumn = new Map([
      ["backlog", []],
      ["open", [{}]],
      ["blocked", []],
    ]);

    expect(visibleKanbanColumns(columns, issuesByColumn, true)).toEqual([
      columns[1],
    ]);
  });

  it("falls back to all columns when every column is empty", () => {
    const issuesByColumn = new Map([
      ["backlog", []],
      ["open", []],
      ["blocked", []],
    ]);

    expect(visibleKanbanColumns(columns, issuesByColumn, true)).toEqual(
      columns,
    );
  });
});
