/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DiffTab component.
 * Covers summary bar, file list, expand/collapse, viewed toggle,
 * loading/error/empty states, and hook invocation.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { DiffFile, DiffFilePatch } from "@/api/issues";
import type { LoomAgentStatus } from "@/types";
import type { UseDiffReturn } from "@/hooks/terminal";

import { DiffTab } from "./DiffTab";

// ============= Mocks =============

let lastUseDiffOptions: {
  agentName: string | null;
  enabled: boolean;
  commitSignal?: number;
};
let mockUseDiffReturn: UseDiffReturn;

const mockFetchPatch = vi.fn();
const mockMarkViewed = vi.fn();

vi.mock("@/hooks/terminal", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/terminal")>(
      "@/hooks/terminal",
    );
  return {
    ...actual,
    useDiff: (opts: {
      agentName: string | null;
      enabled: boolean;
      commitSignal?: number;
    }) => {
      lastUseDiffOptions = opts;
      return mockUseDiffReturn;
    },
  };
});

// Mock sub-components to isolate DiffTab orchestration
vi.mock("./DiffFileRow", () => ({
  DiffFileRow: (props: {
    file: DiffFile;
    isExpanded: boolean;
    isViewed: boolean;
    onToggleExpand: () => void;
    onToggleViewed: () => void;
  }) => (
    <div
      data-testid="file-row"
      data-path={props.file.path}
      data-expanded={props.isExpanded}
      data-viewed={props.isViewed}
    >
      <button
        data-testid={`expand-${props.file.path}`}
        onClick={props.onToggleExpand}
      >
        expand
      </button>
      <button
        data-testid={`viewed-${props.file.path}`}
        onClick={props.onToggleViewed}
      >
        viewed
      </button>
    </div>
  ),
}));

vi.mock("./DiffFileViewer", () => ({
  DiffFileViewer: (props: {
    patch: DiffFilePatch | null;
    isLoading: boolean;
    error?: string;
  }) => (
    <div
      data-testid="file-viewer"
      data-loading={props.isLoading}
      data-has-patch={props.patch !== null}
      data-error={props.error ?? ""}
    />
  ),
}));

// ============= Helpers =============

function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "ember",
    branch: "feature-x",
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

function makeFile(overrides: Partial<DiffFile> = {}): DiffFile {
  return {
    path: "src/main.go",
    status: "M",
    additions: 10,
    deletions: 5,
    ...overrides,
  };
}

function resetMocks() {
  mockFetchPatch.mockReset();
  mockMarkViewed.mockReset();
  mockUseDiffReturn = {
    files: [],
    isLoading: false,
    error: null,
    patchErrors: new Map(),
    viewedFiles: new Set(),
    markViewed: mockMarkViewed,
    patchCache: new Map(),
    fetchPatch: mockFetchPatch,
    summaryStats: { filesChanged: 0, additions: 0, deletions: 0 },
  };
}

async function renderDiffTab(agent: LoomAgentStatus, isActive?: boolean) {
  let result: ReturnType<typeof render>;
  await act(async () => {
    result = render(<DiffTab agent={agent} isActive={isActive} />);
  });
  return result!;
}

// ============= Tests =============

describe("DiffTab", () => {
  beforeEach(() => {
    resetMocks();
  });

  describe("summary bar", () => {
    it("shows file count and +/- stats", async () => {
      mockUseDiffReturn.files = [
        makeFile({ path: "a.go", additions: 10, deletions: 3 }),
        makeFile({ path: "b.go", additions: 5, deletions: 2 }),
      ];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 2,
        additions: 15,
        deletions: 5,
      };

      await renderDiffTab(makeAgent());

      expect(screen.getByText("2 files changed")).toBeInTheDocument();
      expect(screen.getByText("+15")).toBeInTheDocument();
      expect(screen.getByText("-5")).toBeInTheDocument();
    });

    it("shows singular 'file' when one file changed", async () => {
      mockUseDiffReturn.files = [makeFile()];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 10,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      expect(screen.getByText("1 file changed")).toBeInTheDocument();
    });

    it("hides +/- pills when counts are zero", async () => {
      mockUseDiffReturn.files = [makeFile({ additions: 0, deletions: 0 })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      expect(screen.queryByText("+0")).not.toBeInTheDocument();
      expect(screen.queryByText("-0")).not.toBeInTheDocument();
    });
  });

  describe("file list", () => {
    it("renders DiffFileRow for each file", async () => {
      mockUseDiffReturn.files = [
        makeFile({ path: "a.go" }),
        makeFile({ path: "b.go" }),
        makeFile({ path: "c.go" }),
      ];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 3,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      const rows = screen.getAllByTestId("file-row");
      expect(rows).toHaveLength(3);
      expect(rows[0]).toHaveAttribute("data-path", "a.go");
      expect(rows[1]).toHaveAttribute("data-path", "b.go");
      expect(rows[2]).toHaveAttribute("data-path", "c.go");
    });

    it("passes correct isExpanded and isViewed props", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };
      mockUseDiffReturn.viewedFiles = new Set(["a.go"]);

      await renderDiffTab(makeAgent());

      const row = screen.getByTestId("file-row");
      expect(row).toHaveAttribute("data-expanded", "false");
      expect(row).toHaveAttribute("data-viewed", "true");
    });

    it("handles empty file list", async () => {
      mockUseDiffReturn.files = [];

      await renderDiffTab(makeAgent());

      expect(screen.getByText("No changes")).toBeInTheDocument();
      expect(screen.queryByTestId("file-row")).not.toBeInTheDocument();
    });
  });

  describe("expand/collapse", () => {
    it("clicking expand shows DiffFileViewer", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      expect(screen.queryByTestId("file-viewer")).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("expand-a.go"));

      expect(screen.getByTestId("file-viewer")).toBeInTheDocument();
    });

    it("clicking again collapses the viewer", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("expand-a.go"));
      expect(screen.getByTestId("file-viewer")).toBeInTheDocument();

      fireEvent.click(screen.getByTestId("expand-a.go"));
      expect(screen.queryByTestId("file-viewer")).not.toBeInTheDocument();
    });

    it("expanding calls fetchPatch", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("expand-a.go"));

      expect(mockFetchPatch).toHaveBeenCalledWith("a.go");
    });

    it("multiple files can be expanded simultaneously", async () => {
      mockUseDiffReturn.files = [
        makeFile({ path: "a.go" }),
        makeFile({ path: "b.go" }),
      ];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 2,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("expand-a.go"));
      fireEvent.click(screen.getByTestId("expand-b.go"));

      const viewers = screen.getAllByTestId("file-viewer");
      expect(viewers).toHaveLength(2);
    });
  });

  describe("viewed toggle", () => {
    it("calls markViewed when viewed control is toggled", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("viewed-a.go"));

      expect(mockMarkViewed).toHaveBeenCalledWith("a.go");
    });

    it("reflects viewedFiles state in row props", async () => {
      mockUseDiffReturn.files = [
        makeFile({ path: "a.go" }),
        makeFile({ path: "b.go" }),
      ];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 2,
        additions: 0,
        deletions: 0,
      };
      mockUseDiffReturn.viewedFiles = new Set(["b.go"]);

      await renderDiffTab(makeAgent());

      const rows = screen.getAllByTestId("file-row");
      expect(rows[0]).toHaveAttribute("data-viewed", "false");
      expect(rows[1]).toHaveAttribute("data-viewed", "true");
    });
  });

  describe("loading state", () => {
    it("shows loading indicator when isLoading", async () => {
      mockUseDiffReturn.isLoading = true;

      await renderDiffTab(makeAgent());

      expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
    });

    it("hides file list during loading", async () => {
      mockUseDiffReturn.isLoading = true;

      await renderDiffTab(makeAgent());

      expect(screen.queryByTestId("file-row")).not.toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("shows error message when error is set", async () => {
      mockUseDiffReturn.error = new Error("Network error");

      await renderDiffTab(makeAgent());

      expect(screen.getByText("Network error")).toBeInTheDocument();
    });

    it("hides file list on error", async () => {
      mockUseDiffReturn.error = new Error("fail");

      await renderDiffTab(makeAgent());

      expect(screen.queryByTestId("file-row")).not.toBeInTheDocument();
    });
  });

  describe("expanded files reset on commitSignal change", () => {
    it("collapses expanded files when agent.ahead changes", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 10,
        deletions: 5,
      };

      const agent = makeAgent({ name: "nova", ahead: 1 });
      let result: ReturnType<typeof render>;
      await act(async () => {
        result = render(<DiffTab agent={agent} isActive={true} />);
      });

      // Expand a file
      fireEvent.click(screen.getByTestId("expand-a.go"));
      expect(screen.getByTestId("file-viewer")).toBeInTheDocument();

      // Re-render with new ahead count (simulating a new commit)
      const updatedAgent = makeAgent({ name: "nova", ahead: 2 });
      await act(async () => {
        result!.rerender(<DiffTab agent={updatedAgent} isActive={true} />);
      });

      // Expanded files should be reset — viewer should be gone
      expect(screen.queryByTestId("file-viewer")).not.toBeInTheDocument();
    });
  });

  describe("per-file patch errors", () => {
    it("per-file patch error does not affect other expanded files", async () => {
      mockUseDiffReturn.files = [
        makeFile({ path: "a.go" }),
        makeFile({ path: "b.go" }),
      ];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 2,
        additions: 0,
        deletions: 0,
      };
      mockUseDiffReturn.patchErrors = new Map([
        ["a.go", new Error("fetch failed for a.go")],
      ]);

      await renderDiffTab(makeAgent());

      // Expand both files
      fireEvent.click(screen.getByTestId("expand-a.go"));
      fireEvent.click(screen.getByTestId("expand-b.go"));

      const viewers = screen.getAllByTestId("file-viewer");
      // File A: has error, no cached patch
      expect(viewers[0]).toHaveAttribute("data-error", "fetch failed for a.go");
      expect(viewers[0]).toHaveAttribute("data-loading", "false");
      // File B: no error, no cached patch — should show loading
      expect(viewers[1]).toHaveAttribute("data-error", "");
      expect(viewers[1]).toHaveAttribute("data-loading", "true");
    });

    it("expanded file with no patch and no error shows loading", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("expand-a.go"));

      const viewer = screen.getByTestId("file-viewer");
      expect(viewer).toHaveAttribute("data-loading", "true");
      expect(viewer).toHaveAttribute("data-error", "");
    });

    it("file with cached patch shows patch, not error or loading", async () => {
      mockUseDiffReturn.files = [makeFile({ path: "a.go" })];
      mockUseDiffReturn.summaryStats = {
        filesChanged: 1,
        additions: 0,
        deletions: 0,
      };
      mockUseDiffReturn.patchCache = new Map([
        [
          "a.go",
          {
            patch: "--- a/a.go\n+++ b/a.go",
            is_binary: false,
            is_too_large: false,
            additions: 1,
            deletions: 0,
          },
        ],
      ]);

      await renderDiffTab(makeAgent());

      fireEvent.click(screen.getByTestId("expand-a.go"));

      const viewer = screen.getByTestId("file-viewer");
      expect(viewer).toHaveAttribute("data-has-patch", "true");
      expect(viewer).toHaveAttribute("data-loading", "false");
      expect(viewer).toHaveAttribute("data-error", "");
    });
  });

  describe("hook invocation", () => {
    it("passes correct agentName and enabled to useDiff", async () => {
      await renderDiffTab(makeAgent({ name: "nova" }), true);

      expect(lastUseDiffOptions.agentName).toBe("nova");
      expect(lastUseDiffOptions.enabled).toBe(true);
    });

    it("passes agent.ahead as commitSignal to useDiff", async () => {
      await renderDiffTab(makeAgent({ name: "nova", ahead: 5 }), true);

      expect(lastUseDiffOptions.commitSignal).toBe(5);
    });

    it("passes commitSignal=0 when agent.ahead is 0", async () => {
      await renderDiffTab(makeAgent({ name: "nova", ahead: 0 }), true);

      expect(lastUseDiffOptions.commitSignal).toBe(0);
    });
  });
});
