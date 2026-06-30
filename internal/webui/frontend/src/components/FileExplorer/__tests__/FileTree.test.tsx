/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import type { FileEntry } from "@/api/workspace";

import { FileTree } from "../FileTree";

// ---- Helpers ----

function makeFileEntry(name: string, is_dir = false, size = 100): FileEntry {
  return { name, is_dir, size, mod_time: "2026-01-01T00:00:00Z" };
}

// ---- Tests ----

describe("FileTree", () => {
  const defaultProps = {
    treeData: new Map<string, FileEntry[]>(),
    expanded: new Set<string>(),
    selectedPath: null as string | null,
    filterText: "",
    onToggle: vi.fn(),
    onSelectFile: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders with tree role and aria-label", () => {
    render(<FileTree {...defaultProps} />);
    const tree = screen.getByRole("tree");
    expect(tree).toHaveAttribute("aria-label", "File tree");
  });

  it("renders root-level file entries", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go"), makeFileEntry("README.md")]],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.getByLabelText("main.go")).toBeInTheDocument();
    expect(screen.getByLabelText("README.md")).toBeInTheDocument();
  });

  it("renders directory entries with aria-expanded attribute", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    const dir = screen.getByLabelText("src");
    expect(dir).toHaveAttribute("aria-expanded", "false");
  });

  it("does not set aria-expanded on file entries", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    const file = screen.getByLabelText("main.go");
    expect(file).not.toHaveAttribute("aria-expanded");
  });

  it("calls onToggle when directory is clicked", () => {
    const onToggle = vi.fn();
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("pkg", true)]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} onToggle={onToggle} />,
    );
    fireEvent.click(screen.getByLabelText("pkg"));
    expect(onToggle).toHaveBeenCalledWith("pkg");
  });

  it("calls onSelectFile when file is clicked", () => {
    const onSelectFile = vi.fn();
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("go.mod")]],
    ]);
    render(
      <FileTree
        {...defaultProps}
        treeData={treeData}
        onSelectFile={onSelectFile}
      />,
    );
    fireEvent.click(screen.getByLabelText("go.mod"));
    expect(onSelectFile).toHaveBeenCalledWith("go.mod");
  });

  it("shows children of expanded directories", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
      ["src", [makeFileEntry("app.go"), makeFileEntry("util.go")]],
    ]);
    const expanded = new Set(["src"]);
    render(
      <FileTree {...defaultProps} treeData={treeData} expanded={expanded} />,
    );
    expect(screen.getByLabelText("app.go")).toBeInTheDocument();
    expect(screen.getByLabelText("util.go")).toBeInTheDocument();
  });

  it("does not show children of collapsed directories", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
      ["src", [makeFileEntry("hidden.go")]],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.queryByLabelText("hidden.go")).not.toBeInTheDocument();
  });

  it("marks selected file with data-selected attribute", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go"), makeFileEntry("other.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} selectedPath="main.go" />,
    );
    expect(screen.getByLabelText("main.go")).toHaveAttribute("data-selected");
    expect(screen.getByLabelText("other.go")).not.toHaveAttribute(
      "data-selected",
    );
  });

  it("shows 'No files found' when root entries are empty and no filter", () => {
    const treeData = new Map<string, FileEntry[]>([["", []]]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.getByText("No files found")).toBeInTheDocument();
  });

  it("shows 'No files found' when treeData has no root key (defaults to empty)", () => {
    // treeData.get("") returns undefined, which falls back to [] via ?? operator
    // rootEntries.length === 0 && !filterText => shows empty message
    render(<FileTree {...defaultProps} />);
    expect(screen.getByText("No files found")).toBeInTheDocument();
  });

  it("highlights matching text in file names when filterText is set", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("handler.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} filterText="hand" />,
    );
    const mark = screen.getByText("hand").closest("mark");
    expect(mark).toBeInTheDocument();
  });

  it("filters out non-matching entries", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go"), makeFileEntry("settings.yml")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} filterText="settings" />,
    );
    expect(screen.queryByLabelText("main.go")).not.toBeInTheDocument();
    expect(screen.getByLabelText("settings.yml")).toBeInTheDocument();
  });

  it("shows 'No matches' message when filter matches nothing", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(
      <FileTree
        {...defaultProps}
        treeData={treeData}
        filterText="nonexistent"
      />,
    );
    expect(
      screen.getByText(/No matches in loaded folders for/),
    ).toBeInTheDocument();
  });

  it("shows directory if a child matches the filter", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
      ["src", [makeFileEntry("match.go")]],
    ]);
    const expanded = new Set(["src"]);
    render(
      <FileTree
        {...defaultProps}
        treeData={treeData}
        expanded={expanded}
        filterText="match"
      />,
    );
    expect(screen.getByLabelText("src")).toBeInTheDocument();
    expect(screen.getByLabelText("match.go")).toBeInTheDocument();
  });

  it("renders nested directories with correct depth indentation", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
      ["src", [makeFileEntry("internal", true)]],
      ["src/internal", [makeFileEntry("deep.go")]],
    ]);
    const expanded = new Set(["src", "src/internal"]);
    render(
      <FileTree {...defaultProps} treeData={treeData} expanded={expanded} />,
    );
    const deep = screen.getByLabelText("deep.go");
    // depth=2 => paddingLeft = 8 + 2*16 = 40
    expect(deep).toHaveStyle({ paddingLeft: "40px" });
  });

  it("sets data-dir attribute on directory nodes", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("pkg", true), makeFileEntry("file.txt")]],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.getByLabelText("pkg")).toHaveAttribute("data-dir");
    expect(screen.getByLabelText("file.txt")).not.toHaveAttribute("data-dir");
  });

  it("case-insensitive filter matching", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("Handler.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} filterText="handler" />,
    );
    expect(screen.getByLabelText("Handler.go")).toBeInTheDocument();
  });

  describe("keyboard navigation", () => {
    const rootTree = new Map<string, FileEntry[]>([
      [
        "",
        [
          makeFileEntry("alpha.go"),
          makeFileEntry("beta.go"),
          makeFileEntry("gamma.go"),
        ],
      ],
    ]);

    it("defaults the active descendant to the first node", () => {
      render(<FileTree {...defaultProps} treeData={rootTree} />);
      expect(screen.getByRole("tree")).toHaveAttribute(
        "aria-activedescendant",
        "ft-alpha.go",
      );
    });

    it("ArrowDown / ArrowUp move the active node", () => {
      render(<FileTree {...defaultProps} treeData={rootTree} />);
      const tree = screen.getByRole("tree");
      fireEvent.keyDown(tree, { key: "ArrowDown" });
      expect(tree).toHaveAttribute("aria-activedescendant", "ft-beta.go");
      fireEvent.keyDown(tree, { key: "ArrowUp" });
      expect(tree).toHaveAttribute("aria-activedescendant", "ft-alpha.go");
    });

    it("Enter activates the focused file", () => {
      const onSelectFile = vi.fn();
      render(
        <FileTree
          {...defaultProps}
          treeData={rootTree}
          onSelectFile={onSelectFile}
        />,
      );
      const tree = screen.getByRole("tree");
      fireEvent.keyDown(tree, { key: "ArrowDown" }); // → beta.go
      fireEvent.keyDown(tree, { key: "Enter" });
      expect(onSelectFile).toHaveBeenCalledWith("beta.go");
    });

    it("ArrowRight expands a collapsed directory", () => {
      const onToggle = vi.fn();
      const treeData = new Map<string, FileEntry[]>([
        ["", [makeFileEntry("pkg", true)]],
      ]);
      render(
        <FileTree {...defaultProps} treeData={treeData} onToggle={onToggle} />,
      );
      fireEvent.keyDown(screen.getByRole("tree"), { key: "ArrowRight" });
      expect(onToggle).toHaveBeenCalledWith("pkg");
    });

    it("type-ahead jumps to the next matching node", () => {
      render(<FileTree {...defaultProps} treeData={rootTree} />);
      const tree = screen.getByRole("tree");
      fireEvent.keyDown(tree, { key: "g" });
      expect(tree).toHaveAttribute("aria-activedescendant", "ft-gamma.go");
    });
  });
});
