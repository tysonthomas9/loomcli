/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { DiffFilePatch } from "@/api/issues";

import { DiffFileViewer, parsePatch } from "../DiffFileViewer";

// ---- parsePatch unit tests ----

describe("parsePatch", () => {
  it("parses a simple hunk with additions and deletions", () => {
    const patch = [
      "@@ -1,3 +1,4 @@",
      " context line",
      "-removed line",
      "+added line",
      "+another add",
      " more context",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    const hunk = result.hunks[0];
    // hunk header + 5 content lines
    expect(hunk.lines).toHaveLength(6);
    expect(hunk.lines[0].type).toBe("hunk");
    expect(hunk.lines[1].type).toBe("context");
    expect(hunk.lines[2].type).toBe("del");
    expect(hunk.lines[3].type).toBe("add");
    expect(hunk.lines[4].type).toBe("add");
    expect(hunk.lines[5].type).toBe("context");
  });

  it("parses multiple hunks", () => {
    const patch = [
      "@@ -1,2 +1,2 @@",
      "-old",
      "+new",
      "@@ -10,2 +10,2 @@",
      "-another old",
      "+another new",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(2);
  });

  it("tracks line numbers correctly", () => {
    const patch = ["@@ -5,3 +10,3 @@", " context", "-deleted", "+added"].join(
      "\n",
    );

    const result = parsePatch(patch);
    const lines = result.hunks[0].lines;
    // context line: oldNum=5, newNum=10
    expect(lines[1].oldNum).toBe(5);
    expect(lines[1].newNum).toBe(10);
    // deleted line: oldNum=6
    expect(lines[2].oldNum).toBe(6);
    expect(lines[2].newNum).toBeUndefined();
    // added line: newNum=11
    expect(lines[3].newNum).toBe(11);
    expect(lines[3].oldNum).toBeUndefined();
  });

  it("returns empty hunks for empty input", () => {
    const result = parsePatch("");
    expect(result.hunks).toHaveLength(0);
  });

  it("ignores lines before first hunk header", () => {
    const patch = [
      "diff --git a/file.go b/file.go",
      "index 1234..5678 100644",
      "@@ -1,1 +1,1 @@",
      "-old",
      "+new",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    expect(result.hunks[0].lines).toHaveLength(3);
  });
});

// ---- DiffFileViewer component tests ----

describe("DiffFileViewer", () => {
  it("shows loading state", () => {
    render(<DiffFileViewer patch={null} isLoading={true} />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
  });

  it("shows error message", () => {
    render(
      <DiffFileViewer patch={null} isLoading={false} error="Failed to load" />,
    );
    expect(screen.getByText("Failed to load")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch is null", () => {
    render(<DiffFileViewer patch={null} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("shows binary file message for binary patches", () => {
    const patch: DiffFilePatch = {
      patch: "",
      is_binary: true,
      is_too_large: false,
      additions: 0,
      deletions: 0,
    };
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(
      screen.getByText(/Binary file.*cannot display diff/),
    ).toBeInTheDocument();
  });

  it("shows too large message for oversized patches", () => {
    const patch: DiffFilePatch = {
      patch: "",
      is_binary: false,
      is_too_large: true,
      additions: 0,
      deletions: 0,
    };
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("File too large to display")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch string is empty", () => {
    const patch: DiffFilePatch = {
      patch: "",
      is_binary: false,
      is_too_large: false,
      additions: 0,
      deletions: 0,
    };
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("renders diff lines with correct data-type attributes", () => {
    const patch: DiffFilePatch = {
      patch: "@@ -1,3 +1,3 @@\n context\n-removed\n+added",
      is_binary: false,
      is_too_large: false,
      additions: 1,
      deletions: 1,
    };
    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );
    const lines = container.querySelectorAll("[data-type]");
    const types = Array.from(lines).map((l) => l.getAttribute("data-type"));
    expect(types).toContain("hunk");
    expect(types).toContain("context");
    expect(types).toContain("del");
    expect(types).toContain("add");
  });

  it("renders line numbers for non-hunk lines", () => {
    const patch: DiffFilePatch = {
      patch: "@@ -5,2 +5,2 @@\n context\n-del",
      is_binary: false,
      is_too_large: false,
      additions: 0,
      deletions: 1,
    };
    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );
    // Context line at oldNum=5, newNum=5 renders two <span> line numbers
    const lineNumbers = container.querySelectorAll(
      '[data-type="context"] span',
    );
    expect(lineNumbers.length).toBe(2);
    expect(lineNumbers[0].textContent).toBe("5");
    expect(lineNumbers[1].textContent).toBe("5");
    // Deletion line at oldNum=6 renders one filled and one empty span
    const delLineNumbers = container.querySelectorAll('[data-type="del"] span');
    expect(delLineNumbers[0].textContent).toBe("6");
    expect(delLineNumbers[1].textContent).toBe("");
  });

  it("prioritizes loading over error", () => {
    render(<DiffFileViewer patch={null} isLoading={true} error="Some error" />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
    expect(screen.queryByText("Some error")).not.toBeInTheDocument();
  });

  it("renders pre element wrapping diff content", () => {
    const patch: DiffFilePatch = {
      patch: "@@ -1,1 +1,1 @@\n-old\n+new",
      is_binary: false,
      is_too_large: false,
      additions: 1,
      deletions: 1,
    };
    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );
    expect(container.querySelector("pre")).toBeInTheDocument();
  });
});
