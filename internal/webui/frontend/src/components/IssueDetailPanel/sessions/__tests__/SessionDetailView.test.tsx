/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionDetailView component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord, TranscriptEntry } from "@/types/agent";

import { SessionDetailView } from "../SessionDetailView";

// Mock hooks
vi.mock("@/hooks/terminal", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/terminal")>(
      "@/hooks/terminal",
    );
  return {
    ...actual,
    useSessionTranscript: vi.fn(),
    useSessionDiff: vi.fn(),
  };
});

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

// Mock CodeMirrorEditor
vi.mock("@/components/CodeMirrorEditor/CodeMirrorEditor", () => ({
  CodeMirrorEditor: ({ value }: { value: string }) => (
    <div data-testid="codemirror-editor">{value}</div>
  ),
}));

import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";

const mockUseSessionTranscript = vi.mocked(useSessionTranscript);
const mockUseSessionDiff = vi.mocked(useSessionDiff);

function createSession(overrides: Partial<SessionRecord> = {}): SessionRecord {
  return {
    session_id: "sess-1",
    task_id: "task-1",
    agent_name: "nova",
    backend: "claude",
    model: "opus-4",
    status: "completed",
    started_at: "2026-01-20T10:00:00Z",
    ended_at: "2026-01-20T10:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.15,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

function createTranscriptEntry(
  overrides: Partial<TranscriptEntry> = {},
): TranscriptEntry {
  return {
    seq: 1,
    timestamp: "2026-01-20T10:00:00Z",
    role: "assistant",
    type: "text",
    text: "Hello world",
    ...overrides,
  };
}

describe("SessionDetailView", () => {
  const defaultSession = createSession();

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseSessionTranscript.mockReturnValue({
      entries: [],
      isLoading: false,
      error: null,
    });
    mockUseSessionDiff.mockReturnValue({
      diff: null,
      isLoading: false,
      error: null,
    });
  });

  describe("metadata summary", () => {
    it("renders the detail view container", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByTestId("session-detail-view")).toBeInTheDocument();
    });

    it("displays model when present", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Model:")).toBeInTheDocument();
      expect(screen.getByText("opus-4")).toBeInTheDocument();
    });

    it("does not display model when absent", () => {
      const session = createSession({ model: undefined });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText("Model:")).not.toBeInTheDocument();
    });

    it("displays exit code 0 as success", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Exit:")).toBeInTheDocument();
      expect(screen.getByText("0 (success)")).toBeInTheDocument();
    });

    it("displays non-zero exit code as number", () => {
      const session = createSession({ exit_code: 1 });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("1")).toBeInTheDocument();
    });

    it("displays files changed when > 0", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Files:")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
    });

    it("does not display files when files_changed is 0", () => {
      const session = createSession({ files_changed: 0 });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText("Files:")).not.toBeInTheDocument();
    });

    it("displays lines added and removed", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("+50 -10")).toBeInTheDocument();
    });

    it("does not display lines when both are 0", () => {
      const session = createSession({ lines_added: 0, lines_removed: 0 });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText(/\+0 -0/)).not.toBeInTheDocument();
    });

    it("displays lines when only added > 0", () => {
      const session = createSession({ lines_added: 10, lines_removed: 0 });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("+10 -0")).toBeInTheDocument();
    });
  });

  describe("files touched", () => {
    it("shows files touched section when files exist", () => {
      const session = createSession({
        files_touched: ["src/foo.ts", "src/bar.ts"],
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("Files Touched (2)")).toBeInTheDocument();
    });

    it("does not show files touched when empty", () => {
      const session = createSession({ files_touched: [] });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText(/Files Touched/)).not.toBeInTheDocument();
    });

    it("does not show files touched when undefined", () => {
      const session = createSession({ files_touched: undefined });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText(/Files Touched/)).not.toBeInTheDocument();
    });

    it("lists each file path", () => {
      const session = createSession({
        files_touched: ["src/a.ts", "src/b.ts"],
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("src/a.ts")).toBeInTheDocument();
      expect(screen.getByText("src/b.ts")).toBeInTheDocument();
    });
  });

  describe("inner tab bar", () => {
    it("shows Transcript and Diff tab buttons", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(
        screen.getByTestId("session-inner-tab-transcript"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("session-inner-tab-diff")).toBeInTheDocument();
    });

    it("defaults to transcript tab active", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      const transcriptTab = screen.getByTestId("session-inner-tab-transcript");
      expect(transcriptTab.className).toContain("activeInnerTab");
    });

    it("disables diff tab when has_diff is false", () => {
      const session = createSession({ has_diff: false });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByTestId("session-inner-tab-diff")).toBeDisabled();
    });

    it("enables diff tab when has_diff is true", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByTestId("session-inner-tab-diff")).not.toBeDisabled();
    });

    it("sets title on diff tab based on has_diff", () => {
      const session = createSession({ has_diff: false });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByTestId("session-inner-tab-diff")).toHaveAttribute(
        "title",
        "No diff available",
      );
    });

    it("switches to diff tab on click", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByTestId("session-diff")).toBeInTheDocument();
      expect(
        screen.queryByTestId("session-transcript"),
      ).not.toBeInTheDocument();
    });

    it("switches back to transcript tab on click", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // Switch to diff
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      // Switch back to transcript
      fireEvent.click(screen.getByTestId("session-inner-tab-transcript"));
      expect(screen.getByTestId("session-transcript")).toBeInTheDocument();
      expect(screen.queryByTestId("session-diff")).not.toBeInTheDocument();
    });
  });

  describe("transcript tab", () => {
    it("shows loading state when loading with no entries", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [],
        isLoading: true,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Loading transcript...")).toBeInTheDocument();
    });

    it("shows error state", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [],
        isLoading: false,
        error: new Error("Network error"),
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(
        screen.getByText("Failed to load transcript: Network error"),
      ).toBeInTheDocument();
    });

    it("shows empty state when no entries and not loading", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("No transcript entries")).toBeInTheDocument();
    });

    it("renders transcript entries", () => {
      const entries = [
        createTranscriptEntry({ seq: 1, role: "user", text: "Hello" }),
        createTranscriptEntry({
          seq: 2,
          role: "assistant",
          text: "Hi there",
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Hello")).toBeInTheDocument();
      expect(screen.getByText("Hi there")).toBeInTheDocument();
    });

    it("renders role labels for entries", () => {
      const entries = [
        createTranscriptEntry({ seq: 1, role: "user", text: "test" }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("user")).toBeInTheDocument();
    });

    it("renders tool name for tool_use entries", () => {
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: { file_path: "/tmp/x" },
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Tool: Read")).toBeInTheDocument();
    });

    it("renders tool_input JSON for tool_use entries", () => {
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Bash",
          text: undefined,
          tool_input: { command: "ls -la" },
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText(/"command": "ls -la"/)).toBeInTheDocument();
    });

    it("does not show loading text when entries exist during loading", () => {
      const entries = [createTranscriptEntry({ seq: 1, text: "existing" })];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: true,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(
        screen.queryByText("Loading transcript..."),
      ).not.toBeInTheDocument();
      expect(screen.getByText("existing")).toBeInTheDocument();
    });
  });

  describe("diff tab", () => {
    it("shows loading state when diff is loading", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: null,
        isLoading: true,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByText("Loading diff...")).toBeInTheDocument();
    });

    it("shows error state for diff", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: null,
        isLoading: false,
        error: new Error("Diff fetch failed"),
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(
        screen.getByText("Failed to load diff: Diff fetch failed"),
      ).toBeInTheDocument();
    });

    it("shows diff content in CodeMirrorEditor", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: "--- a/file.ts\n+++ b/file.ts\n@@ -1 +1 @@\n-old\n+new",
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByTestId("codemirror-editor")).toBeInTheDocument();
    });

    it("shows 'No diff available' when diff is null", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: null,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByText("No diff available")).toBeInTheDocument();
    });
  });

  describe("tool_input truncation", () => {
    it("truncates tool_input longer than 2000 chars", () => {
      const longInput = "x".repeat(10000);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: longInput,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // Full text should NOT be present
      expect(screen.queryByText(longInput)).not.toBeInTheDocument();
      // Show full input button should be present
      expect(screen.getByTestId("show-full-input")).toBeInTheDocument();
    });

    it("does not truncate tool_input under 2000 chars", () => {
      const shortInput = "y".repeat(500);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: shortInput,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText(shortInput)).toBeInTheDocument();
      expect(screen.queryByTestId("show-full-input")).not.toBeInTheDocument();
    });

    it("does not truncate tool_input of exactly 2000 chars", () => {
      const exactInput = "z".repeat(2000);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: exactInput,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText(exactInput)).toBeInTheDocument();
      expect(screen.queryByTestId("show-full-input")).not.toBeInTheDocument();
    });

    it("expands tool_input on 'Show full input' click", () => {
      const longInput = "a".repeat(10000);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: longInput,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("show-full-input"));
      expect(screen.getByText(longInput)).toBeInTheDocument();
      expect(screen.getByTestId("show-less-input")).toBeInTheDocument();
      expect(screen.queryByTestId("show-full-input")).not.toBeInTheDocument();
    });

    it("collapses tool_input on 'Show less' click", () => {
      const longInput = "b".repeat(10000);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "tool_use",
          tool_name: "Read",
          text: undefined,
          tool_input: longInput,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("show-full-input"));
      fireEvent.click(screen.getByTestId("show-less-input"));
      expect(screen.queryByText(longInput)).not.toBeInTheDocument();
      expect(screen.getByTestId("show-full-input")).toBeInTheDocument();
    });

    it("does not truncate text entries (only tool_use inputs and tool_result outputs)", () => {
      const longText = "c".repeat(10000);
      const entries = [
        createTranscriptEntry({
          seq: 1,
          role: "assistant",
          type: "text",
          text: longText,
        }),
      ];
      mockUseSessionTranscript.mockReturnValue({
        entries,
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText(longText)).toBeInTheDocument();
      expect(screen.queryByTestId("show-full-input")).not.toBeInTheDocument();
    });
  });

  describe("hook invocations", () => {
    it("passes correct args to useSessionTranscript", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(mockUseSessionTranscript).toHaveBeenCalledWith(
        "task-1",
        "sess-1",
        false,
      );
    });

    it("passes is_active=true to useSessionTranscript for active sessions", () => {
      const session = createSession({ is_active: true });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(mockUseSessionTranscript).toHaveBeenCalledWith(
        "task-1",
        "sess-1",
        true,
      );
    });

    it("passes enabled=false to useSessionDiff when on transcript tab", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // On transcript tab, diff should NOT be fetched (enabled = innerTab === "diff" && has_diff)
      // Since innerTab defaults to "transcript", enabled should be false
      expect(mockUseSessionDiff).toHaveBeenCalledWith(
        "task-1",
        "sess-1",
        false,
      );
    });
  });
});
