/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import type { FileEntry, FileReadData } from "@/api/files";
import type { UseFileTreeReturn } from "@/hooks/useFileTree";
import type { UseFileContentReturn } from "@/hooks/useFileContent";

import { FileExplorer } from "../FileExplorer";
import { FileTree } from "../FileTree";
import { FileViewer } from "../FileViewer";

// ---- Mocks ----

vi.mock("@/hooks", () => ({
  useAgentContext: vi.fn(),
}));

vi.mock("@/hooks/useFileTree", () => ({
  useFileTree: vi.fn(),
}));

vi.mock("@/hooks/useFileContent", () => ({
  useFileContent: vi.fn(),
}));

vi.mock("@/components/CodeMirrorEditor", () => ({
  CodeMirrorEditor: ({
    value,
    language,
  }: {
    value: string;
    language?: string;
  }) => (
    <div data-testid="mock-codemirror" data-language={language}>
      {value}
    </div>
  ),
}));

import { useAgentContext } from "@/hooks";
import { useFileTree } from "@/hooks/useFileTree";
import { useFileContent } from "@/hooks/useFileContent";

const mockUseAgents = vi.mocked(useAgentContext);
const mockUseFileTree = vi.mocked(useFileTree);
const mockUseFileContent = vi.mocked(useFileContent);

// ---- Helpers ----

function makeFileEntry(name: string, is_dir = false, size = 100): FileEntry {
  return { name, is_dir, size, mod_time: "2026-01-01T00:00:00Z" };
}

function defaultFileTreeReturn(
  overrides: Partial<UseFileTreeReturn> = {},
): UseFileTreeReturn {
  return {
    expanded: new Set<string>(),
    treeData: new Map<string, FileEntry[]>(),
    selectedPath: null,
    isLoading: false,
    error: null,
    filterText: "",
    debouncedFilterText: "",
    toggle: vi.fn(),
    loadDir: vi.fn(),
    selectFile: vi.fn(),
    setFilterText: vi.fn(),
    ...overrides,
  };
}

function defaultFileContentReturn(
  overrides: Partial<UseFileContentReturn> = {},
): UseFileContentReturn {
  return {
    fileData: null,
    isLoading: false,
    error: null,
    fetchFile: vi.fn(),
    clearFile: vi.fn(),
    ...overrides,
  };
}

function defaultAgentsReturn() {
  return {
    agents: [
      { name: "alpha", branch: "main", status: "ready", ahead: 0, behind: 0 },
      { name: "beta", branch: "main", status: "ready", ahead: 0, behind: 0 },
    ],
    tasks: { total: 0, pending: 0, active: 0, done: 0, failed: 0 },
    taskLists: { pending: [], active: [], done: [], failed: [] },
    agentTasks: {},
    sync: { db: "ok", git: "ok" },
  };
}

// ---- FileExplorer tests ----

describe("FileExplorer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseAgents.mockReturnValue(
      defaultAgentsReturn() as ReturnType<typeof useAgentContext>,
    );
    mockUseFileTree.mockReturnValue(defaultFileTreeReturn());
    mockUseFileContent.mockReturnValue(defaultFileContentReturn());
  });

  it("renders agent selector with agents from useAgents", () => {
    render(<FileExplorer />);
    const select = screen.getByLabelText("Select agent");
    expect(select).toBeInTheDocument();
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
  });

  it("auto-selects first agent on mount", () => {
    render(<FileExplorer />);
    const select = screen.getByLabelText("Select agent") as HTMLSelectElement;
    expect(select.value).toBe("alpha");
  });

  it("shows loading state when isLoading is true", () => {
    mockUseFileTree.mockReturnValue(defaultFileTreeReturn({ isLoading: true }));
    render(<FileExplorer />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("shows error state when error is set", () => {
    mockUseFileTree.mockReturnValue(
      defaultFileTreeReturn({ error: "Connection refused" }),
    );
    render(<FileExplorer />);
    expect(screen.getByText("Connection refused")).toBeInTheDocument();
  });

  it('shows "No agents running" when agents array is empty', () => {
    mockUseAgents.mockReturnValue({
      ...defaultAgentsReturn(),
      agents: [],
    } as ReturnType<typeof useAgents>);
    render(<FileExplorer />);
    expect(screen.getByText("No agents running")).toBeInTheDocument();
  });

  it("calls fetchFile when selectedPath changes", () => {
    const fetchFile = vi.fn();
    mockUseFileTree.mockReturnValue(
      defaultFileTreeReturn({ selectedPath: "src/main.go" }),
    );
    mockUseFileContent.mockReturnValue(defaultFileContentReturn({ fetchFile }));
    render(<FileExplorer />);
    expect(fetchFile).toHaveBeenCalledWith("src/main.go");
  });

  it("closes FileViewer and clears selection on handleClose", () => {
    const selectFile = vi.fn();
    const clearFile = vi.fn();
    mockUseFileTree.mockReturnValue(
      defaultFileTreeReturn({ selectedPath: "src/main.go", selectFile }),
    );
    mockUseFileContent.mockReturnValue(defaultFileContentReturn({ clearFile }));

    render(<FileExplorer />);
    // Click the close button inside FileViewer
    const closeButton = screen.getByLabelText("Close file viewer");
    fireEvent.click(closeButton);

    expect(selectFile).toHaveBeenCalledWith(null);
    expect(clearFile).toHaveBeenCalled();
  });
});

// ---- FileTree tests ----

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

  it('renders root entries from treeData.get("")', () => {
    const treeData = new Map<string, FileEntry[]>([
      [
        "",
        [
          makeFileEntry("main.go"),
          makeFileEntry("go.mod"),
          makeFileEntry("src", true),
        ],
      ],
    ]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.getByLabelText("main.go")).toBeInTheDocument();
    expect(screen.getByLabelText("go.mod")).toBeInTheDocument();
    expect(screen.getByLabelText("src")).toBeInTheDocument();
  });

  it("clicking a directory calls onToggle with full path", () => {
    const onToggle = vi.fn();
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("src", true)]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} onToggle={onToggle} />,
    );
    fireEvent.click(screen.getByLabelText("src"));
    expect(onToggle).toHaveBeenCalledWith("src");
  });

  it("clicking a file calls onSelectFile with full path", () => {
    const onSelectFile = vi.fn();
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(
      <FileTree
        {...defaultProps}
        treeData={treeData}
        onSelectFile={onSelectFile}
      />,
    );
    fireEvent.click(screen.getByLabelText("main.go"));
    expect(onSelectFile).toHaveBeenCalledWith("main.go");
  });

  it("expanded directories show their children", () => {
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

  it("selected file has data-selected attribute", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} selectedPath="main.go" />,
    );
    const node = screen.getByLabelText("main.go");
    expect(node).toHaveAttribute("data-selected");
  });

  it('shows "No files found" when root entries are empty', () => {
    const treeData = new Map<string, FileEntry[]>([["", []]]);
    render(<FileTree {...defaultProps} treeData={treeData} />);
    expect(screen.getByText("No files found")).toBeInTheDocument();
  });

  it("filter text highlights matching portions of file names", () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} filterText="main" />,
    );
    const mark = screen.getByText("main").closest("mark");
    expect(mark).toBeInTheDocument();
  });

  it('shows "No matches" empty state when filter matches nothing', () => {
    const treeData = new Map<string, FileEntry[]>([
      ["", [makeFileEntry("main.go")]],
    ]);
    render(
      <FileTree {...defaultProps} treeData={treeData} filterText="zzzzz" />,
    );
    expect(screen.getByText(/No matches for/)).toBeInTheDocument();
  });
});

// ---- FileViewer tests ----

describe("FileViewer", () => {
  const defaultProps = {
    isOpen: false,
    path: null as string | null,
    fileData: null as FileReadData | null,
    isLoading: false,
    error: null as string | null,
    onClose: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders overlay with visibility hidden when isOpen is false", () => {
    const { container } = render(<FileViewer {...defaultProps} />);
    const overlay = container.firstChild as HTMLElement;
    expect(overlay).toHaveAttribute("aria-hidden", "true");
  });

  it("renders overlay visible when isOpen is true", () => {
    const { container } = render(
      <FileViewer {...defaultProps} isOpen={true} path="src/main.go" />,
    );
    const overlay = container.firstChild as HTMLElement;
    expect(overlay).toHaveAttribute("aria-hidden", "false");
  });

  it("shows file path in header", () => {
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="src/internal/handler.go"
      />,
    );
    expect(screen.getByText("src/internal/handler.go")).toBeInTheDocument();
  });

  it("shows loading state while file is loading", () => {
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        isLoading={true}
      />,
    );
    expect(screen.getByText("Loading file...")).toBeInTheDocument();
  });

  it("shows error state when file fetch fails", () => {
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        error="File not found"
      />,
    );
    expect(screen.getByText("File not found")).toBeInTheDocument();
  });

  it('shows "Binary file" notice for binary files', () => {
    const binaryData: FileReadData = {
      path: "image.png",
      size: 2048,
      binary: true,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="image.png"
        fileData={binaryData}
      />,
    );
    expect(screen.getByText(/Binary file.*cannot display/)).toBeInTheDocument();
  });

  it("renders CodeMirrorEditor with correct language for .go files", async () => {
    const goData: FileReadData = {
      path: "main.go",
      content: "package main\n\nfunc main() {}",
      size: 30,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        fileData={goData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveAttribute("data-language", "go");
    expect(editor).toHaveTextContent("package main");
  });

  it("clicking overlay background calls onClose", () => {
    const onClose = vi.fn();
    const { container } = render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        onClose={onClose}
      />,
    );
    // Click the overlay itself (not the panel inside)
    const overlay = container.firstChild as HTMLElement;
    fireEvent.click(overlay);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("close button calls onClose", () => {
    const onClose = vi.fn();
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        onClose={onClose}
      />,
    );
    fireEvent.click(screen.getByLabelText("Close file viewer"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
