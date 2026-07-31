/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import type { DiffFile } from "@/api/issues";

import { DiffFileRow } from "../DiffFileRow";

// ---- Helpers ----

function makeDiffFile(overrides: Partial<DiffFile> = {}): DiffFile {
  return {
    path: "src/main.go",
    status: "M",
    additions: 10,
    deletions: 5,
    ...overrides,
  };
}

// ---- Tests ----

describe("DiffFileRow", () => {
  const defaultProps = {
    file: makeDiffFile(),
    isExpanded: false,
    isViewed: false,
    onToggleExpand: vi.fn(),
    onToggleViewed: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  function getRowButton(): HTMLElement {
    const row = screen.getByText("src/main.go").closest('[role="button"]');
    expect(row).toBeInTheDocument();
    return row as HTMLElement;
  }

  it("renders the file path", () => {
    render(<DiffFileRow {...defaultProps} />);
    expect(screen.getByText("src/main.go")).toBeInTheDocument();
  });

  it("renders the status badge with status letter", () => {
    render(<DiffFileRow {...defaultProps} />);
    expect(screen.getByText("M")).toBeInTheDocument();
  });

  it("renders status badge with data-status attribute", () => {
    render(<DiffFileRow {...defaultProps} />);
    const badge = screen.getByText("M");
    expect(badge).toHaveAttribute("data-status", "M");
  });

  it("renders added file status", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ status: "A", path: "new-file.go" })}
      />,
    );
    expect(screen.getByText("A")).toHaveAttribute("data-status", "A");
    expect(screen.getByText("new-file.go")).toBeInTheDocument();
  });

  it("renders deleted file status", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ status: "D", path: "removed.go" })}
      />,
    );
    expect(screen.getByText("D")).toHaveAttribute("data-status", "D");
  });

  it("renders rename path with arrow notation", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({
          status: "R",
          old_path: "old/path.go",
          path: "new/path.go",
        })}
      />,
    );
    expect(screen.getByTitle("old/path.go → new/path.go")).toBeInTheDocument();
  });

  it("shows addition count with + prefix", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ additions: 25, deletions: 0 })}
      />,
    );
    expect(screen.getByText("+25")).toBeInTheDocument();
  });

  it("shows deletion count with - prefix", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ additions: 0, deletions: 12 })}
      />,
    );
    expect(screen.getByText("-12")).toBeInTheDocument();
  });

  it("hides addition count when zero", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ additions: 0, deletions: 5 })}
      />,
    );
    expect(screen.queryByText("+0")).not.toBeInTheDocument();
  });

  it("hides deletion count when zero", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ additions: 10, deletions: 0 })}
      />,
    );
    expect(screen.queryByText("-0")).not.toBeInTheDocument();
  });

  it("calls onToggleExpand when row is clicked", () => {
    const onToggleExpand = vi.fn();
    render(<DiffFileRow {...defaultProps} onToggleExpand={onToggleExpand} />);
    fireEvent.click(getRowButton());
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
  });

  it("calls onToggleExpand on Enter key", () => {
    const onToggleExpand = vi.fn();
    render(<DiffFileRow {...defaultProps} onToggleExpand={onToggleExpand} />);
    fireEvent.keyDown(getRowButton(), { key: "Enter" });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
  });

  it("calls onToggleExpand on Space key", () => {
    const onToggleExpand = vi.fn();
    render(<DiffFileRow {...defaultProps} onToggleExpand={onToggleExpand} />);
    fireEvent.keyDown(getRowButton(), { key: " " });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
  });

  it("renders viewed control unpressed by default", () => {
    render(<DiffFileRow {...defaultProps} />);
    const viewedButton = screen.getByRole("button", {
      name: "Mark src/main.go as viewed",
    });
    expect(viewedButton).toHaveAttribute("aria-pressed", "false");
  });

  it("renders viewed control pressed when isViewed is true", () => {
    render(<DiffFileRow {...defaultProps} isViewed={true} />);
    const viewedButton = screen.getByRole("button", {
      name: "Unmark src/main.go as viewed",
    });
    expect(viewedButton).toHaveAttribute("aria-pressed", "true");
  });

  it("calls onToggleViewed when viewed control is clicked", () => {
    const onToggleViewed = vi.fn();
    render(<DiffFileRow {...defaultProps} onToggleViewed={onToggleViewed} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Mark src/main.go as viewed" }),
    );
    expect(onToggleViewed).toHaveBeenCalledTimes(1);
  });

  it("viewed control click does not propagate to row click handler", () => {
    const onToggleExpand = vi.fn();
    const onToggleViewed = vi.fn();
    render(
      <DiffFileRow
        {...defaultProps}
        onToggleExpand={onToggleExpand}
        onToggleViewed={onToggleViewed}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Mark src/main.go as viewed" }),
    );
    expect(onToggleViewed).toHaveBeenCalledTimes(1);
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it("has data-expanded attribute on chevron when expanded", () => {
    const { container } = render(
      <DiffFileRow {...defaultProps} isExpanded={true} />,
    );
    const chevron = container.querySelector("svg[data-expanded]");
    expect(chevron).not.toBeNull();
    expect(chevron!.getAttribute("data-expanded")).toBe("true");
  });

  it("has data-expanded false on chevron when collapsed", () => {
    const { container } = render(
      <DiffFileRow {...defaultProps} isExpanded={false} />,
    );
    const chevron = container.querySelector("svg[data-expanded]");
    expect(chevron).not.toBeNull();
    expect(chevron!.getAttribute("data-expanded")).toBe("false");
  });

  it("has role button and tabIndex 0", () => {
    render(<DiffFileRow {...defaultProps} />);
    const row = getRowButton();
    expect(row).toHaveAttribute("tabIndex", "0");
  });

  it("displays both additions and deletions when both are non-zero", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ additions: 15, deletions: 8 })}
      />,
    );
    expect(screen.getByText("+15")).toBeInTheDocument();
    expect(screen.getByText("-8")).toBeInTheDocument();
  });

  it("renders viewed control with correct aria-label including file path", () => {
    render(
      <DiffFileRow
        {...defaultProps}
        file={makeDiffFile({ path: "pkg/handler.go" })}
      />,
    );
    expect(
      screen.getByLabelText("Mark pkg/handler.go as viewed"),
    ).toBeInTheDocument();
  });
});
