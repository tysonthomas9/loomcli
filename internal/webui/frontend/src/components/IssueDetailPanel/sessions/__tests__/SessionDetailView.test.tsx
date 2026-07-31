/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord, TranscriptEntry } from "@/types/agent";

import { SessionDetailView } from "../SessionDetailView";

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
    evidence: { status: "ok", usage_status: "reported", conflicts: [] },
    ...overrides,
  };
}

function createEntry(
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

  // ─── Masthead ──────────────────────────────────────────────────────
  describe("masthead", () => {
    it("renders the detail view container", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByTestId("session-detail-view")).toBeInTheDocument();
    });

    it("shows agent name, backend, and model", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("nova")).toBeInTheDocument();
      expect(screen.getByText("claude")).toBeInTheDocument();
      expect(screen.getByText("opus-4")).toBeInTheDocument();
    });

    it("omits the model label when model is absent", () => {
      const session = createSession({ model: undefined });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText("Model:")).not.toBeInTheDocument();
    });

    it("renders stat cards for outcome, exit, duration, tokens, cost", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Outcome")).toBeInTheDocument();
      // Status renders via the shared formatStatusLabel (title-cased).
      expect(screen.getByText("Completed")).toBeInTheDocument();
      expect(screen.getByText("Exit")).toBeInTheDocument();
      expect(screen.getByText("0 (success)")).toBeInTheDocument();
      expect(screen.getByText("Duration")).toBeInTheDocument();
      expect(screen.getByText("Tokens")).toBeInTheDocument();
      expect(screen.getByText("Cost")).toBeInTheDocument();
    });

    it("shows non-zero exit code as a plain number", () => {
      const session = createSession({ exit_code: 1 });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("1")).toBeInTheDocument();
    });

    it("shows failed run error class in an alert banner", () => {
      const session = createSession({
        status: "failed",
        exit_code: 1,
        error_class: "AuthFailure",
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByTestId("run-error-banner")).toHaveTextContent(
        "AuthFailure",
      );
    });

    it("shows explicit run evidence conflicts", () => {
      const session = createSession({
        evidence: {
          status: "conflict",
          usage_status: "conflict",
          conflicts: [
            {
              field: "input_tokens",
              existing_source: "control_plane",
              existing_value: "12",
              incoming_source: "session_store",
              incoming_value: "13",
            },
          ],
        },
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByTestId("run-evidence-conflict")).toHaveTextContent(
        "input_tokens: control_plane reported 12; session_store reported 13",
      );
    });

    it("renders a Files stat when files_changed > 0 with added/removed detail", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Files")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
      expect(screen.getByText(/\+50 -10/)).toBeInTheDocument();
    });

    it("omits the Files stat when no files changed and no lines", () => {
      const session = createSession({
        files_changed: 0,
        lines_added: 0,
        lines_removed: 0,
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.queryByText("Files")).not.toBeInTheDocument();
    });

    it("shows the active badge when session is_active", () => {
      const session = createSession({ is_active: true });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("active")).toBeInTheDocument();
    });

    it("surfaces the initial user text as a Prompt block", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "user",
            type: "text",
            text: "do the thing",
          }),
          createEntry({ seq: 2, role: "assistant", text: "ok" }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Prompt")).toBeInTheDocument();
      expect(screen.getByText("do the thing")).toBeInTheDocument();
    });

    it("renders the prompt as formatted Markdown (heading + inline code), not literal source", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "user",
            type: "text",
            text: "## WORKFLOW\n\nCall `foo.bar()` now.",
          }),
          createEntry({ seq: 2, role: "assistant", text: "ok" }),
        ],
        isLoading: false,
        error: null,
      });
      const { container } = render(
        <SessionDetailView taskId="task-1" session={defaultSession} />,
      );
      // Heading renders as <h2>, not literal "## WORKFLOW"
      const heading = screen.getByRole("heading", { name: "WORKFLOW" });
      expect(heading.tagName).toBe("H2");
      // Inline code renders as <code>, not literal backticks
      const code = container.querySelector("code");
      expect(code).not.toBeNull();
      expect(code).toHaveTextContent("foo.bar()");
      // No literal markdown syntax survives in the DOM text
      expect(container.textContent).not.toContain("## WORKFLOW");
      expect(container.textContent).not.toContain("`foo.bar()`");
    });

    it("does not render a Prompt block when no user text exists", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [createEntry({ seq: 1, role: "assistant", text: "hi" })],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.queryByText("Prompt")).not.toBeInTheDocument();
    });
  });

  // ─── Files Touched ─────────────────────────────────────────────────
  describe("files touched", () => {
    it("shows files-touched section when files exist", () => {
      const session = createSession({
        files_touched: ["src/foo.ts", "src/bar.ts"],
      });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByText("Files Touched (2)")).toBeInTheDocument();
    });

    it("does not show files-touched when empty or undefined", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
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

  // ─── Inner tabs ─────────────────────────────────────────────────────
  describe("inner tabs", () => {
    it("shows Transcript and Diff tabs", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(
        screen.getByTestId("session-inner-tab-transcript"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("session-inner-tab-diff")).toBeInTheDocument();
    });

    it("defaults to the transcript tab", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      const t = screen.getByTestId("session-inner-tab-transcript");
      expect(t.className).toContain("activeInnerTab");
    });

    it("disables diff tab when has_diff is false", () => {
      const session = createSession({ has_diff: false });
      render(<SessionDetailView taskId="task-1" session={session} />);
      expect(screen.getByTestId("session-inner-tab-diff")).toBeDisabled();
    });

    it("switches to diff tab on click", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByTestId("session-diff")).toBeInTheDocument();
      expect(
        screen.queryByTestId("session-transcript"),
      ).not.toBeInTheDocument();
    });
  });

  // ─── Transcript ────────────────────────────────────────────────────
  describe("transcript", () => {
    it("shows loading state with no entries", () => {
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

    it("shows empty state when no entries", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("No transcript entries")).toBeInTheDocument();
    });

    it("renders assistant text without a role label", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [createEntry({ seq: 1, role: "assistant", text: "Hi there" })],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(screen.getByText("Hi there")).toBeInTheDocument();
      // No "assistant" byline in the body
      expect(screen.queryByText(/^assistant$/)).not.toBeInTheDocument();
    });

    it("renders assistant transcript text as formatted Markdown (bold + inline code)", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "text",
            text: "I called **JsonlStateStore** via `add_state`.",
          }),
        ],
        isLoading: false,
        error: null,
      });
      const { container } = render(
        <SessionDetailView taskId="task-1" session={defaultSession} />,
      );
      expect(container.querySelector("strong")).toHaveTextContent(
        "JsonlStateStore",
      );
      expect(container.querySelector("code")).toHaveTextContent("add_state");
      // Literal markdown syntax must not survive
      expect(container.textContent).not.toContain("**JsonlStateStore**");
      expect(container.textContent).not.toContain("`add_state`");
    });

    it("renders JSON transcript envelopes as output text and keeps the raw envelope in details", () => {
      const raw =
        '{"status":"completed","output":"Review complete.\\n\\nNo changes needed."}';
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "text",
            text: raw,
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);

      expect(screen.getByText("Review complete.")).toBeInTheDocument();
      expect(screen.getByText("No changes needed.")).toBeInTheDocument();
      const details = screen.getByText("Raw envelope").closest("details");
      expect(details).not.toHaveAttribute("open");
      expect(details).toHaveTextContent(raw);
    });

    it("renders a user-message interjection as formatted Markdown", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({ seq: 1, role: "user", type: "text", text: "first" }),
          createEntry({ seq: 2, role: "assistant", text: "ok" }),
          createEntry({
            seq: 3,
            role: "user",
            type: "text",
            text: "see `config.yaml`",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      const interjection = screen.getByTestId("transcript-interjection");
      expect(interjection.querySelector("code")).toHaveTextContent(
        "config.yaml",
      );
    });

    it("groups assistant text + tool_use events sharing a uuid into one turn", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "text",
            text: "I will write it.",
            uuid: "a1",
          }),
          createEntry({
            seq: 2,
            role: "assistant",
            type: "tool_use",
            tool_name: "Write",
            tool_input: { file_path: "/tmp/x" },
            tool_use_id: "tu-1",
            uuid: "a1",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // Single turn article containing both events
      const turns = screen.getAllByTestId("transcript-event");
      expect(turns.length).toBeGreaterThan(0);
      expect(screen.getByText("I will write it.")).toBeInTheDocument();
      expect(screen.getByText("1 tool call")).toBeInTheDocument();
    });

    it("renders a collapsed tool pill by default", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "tool_use",
            tool_name: "Read",
            tool_input: { file_path: "/tmp/a" },
            tool_use_id: "t1",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      const pill = screen.getByTestId("tool-pill");
      expect(pill).toBeInTheDocument();
      expect(pill.getAttribute("aria-expanded")).toBe("false");
      // Pill shows tool name + arg preview
      expect(screen.getByText("Read")).toBeInTheDocument();
      expect(screen.getByText("/tmp/a")).toBeInTheDocument();
    });

    it("expands the tool body on pill click, revealing input JSON", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "tool_use",
            tool_name: "Bash",
            tool_input: { command: "ls -la" },
            tool_use_id: "t1",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("tool-pill"));
      expect(screen.getByText(/"command": "ls -la"/)).toBeInTheDocument();
    });

    it("pairs a tool_result with its tool_use inline when expanded", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "assistant",
            type: "tool_use",
            tool_name: "Write",
            tool_input: { file_path: "/tmp/hello.txt" },
            tool_use_id: "tu-42",
          }),
          createEntry({
            seq: 2,
            role: "tool",
            type: "tool_result",
            tool_use_id: "tu-42",
            output: "File created",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("tool-pill"));
      expect(screen.getByText("File created")).toBeInTheDocument();
    });

    it("renders subsequent user text as a user-message interjection", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({ seq: 1, role: "user", type: "text", text: "first" }),
          createEntry({ seq: 2, role: "assistant", text: "ok" }),
          createEntry({ seq: 3, role: "user", type: "text", text: "second" }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // First user text → Prompt (masthead)
      expect(screen.getByText("first")).toBeInTheDocument();
      // Second user text → interjection
      expect(screen.getByTestId("transcript-interjection")).toBeInTheDocument();
      expect(screen.getByText("User message")).toBeInTheDocument();
      expect(screen.getByText("second")).toBeInTheDocument();
    });

    it("does not render tool_result entries as their own turns", () => {
      mockUseSessionTranscript.mockReturnValue({
        entries: [
          createEntry({
            seq: 1,
            role: "tool",
            type: "tool_result",
            tool_use_id: "orphan",
            output: "stray result",
          }),
        ],
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      // Orphan tool_result without matching tool_use = not rendered
      expect(screen.queryByText("stray result")).not.toBeInTheDocument();
    });
  });

  // ─── Diff ───────────────────────────────────────────────────────────
  describe("diff", () => {
    it("shows loading state", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: null,
        isLoading: true,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByText("Loading diff...")).toBeInTheDocument();
    });

    it("shows error state", () => {
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

    it("renders diff in CodeMirrorEditor when present", () => {
      mockUseSessionDiff.mockReturnValue({
        diff: "--- a\n+++ b\n",
        isLoading: false,
        error: null,
      });
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByTestId("codemirror-editor")).toBeInTheDocument();
    });

    it("shows 'No diff available' when diff is null", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      fireEvent.click(screen.getByTestId("session-inner-tab-diff"));
      expect(screen.getByText("No diff available")).toBeInTheDocument();
    });
  });

  // ─── Hook invocations ──────────────────────────────────────────────
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

    it("passes enabled=false to useSessionDiff while on transcript tab", () => {
      render(<SessionDetailView taskId="task-1" session={defaultSession} />);
      expect(mockUseSessionDiff).toHaveBeenCalledWith(
        "task-1",
        "sess-1",
        false,
      );
    });
  });
});
