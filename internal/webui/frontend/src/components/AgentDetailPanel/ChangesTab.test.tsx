/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the agent's Changes tab (P2.24, D43): the task or lead
 * header, then the changed files with their diffs inline.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { DiffFile, DiffFilePatch } from "@/api/issues";
import type { Issue, LoomAgentStatus } from "@/types";
import type { UseDiffReturn } from "@/hooks/terminal";

import { ChangesTab } from "./ChangesTab";

// ============= Mocks =============

let lastUseDiffOptions: {
  agentName: string | null;
  enabled: boolean;
  commitSignal?: number;
  refreshMs?: number;
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
    useDiff: (opts: typeof lastUseDiffOptions) => {
      lastUseDiffOptions = opts;
      return mockUseDiffReturn;
    },
  };
});

const mockStartedFrom = vi.fn();
vi.mock("@/hooks/api", () => ({
  getTaskStartedFrom: (...args: unknown[]) => mockStartedFrom(...args),
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "ws-1" }),
}));

// Mock sub-components to isolate ChangesTab orchestration
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
    task_id: "T-1",
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

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "T-1",
    title: "Add the login form",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
    ...overrides,
  } as Issue;
}

function resetMocks() {
  mockStartedFrom.mockReset();
  mockStartedFrom.mockResolvedValue({ kind: "trunk" });
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

function tab(
  agent: LoomAgentStatus,
  isActive?: boolean,
  issues: Issue[] = [makeIssue()],
  onOpenTaskChanges?: (task: Issue) => void,
) {
  return (
    <MemoryRouter>
      <ChangesTab
        agent={agent}
        isActive={isActive}
        issues={issues}
        lead="lead-1"
        {...(onOpenTaskChanges ? { onOpenTaskChanges } : {})}
      />
    </MemoryRouter>
  );
}

async function renderDiffTab(
  agent: LoomAgentStatus,
  isActive?: boolean,
  issues?: Issue[],
  onOpenTaskChanges?: (task: Issue) => void,
) {
  let result: ReturnType<typeof render>;
  await act(async () => {
    result = render(tab(agent, isActive, issues, onOpenTaskChanges));
  });
  return result!;
}

// ============= Tests =============

describe("ChangesTab", () => {
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

      expect(screen.getByText("No changes yet")).toBeInTheDocument();
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
    it("calls markViewed when checkbox is toggled", async () => {
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

      expect(screen.getByText(/Loading changes/)).toBeInTheDocument();
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
        result = render(tab(agent, true));
      });

      // Expand a file
      fireEvent.click(screen.getByTestId("expand-a.go"));
      expect(screen.getByTestId("file-viewer")).toBeInTheDocument();

      // Re-render with new ahead count (simulating a new commit)
      const updatedAgent = makeAgent({ name: "nova", ahead: 2 });
      await act(async () => {
        result!.rerender(tab(updatedAgent, true));
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

  describe("task header (P2.24)", () => {
    it("shows the task key, title, status and where it started", async () => {
      mockStartedFrom.mockResolvedValue({ kind: "lead" });

      await renderDiffTab(makeAgent({ status: "working:T-1" }));

      const header = screen.getByTestId("changes-task-header");
      expect(header).toHaveTextContent("T-1");
      expect(header).toHaveTextContent("Add the login form");
      expect(screen.getByTestId("changes-task-status")).toHaveTextContent(
        "Working",
      );
      expect(
        await screen.findByTestId("changes-started-from"),
      ).toHaveTextContent("Started from: the lead's latest work");
      expect(mockStartedFrom).toHaveBeenCalledWith("ws-1", "T-1", "lead-1");
      // No branch names, SHAs, PR or merge buttons.
      expect(header).not.toHaveTextContent("feature-x");
      expect(screen.queryByRole("button", { name: /merge|PR/i })).toBeNull();
      expect(screen.queryByTestId("changes-open-task")).toBeNull();
    });

    it.each([
      [{ kind: "trunk" }, "Started from: trunk"],
      [{ kind: "blocker", task: "T-0" }, "Started from: T-0 Build the API"],
    ])("names the start point %j", async (from, text) => {
      mockStartedFrom.mockResolvedValue(from);

      await renderDiffTab(makeAgent(), true, [
        makeIssue(),
        makeIssue({ id: "T-0", title: "Build the API", status: "closed" }),
      ]);

      expect(
        await screen.findByTestId("changes-started-from"),
      ).toHaveTextContent(text);
    });

    it.each([
      ["review", undefined, "Waiting for review"],
      ["closed", "Approved: code applied", "Approved"],
    ])(
      "links a %s task to its Changes when done",
      async (status, reason, pill) => {
        const onOpen = vi.fn();
        const task = makeIssue({
          status: status as Issue["status"],
          ...(reason ? { close_reason: reason } : {}),
        });

        await renderDiffTab(makeAgent(), true, [task], onOpen);

        expect(screen.getByTestId("changes-task-status")).toHaveTextContent(
          pill,
        );
        fireEvent.click(screen.getByTestId("changes-open-task"));
        expect(onOpen).toHaveBeenCalledWith(task);
      },
    );

    it("keeps the file list live only while the agent works", async () => {
      await renderDiffTab(makeAgent({ status: "working:T-1" }), true);
      expect(lastUseDiffOptions.refreshMs).toBe(5000);

      await renderDiffTab(makeAgent({ status: "ready" }), true);
      expect(lastUseDiffOptions.refreshMs).toBeUndefined();
    });

    it("says No task assigned when the agent has no task", async () => {
      await renderDiffTab(makeAgent({ task_id: undefined }), true, []);

      expect(screen.getByTestId("changes-no-task")).toHaveTextContent(
        "No task assigned",
      );
      expect(lastUseDiffOptions.enabled).toBe(false);
      expect(mockStartedFrom).not.toHaveBeenCalled();
    });
  });

  describe("lead (S16)", () => {
    it("counts the epic's approved tasks and links to the PRs", async () => {
      const approved = (id: string, parent: string) =>
        makeIssue({
          id,
          parent,
          status: "closed",
          close_reason: "Approved: code applied",
        });

      await renderDiffTab(
        makeAgent({ name: "lead-1", role: "lead", parent: "EPIC-1" }),
        true,
        [
          approved("T-1", "EPIC-1"),
          approved("T-2", "EPIC-1"),
          approved("T-9", "EPIC-2"),
          makeIssue({
            id: "T-3",
            parent: "EPIC-1",
            status: "closed",
            close_reason: "No changes",
          }),
        ],
      );

      expect(screen.getByTestId("changes-lead-summary")).toHaveTextContent(
        "2 tasks approved · open PRs →",
      );
      expect(screen.getByTestId("changes-open-prs")).toHaveAttribute(
        "href",
        "/ws/ws-1/prs",
      );
      expect(screen.queryByTestId("changes-task-header")).toBeNull();
      expect(screen.queryByText("No changes yet")).toBeNull();
      expect(lastUseDiffOptions.enabled).toBe(true);
      expect(mockStartedFrom).not.toHaveBeenCalled();
    });
  });
});
