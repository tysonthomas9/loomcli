/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GitDiffViewer and DiffFileRow components.
 *
 * GitDiffViewer: rendering tests for loading, error, binary, too-large, empty,
 * and actual diff content through the open-source renderer.
 * DiffFileRow: status badges, rename display, stats, viewed checkbox, expand
 * chevron, click handlers.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { DiffFile, DiffFilePatch } from "@/api/issues";
import { GitDiffViewer } from "@/components/DiffView";

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

// ============= GitDiffViewer rendering tests =============

describe("GitDiffViewer", () => {
  it("shows loading message when isLoading is true", () => {
    render(<GitDiffViewer patch={null} isLoading={true} />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
  });

  it("shows error message when error is provided", () => {
    render(
      <GitDiffViewer patch={null} isLoading={false} error="Fetch failed" />,
    );
    expect(screen.getByText("Fetch failed")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch is null", () => {
    render(<GitDiffViewer patch={null} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("shows binary file message for binary patches", () => {
    const patch = makePatch({ is_binary: true });
    render(<GitDiffViewer patch={patch} isLoading={false} />);
    expect(
      screen.getByText(/Binary file.*cannot display diff/),
    ).toBeInTheDocument();
  });

  it("shows too-large message for oversized patches", () => {
    const patch = makePatch({ is_too_large: true });
    render(<GitDiffViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("File too large to display")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch string is empty", () => {
    const patch = makePatch({ patch: "" });
    render(<GitDiffViewer patch={patch} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("renders diff lines for a valid patch", async () => {
    const patchStr = [
      "@@ -1,3 +1,3 @@",
      " unchanged",
      "-removed",
      "+added",
    ].join("\n");
    const patch = makePatch({ patch: patchStr });

    const { container } = render(
      <GitDiffViewer filePath="src/main.go" patch={patch} isLoading={false} />,
    );

    await waitFor(() => {
      expect(container).toHaveTextContent("unchanged");
      expect(container).toHaveTextContent("removed");
      expect(container).toHaveTextContent("added");
    });
  });

  it("renders multiple hunks", async () => {
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
      <GitDiffViewer filePath="src/main.go" patch={patch} isLoading={false} />,
    );

    await waitFor(() => {
      expect(container).toHaveTextContent("old1");
      expect(container).toHaveTextContent("new1");
      expect(container).toHaveTextContent("old2");
      expect(container).toHaveTextContent("new2");
    });
  });

  it("error state takes priority over patch content", () => {
    const patch = makePatch({ patch: "@@ -1 +1 @@\n+line" });
    render(
      <GitDiffViewer patch={patch} isLoading={false} error="Something broke" />,
    );
    expect(screen.getByText("Something broke")).toBeInTheDocument();
    expect(screen.queryByText("+line")).not.toBeInTheDocument();
  });

  it("loading state takes priority over everything", () => {
    const patch = makePatch({ patch: "@@ -1 +1 @@\n+line" });
    render(<GitDiffViewer patch={patch} isLoading={true} error="err" />);
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
