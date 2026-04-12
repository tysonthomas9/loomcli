/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DiffFileViewer (including parsePatch) and DiffFileRow components.
 *
 * parsePatch: pure function tests for hunk parsing, line numbering, multi-hunk patches.
 * DiffFileViewer: rendering tests for loading, error, binary, too-large, empty, and actual diff.
 * DiffFileRow: status badges, rename display, stats, viewed checkbox, expand chevron, click handlers.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { DiffFile, DiffFilePatch } from "@/api/issues";

import { parsePatch, DiffFileViewer } from "./DiffFileViewer";
import { DiffFileRow } from "./DiffFileRow";

// ============= Helpers =============

function makeFile(overrides: Partial<DiffFile> = {}): DiffFile {
  return {
    path: "src/main.go",
    status: "M",
    additions: 10,
    deletions: 5,
    ...overrides,
  };
}

function makePatch(overrides: Partial<DiffFilePatch> = {}): DiffFilePatch {
  return {
    patch: "",
    is_binary: false,
    is_too_large: false,
    additions: 0,
    deletions: 0,
    ...overrides,
  };
}

// ============= parsePatch tests =============

describe("parsePatch", () => {
  it("returns empty hunks for an empty string", () => {
    const result = parsePatch("");
    expect(result.hunks).toHaveLength(0);
  });

  it("returns empty hunks when there are no @@ headers", () => {
    const result = parsePatch("some random text\nanother line");
    expect(result.hunks).toHaveLength(0);
  });

  it("parses a single hunk with adds, deletes, and context", () => {
    const patch = [
      "@@ -10,4 +10,5 @@ func main() {",
      " context line",
      "-deleted line",
      "+added line",
      "+another added",
      " more context",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    // First line is the hunk header itself
    expect(lines[0]).toEqual({
      type: "hunk",
      content: "@@ -10,4 +10,5 @@ func main() {",
    });
    // Context line
    expect(lines[1]).toEqual({
      type: "context",
      content: " context line",
      oldNum: 10,
      newNum: 10,
    });
    // Deleted line
    expect(lines[2]).toEqual({
      type: "del",
      content: "-deleted line",
      oldNum: 11,
    });
    // Added lines
    expect(lines[3]).toEqual({
      type: "add",
      content: "+added line",
      newNum: 11,
    });
    expect(lines[4]).toEqual({
      type: "add",
      content: "+another added",
      newNum: 12,
    });
    // Context after
    expect(lines[5]).toEqual({
      type: "context",
      content: " more context",
      oldNum: 12,
      newNum: 13,
    });
  });

  it("parses multiple hunks", () => {
    const patch = [
      "@@ -1,3 +1,3 @@",
      " line1",
      "-old",
      "+new",
      "@@ -20,2 +20,3 @@",
      " existing",
      "+inserted",
      " after",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(2);
    expect(result.hunks[0].header).toBe("@@ -1,3 +1,3 @@");
    expect(result.hunks[1].header).toBe("@@ -20,2 +20,3 @@");

    // Second hunk should reset line numbers
    const hunk2Lines = result.hunks[1].lines;
    expect(hunk2Lines[1]).toMatchObject({
      type: "context",
      oldNum: 20,
      newNum: 20,
    });
    expect(hunk2Lines[2]).toMatchObject({
      type: "add",
      newNum: 21,
    });
    expect(hunk2Lines[3]).toMatchObject({
      type: "context",
      oldNum: 21,
      newNum: 22,
    });
  });

  it("handles hunk header without comma counts", () => {
    // Single-line range: @@ -5 +5 @@
    const patch = "@@ -5 +7 @@\n+added";

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    const lines = result.hunks[0].lines;
    expect(lines[1]).toEqual({ type: "add", content: "+added", newNum: 7 });
  });

  it("tracks line numbers correctly through consecutive deletes and adds", () => {
    const patch = [
      "@@ -1,4 +1,4 @@",
      "-a",
      "-b",
      "+c",
      "+d",
      " same",
      " same2",
    ].join("\n");

    const result = parsePatch(patch);
    const lines = result.hunks[0].lines;

    // After hunk header at [0]:
    // del at oldNum 1
    expect(lines[1]).toMatchObject({ type: "del", oldNum: 1 });
    // del at oldNum 2
    expect(lines[2]).toMatchObject({ type: "del", oldNum: 2 });
    // add at newNum 1
    expect(lines[3]).toMatchObject({ type: "add", newNum: 1 });
    // add at newNum 2
    expect(lines[4]).toMatchObject({ type: "add", newNum: 2 });
    // context: oldNum 3, newNum 3
    expect(lines[5]).toMatchObject({ type: "context", oldNum: 3, newNum: 3 });
    expect(lines[6]).toMatchObject({ type: "context", oldNum: 4, newNum: 4 });
  });

  it("ignores lines before the first hunk header", () => {
    const patch = [
      "diff --git a/file.go b/file.go",
      "index abc..def 100644",
      "--- a/file.go",
      "+++ b/file.go",
      "@@ -1,2 +1,2 @@",
      "-old",
      "+new",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    // Only hunk header + 2 content lines
    expect(result.hunks[0].lines).toHaveLength(3);
  });

  it("does not create a new hunk for embedded @@ in file content", () => {
    const patch = [
      "@@ -1,4 +1,4 @@",
      " normal line",
      "@@ embedded but not a hunk header @@",
      "-old line",
      "+new line",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    expect(lines).toHaveLength(5);

    expect(lines[1]).toMatchObject({ type: "context", oldNum: 1, newNum: 1 });
    expect(lines[2]).toMatchObject({ type: "context", oldNum: 2, newNum: 2 });
    expect(lines[3]).toMatchObject({ type: "del", oldNum: 3 });
    expect(lines[4]).toMatchObject({ type: "add", newNum: 3 });
  });

  it("treats raw @@ at column 0 as context when it doesn't match hunk format", () => {
    const patch = [
      "@@ -1,3 +1,3 @@",
      " first",
      "@@ not a valid hunk header",
      " last",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    expect(lines).toHaveLength(4);
    expect(lines[1]).toMatchObject({ type: "context", oldNum: 1, newNum: 1 });
    expect(lines[2]).toMatchObject({ type: "context", oldNum: 2, newNum: 2 });
    expect(lines[3]).toMatchObject({ type: "context", oldNum: 3, newNum: 3 });
  });

  it("stores the full hunk header including trailing context", () => {
    const header = "@@ -100,6 +110,8 @@ func (s *Server) handleRequest()";
    const patch = `${header}\n context`;
    const result = parsePatch(patch);
    expect(result.hunks[0].header).toBe(header);
  });
});

// ============= DiffFileViewer rendering tests =============

describe("DiffFileViewer", () => {
  it("shows loading message when isLoading is true", () => {
    render(<DiffFileViewer patch={null} isLoading={true} />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
  });

  it("shows error message when error is provided", () => {
    render(
      <DiffFileViewer patch={null} isLoading={false} error="Fetch failed" />,
    );
    expect(screen.getByText("Fetch failed")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch is null", () => {
    render(<DiffFileViewer patch={null} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("shows binary file message for binary patches", () => {
    const patch = makePatch({ is_binary: true });
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(
      screen.getByText(/Binary file.*cannot display diff/),
    ).toBeInTheDocument();
  });

  it("shows too-large message for oversized patches", () => {
    const patch = makePatch({ is_too_large: true });
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("File too large to display")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch string is empty", () => {
    const patch = makePatch({ patch: "" });
    render(<DiffFileViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("renders diff lines for a valid patch", () => {
    const patchStr = [
      "@@ -1,3 +1,3 @@",
      " unchanged",
      "-removed",
      "+added",
    ].join("\n");
    const patch = makePatch({ patch: patchStr });

    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );

    // Hunk header should be rendered
    expect(screen.getByText("@@ -1,3 +1,3 @@")).toBeInTheDocument();

    // Content lines are rendered inside divs with data-type attributes.
    // Non-hunk lines have line number spans as siblings, so we check via
    // the data-type selector and textContent which includes child text.
    const contextLine = container.querySelector('[data-type="context"]');
    expect(contextLine).toBeTruthy();
    expect(contextLine!.textContent).toContain(" unchanged");

    const delLine = container.querySelector('[data-type="del"]');
    expect(delLine).toBeTruthy();
    expect(delLine!.textContent).toContain("-removed");

    const addLine = container.querySelector('[data-type="add"]');
    expect(addLine).toBeTruthy();
    expect(addLine!.textContent).toContain("+added");
  });

  it("renders correct data-type attributes on diff lines", () => {
    const patchStr = ["@@ -1,3 +1,3 @@", " ctx", "-del", "+add"].join("\n");
    const patch = makePatch({ patch: patchStr });

    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );

    const lines = container.querySelectorAll("[data-type]");
    const types = Array.from(lines).map((el) => el.getAttribute("data-type"));
    expect(types).toEqual(["hunk", "context", "del", "add"]);
  });

  it("renders line numbers for context lines", () => {
    const patchStr = "@@ -5,1 +8,1 @@\n same";
    const patch = makePatch({ patch: patchStr });

    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );

    // The context line at oldNum=5, newNum=8
    const contextLine = container.querySelector('[data-type="context"]');
    expect(contextLine).toBeTruthy();
    // Should contain line number spans with 5 and 8
    const spans = contextLine!.querySelectorAll("span");
    expect(spans[0].textContent).toBe("5");
    expect(spans[1].textContent).toBe("8");
  });

  it("renders multiple hunks", () => {
    const patchStr = [
      "@@ -1,1 +1,1 @@",
      "-old1",
      "+new1",
      "@@ -50,1 +50,1 @@",
      "-old2",
      "+new2",
    ].join("\n");
    const patch = makePatch({ patch: patchStr });

    const { container } = render(
      <DiffFileViewer patch={patch} isLoading={false} />,
    );

    // Both hunk headers rendered
    expect(screen.getByText("@@ -1,1 +1,1 @@")).toBeInTheDocument();
    expect(screen.getByText("@@ -50,1 +50,1 @@")).toBeInTheDocument();

    // Content lines (text split across spans + text node, so use textContent)
    const delLines = container.querySelectorAll('[data-type="del"]');
    const addLines = container.querySelectorAll('[data-type="add"]');
    expect(delLines).toHaveLength(2);
    expect(addLines).toHaveLength(2);
    expect(delLines[0].textContent).toContain("-old1");
    expect(addLines[0].textContent).toContain("+new1");
    expect(delLines[1].textContent).toContain("-old2");
    expect(addLines[1].textContent).toContain("+new2");
  });

  it("error state takes priority over patch content", () => {
    const patch = makePatch({ patch: "@@ -1 +1 @@\n+line" });
    render(
      <DiffFileViewer
        patch={patch}
        isLoading={false}
        error="Something broke"
      />,
    );
    expect(screen.getByText("Something broke")).toBeInTheDocument();
    expect(screen.queryByText("+line")).not.toBeInTheDocument();
  });

  it("loading state takes priority over everything", () => {
    const patch = makePatch({ patch: "@@ -1 +1 @@\n+line" });
    render(<DiffFileViewer patch={patch} isLoading={true} error="err" />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
    expect(screen.queryByText("err")).not.toBeInTheDocument();
    expect(screen.queryByText("+line")).not.toBeInTheDocument();
  });
});

// ============= DiffFileRow rendering tests =============

describe("DiffFileRow", () => {
  const defaultProps = {
    file: makeFile(),
    isExpanded: false,
    isViewed: false,
    onToggleExpand: vi.fn(),
    onToggleViewed: vi.fn(),
  };

  function renderRow(overrides: Partial<typeof defaultProps> = {}) {
    const props = { ...defaultProps, ...overrides };
    // Reset mocks for fresh assertions
    if (props.onToggleExpand === defaultProps.onToggleExpand) {
      defaultProps.onToggleExpand.mockReset();
    }
    if (props.onToggleViewed === defaultProps.onToggleViewed) {
      defaultProps.onToggleViewed.mockReset();
    }
    return render(<DiffFileRow {...props} />);
  }

  describe("status badge", () => {
    it("renders the file status text", () => {
      renderRow({ file: makeFile({ status: "M" }) });
      expect(screen.getByText("M")).toBeInTheDocument();
    });

    it("sets data-status attribute on the badge", () => {
      renderRow({ file: makeFile({ status: "A" }) });
      const badge = screen.getByText("A");
      expect(badge).toHaveAttribute("data-status", "A");
    });

    it.each(["M", "A", "D", "R"] as const)(
      "renders status badge for %s",
      (status) => {
        renderRow({ file: makeFile({ status }) });
        const badge = screen.getByText(status);
        expect(badge).toHaveAttribute("data-status", status);
      },
    );
  });

  describe("file path display", () => {
    it("shows the file path", () => {
      renderRow({ file: makeFile({ path: "pkg/util/helper.go" }) });
      expect(screen.getByText("pkg/util/helper.go")).toBeInTheDocument();
    });

    it("shows rename path with arrow for R status", () => {
      renderRow({
        file: makeFile({
          status: "R",
          old_path: "old/path.go",
          path: "new/path.go",
        }),
      });
      expect(screen.getByText("old/path.go → new/path.go")).toBeInTheDocument();
    });

    it("shows just the path when status is R but old_path is missing", () => {
      renderRow({
        file: makeFile({ status: "R", path: "new/path.go" }),
      });
      expect(screen.getByText("new/path.go")).toBeInTheDocument();
      expect(screen.queryByText(/→/)).not.toBeInTheDocument();
    });

    it("sets title attribute to the display path", () => {
      renderRow({ file: makeFile({ path: "src/foo.go" }) });
      const pathEl = screen.getByText("src/foo.go");
      expect(pathEl).toHaveAttribute("title", "src/foo.go");
    });

    it("sets title attribute to rename display path", () => {
      renderRow({
        file: makeFile({
          status: "R",
          old_path: "a.go",
          path: "b.go",
        }),
      });
      const pathEl = screen.getByText("a.go → b.go");
      expect(pathEl).toHaveAttribute("title", "a.go → b.go");
    });
  });

  describe("stats", () => {
    it("shows additions when > 0", () => {
      renderRow({ file: makeFile({ additions: 15, deletions: 0 }) });
      expect(screen.getByText("+15")).toBeInTheDocument();
    });

    it("shows deletions when > 0", () => {
      renderRow({ file: makeFile({ additions: 0, deletions: 8 }) });
      expect(screen.getByText("-8")).toBeInTheDocument();
    });

    it("shows both additions and deletions", () => {
      renderRow({ file: makeFile({ additions: 10, deletions: 5 }) });
      expect(screen.getByText("+10")).toBeInTheDocument();
      expect(screen.getByText("-5")).toBeInTheDocument();
    });

    it("hides additions pill when additions is 0", () => {
      renderRow({ file: makeFile({ additions: 0, deletions: 3 }) });
      expect(screen.queryByText("+0")).not.toBeInTheDocument();
    });

    it("hides deletions pill when deletions is 0", () => {
      renderRow({ file: makeFile({ additions: 3, deletions: 0 }) });
      expect(screen.queryByText("-0")).not.toBeInTheDocument();
    });
  });

  describe("viewed checkbox", () => {
    it("renders unchecked when isViewed is false", () => {
      renderRow({ isViewed: false });
      const checkbox = screen.getByRole("checkbox");
      expect(checkbox).not.toBeChecked();
    });

    it("renders checked when isViewed is true", () => {
      renderRow({ isViewed: true });
      const checkbox = screen.getByRole("checkbox");
      expect(checkbox).toBeChecked();
    });

    it("calls onToggleViewed when checkbox changes", () => {
      const onToggleViewed = vi.fn();
      renderRow({ onToggleViewed });
      fireEvent.click(screen.getByRole("checkbox"));
      expect(onToggleViewed).toHaveBeenCalledTimes(1);
    });

    it("does not call onToggleExpand when checkbox is clicked", () => {
      const onToggleExpand = vi.fn();
      const onToggleViewed = vi.fn();
      renderRow({ onToggleExpand, onToggleViewed });
      fireEvent.click(screen.getByRole("checkbox"));
      expect(onToggleExpand).not.toHaveBeenCalled();
      expect(onToggleViewed).toHaveBeenCalledTimes(1);
    });

    it("has correct aria-label", () => {
      renderRow({ file: makeFile({ path: "src/app.go" }) });
      const checkbox = screen.getByRole("checkbox");
      expect(checkbox).toHaveAttribute(
        "aria-label",
        "Mark src/app.go as viewed",
      );
    });
  });

  describe("expand chevron", () => {
    it("sets data-expanded=false when not expanded", () => {
      const { container } = renderRow({ isExpanded: false });
      const svg = container.querySelector("svg");
      expect(svg).toHaveAttribute("data-expanded", "false");
    });

    it("sets data-expanded=true when expanded", () => {
      const { container } = renderRow({ isExpanded: true });
      const svg = container.querySelector("svg");
      expect(svg).toHaveAttribute("data-expanded", "true");
    });
  });

  describe("click handlers", () => {
    it("calls onToggleExpand when row is clicked", () => {
      const onToggleExpand = vi.fn();
      renderRow({ onToggleExpand });
      fireEvent.click(screen.getByRole("button"));
      expect(onToggleExpand).toHaveBeenCalledTimes(1);
    });

    it("calls onToggleExpand on Enter key", () => {
      const onToggleExpand = vi.fn();
      renderRow({ onToggleExpand });
      fireEvent.keyDown(screen.getByRole("button"), { key: "Enter" });
      expect(onToggleExpand).toHaveBeenCalledTimes(1);
    });

    it("calls onToggleExpand on Space key", () => {
      const onToggleExpand = vi.fn();
      renderRow({ onToggleExpand });
      fireEvent.keyDown(screen.getByRole("button"), { key: " " });
      expect(onToggleExpand).toHaveBeenCalledTimes(1);
    });

    it("does not call onToggleExpand on other keys", () => {
      const onToggleExpand = vi.fn();
      renderRow({ onToggleExpand });
      fireEvent.keyDown(screen.getByRole("button"), { key: "Tab" });
      expect(onToggleExpand).not.toHaveBeenCalled();
    });

    it("has tabIndex 0 for keyboard accessibility", () => {
      renderRow();
      expect(screen.getByRole("button")).toHaveAttribute("tabIndex", "0");
    });
  });
});
