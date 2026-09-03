/**
 * @vitest-environment jsdom
 */
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type {
  SessionEvalRecord,
  SessionEvalState,
  WorkspaceSessionFilters,
  WorkspaceSessionListItem,
  WorkspaceTraceRunData,
} from "@/types";
import {
  useWorkspaceSession,
  useWorkspaceSessionDiff,
  useWorkspaceSessionSubagents,
  useWorkspaceSessionTranscript,
  useWorkspaceSessions,
  useWorkspaceSubagentTranscript,
  useWorkspaceTraceRun,
} from "@/hooks/terminal";
import { useSessionEval } from "@/hooks/evals";
import { useToast } from "@/hooks/ui";

import { getTruncationBannerText, TracesView } from "../TracesView";

vi.mock("@/components/TranscriptView", () => ({
  formatDuration: (value: number | undefined) => `${value ?? 0}s`,
  formatTokens: (value: number) => `${value} tokens`,
  TranscriptView: ({
    entries,
    toolbar,
    footer,
    showTranscript = true,
  }: {
    entries?: unknown[];
    toolbar?: React.ReactNode;
    footer?: React.ReactNode;
    showTranscript?: boolean;
  }) => (
    <div data-testid="mock-transcript-view">
      {toolbar}
      {showTranscript && (
        <div data-testid="mock-transcript-entries">
          {JSON.stringify(entries ?? [])}
        </div>
      )}
      {footer}
    </div>
  ),
}));

vi.mock("@/components/CodeMirrorEditor", () => ({
  CodeMirrorEditor: ({ value }: { value: string }) => <pre>{value}</pre>,
}));

vi.mock("@/hooks/terminal", () => ({
  useWorkspaceSession: vi.fn(),
  useWorkspaceSessionDiff: vi.fn(),
  useWorkspaceSessionSubagents: vi.fn(),
  useWorkspaceSessionTranscript: vi.fn(),
  useWorkspaceSessions: vi.fn(),
  useWorkspaceSubagentTranscript: vi.fn(),
  useWorkspaceTraceRun: vi.fn(),
}));

vi.mock("@/hooks/evals", () => ({ useSessionEval: vi.fn() }));
vi.mock("@/hooks/ui", () => ({ useToast: vi.fn() }));

const mockUseWorkspaceSessions = vi.mocked(useWorkspaceSessions);
const mockUseWorkspaceSession = vi.mocked(useWorkspaceSession);
const mockUseWorkspaceSessionDiff = vi.mocked(useWorkspaceSessionDiff);
const mockUseWorkspaceSessionSubagents = vi.mocked(
  useWorkspaceSessionSubagents,
);
const mockUseWorkspaceSubagentTranscript = vi.mocked(
  useWorkspaceSubagentTranscript,
);
const mockUseWorkspaceSessionTranscript = vi.mocked(
  useWorkspaceSessionTranscript,
);
const mockUseWorkspaceTraceRun = vi.mocked(useWorkspaceTraceRun);
const mockUseSessionEval = vi.mocked(useSessionEval);
const mockUseToast = vi.mocked(useToast);

let listSessions: WorkspaceSessionListItem[] = [];
let listScoreDimensions: string[] = [];
let evalState: SessionEvalState = {
  eval_status: "none",
  eval_requested: false,
  eval: null,
};
let traceRun: WorkspaceTraceRunData | null = null;

function session(
  id: string,
  overrides: Partial<WorkspaceSessionListItem> = {},
): WorkspaceSessionListItem {
  return {
    session_id: id,
    task_id: "TASK-1",
    agent_name: "coder",
    kind: "task",
    backend: "codex",
    started_at: "2026-07-20T10:00:00Z",
    ended_at: "2026-07-20T10:01:00Z",
    duration_s: 60,
    status: "completed",
    exit_code: 0,
    input_tokens: 10,
    output_tokens: 5,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 1,
    lines_added: 2,
    lines_removed: 1,
    attempt_num: 1,
    is_active: false,
    has_transcript: true,
    has_diff: false,
    ...overrides,
  };
}

function doneEval(
  overrides: Partial<SessionEvalRecord> = {},
): SessionEvalRecord {
  return {
    eval_id: "eval-1",
    session_id: "sess-task",
    agent_id: "coder",
    workspace_key: "WS",
    scores: {
      outcome_success: 90,
      instruction_adherence: 80,
      efficiency: 70,
      tool_use_quality: 60,
    },
    score_rationales: {
      outcome_success: "ok",
      instruction_adherence: "ok",
      efficiency: "ok",
      tool_use_quality: "ok",
    },
    error_taxonomy_tags: [],
    improvement_categories: { harness: [], linter: [], prompt: [], skill: [] },
    judge_summary: "done",
    judge_model: "codex-mini",
    judge_prompt_version: "v2",
    judge_session_id: "judge-99",
    eval_cost: { input_tokens: 1, output_tokens: 1, total_tokens: 2 },
    session_started_at: "2026-07-20T10:00:00Z",
    session_ended_at: "2026-07-20T10:01:00Z",
    created_at: "2026-07-20T10:02:00Z",
    updated_at: "2026-07-20T10:02:00Z",
    ...overrides,
  };
}

function LocationDisplay(): JSX.Element {
  const location = useLocation();
  return (
    <div data-testid="location">
      {location.pathname}
      {location.search}
    </div>
  );
}

function TestRoutes(): JSX.Element {
  const element = (
    <>
      <TracesView />
      <LocationDisplay />
    </>
  );
  return (
    <Routes>
      <Route path="/ws/:workspaceId/traces" element={element} />
      <Route path="/ws/:workspaceId/traces/runs/:taskRunId" element={element} />
    </Routes>
  );
}

function renderAt(url = "/ws/WS/traces") {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <TestRoutes />
    </MemoryRouter>,
  );
}

function mockListResult(
  filters?: (value: WorkspaceSessionFilters) => WorkspaceSessionListItem[],
) {
  mockUseWorkspaceSessions.mockImplementation((value) => ({
    sessions: filters ? filters(value) : listSessions,
    total: listSessions.length,
    limit: 200,
    scoreDimensions: listScoreDimensions,
    isLoading: false,
    error: null,
    refetch: vi.fn().mockResolvedValue(undefined),
  }));
}

describe("TracesView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listSessions = [];
    listScoreDimensions = [];
    traceRun = null;
    evalState = { eval_status: "none", eval_requested: false, eval: null };

    mockListResult();
    mockUseWorkspaceSession.mockImplementation((sessionId) => ({
      session:
        listSessions.find((item) => item.session_id === sessionId) ?? null,
      isLoading: false,
      error: null,
    }));
    mockUseWorkspaceSessionDiff.mockReturnValue({
      diff: null,
      isLoading: false,
      error: null,
    });
    mockUseWorkspaceSessionSubagents.mockReturnValue({
      subagentIds: [],
      isLoading: false,
      error: null,
    });
    mockUseWorkspaceSubagentTranscript.mockReturnValue({
      entries: [],
      isLoading: false,
      error: null,
    });
    mockUseWorkspaceSessionTranscript.mockImplementation((sessionId) => ({
      entries: sessionId
        ? [
            {
              seq: 1,
              role: "assistant",
              type: "text",
              text: `transcript:${sessionId}`,
            },
          ]
        : [],
      isLoading: false,
      error: null,
    }));
    mockUseWorkspaceTraceRun.mockImplementation(() => ({
      run: traceRun,
      isLoading: false,
      error: null,
    }));
    mockUseSessionEval.mockImplementation(() => ({
      evalState,
      isLoading: false,
      isRejudging: false,
      error: null,
      refetch: vi.fn().mockResolvedValue(undefined),
      requestRejudge: vi.fn().mockResolvedValue({
        requested: true,
        binding_enabled: true,
      }),
    }));
    mockUseToast.mockReturnValue({
      showToast: vi.fn(),
      toasts: [],
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    });
  });

  it("renders run, attempt, invocation, tags, dynamic scores, and daemon fallbacks", () => {
    listSessions = [
      session("sess-task", {
        task_run_id: "run-1",
        attempt: 2,
        invocation_key: "review",
        tags: ["frontend"],
        eval_scores: { efficiency: 87 },
      }),
      session("daemon-1", { kind: "terminal", task_run_id: "" }),
      session("legacy-1", {
        task_run_id: "legacy-run",
        attempt: 0,
        invocation_key: "",
      }),
      session("zero-a0", {
        task_run_id: "run-2",
        attempt: 0,
        invocation_key: "worker",
      }),
    ];
    listScoreDimensions = ["efficiency", "new_dimension"];
    mockListResult();

    renderAt();

    const headers = within(screen.getByTestId("trace-session-table"))
      .getAllByRole("columnheader")
      .map((header) => header.textContent);
    expect(headers).toEqual(
      expect.arrayContaining([
        "Run",
        "Attempt",
        "Invocation",
        "Tags",
        "efficiency",
        "new dimension",
      ]),
    );
    expect(screen.getByText("87")).toBeInTheDocument();
    const daemonRow = screen.getByText("daemon-1").closest("tr");
    expect(daemonRow).not.toBeNull();
    expect(
      within(daemonRow!)
        .getAllByRole("cell")
        .slice(1, 4)
        .map((cell) => cell.textContent),
    ).toEqual(["-", "-", "-"]);
    const legacyRow = screen.getByText("legacy-1").closest("tr");
    expect(
      within(legacyRow!)
        .getAllByRole("cell")
        .slice(1, 4)
        .map((cell) => cell.textContent),
    ).toEqual(["legacy-run", "-", "-"]);
    const taskRow = screen.getByText("sess-task").closest("tr");
    expect(
      within(taskRow!)
        .getAllByRole("cell")
        .slice(1, 4)
        .map((cell) => cell.textContent),
    ).toEqual(["run-1", "2", "review"]);
    // Missing listed dimension renders "-" (sess-task has no new_dimension score).
    expect(
      within(taskRow!)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toContain("-");
    // Post-migration attempt 0 with an invocation key is a real value, not "-".
    const zeroRow = screen.getByText("zero-a0").closest("tr");
    expect(
      within(zeroRow!)
        .getAllByRole("cell")
        .slice(1, 4)
        .map((cell) => cell.textContent),
    ).toEqual(["run-2", "0", "worker"]);
  });

  it("opens a closable side panel and expands a run-linked session", () => {
    listSessions = [
      session("sess-task", {
        task_run_id: "run-1",
        attempt: 0,
        invocation_key: "agent",
      }),
    ];
    mockListResult();

    renderAt();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("sess-task"));
    const panel = screen.getByRole("complementary");
    expect(
      within(panel).getByRole("button", { name: "Expand" }),
    ).toBeInTheDocument();

    const tabs = within(screen.getByTestId("trace-detail-tabs"))
      .getAllByRole("button")
      .map((button) => button.textContent);
    expect(tabs).toEqual(["Eval", "Transcript", "Diff", "Judge"]);
    expect(screen.getByRole("button", { name: "Eval" })).toHaveAttribute(
      "data-active",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: "Close trace detail" }));
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("sess-task"));
    fireEvent.click(screen.getByRole("button", { name: "Expand" }));
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/ws/WS/traces/runs/run-1?session=sess-task",
    );
  });

  it("navigates directly from a Run cell to the registered run view", () => {
    listSessions = [
      session("sess-task", {
        task_run_id: "run-1",
        attempt: 0,
        invocation_key: "agent",
      }),
    ];
    mockListResult();

    renderAt();
    fireEvent.click(screen.getByRole("button", { name: "run-1" }));

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/ws/WS/traces/runs/run-1",
    );
  });

  it("omits Expand for a run-less daemon session", () => {
    listSessions = [session("daemon-1", { kind: "terminal", task_run_id: "" })];
    mockListResult();

    renderAt();
    fireEvent.click(screen.getByText("daemon-1"));

    expect(
      screen.queryByRole("button", { name: "Expand" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Transcript" })).toHaveAttribute(
      "data-active",
      "true",
    );
  });

  it("uses the exact displayed eval judge link and disables Judge without an eval", () => {
    listSessions = [session("sess-task", { task_run_id: "run-1" })];
    mockListResult();
    const { rerender } = renderAt();
    fireEvent.click(screen.getByText("sess-task"));
    expect(screen.getByRole("button", { name: "Judge" })).toBeDisabled();

    evalState = {
      eval_status: "done",
      eval_requested: false,
      eval: doneEval({ judge_session_id: "judge-exact" }),
    };
    rerender(
      <MemoryRouter initialEntries={["/ws/WS/traces"]}>
        <TestRoutes />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Judge" }));

    expect(mockUseWorkspaceSessionTranscript).toHaveBeenCalledWith(
      "judge-exact",
      false,
    );

    evalState = { eval_status: "none", eval_requested: true, eval: null };
    rerender(
      <MemoryRouter initialEntries={["/ws/WS/traces"]}>
        <TestRoutes />
      </MemoryRouter>,
    );
    expect(screen.getByRole("button", { name: "Judge" })).toBeDisabled();
    expect(
      screen.getByText("No judge transcript is linked to the current eval."),
    ).toBeInTheDocument();
  });

  it("hides judges by default and reveals them through Kind=Judge", () => {
    const task = session("sess-task");
    const judge = session("sess-judge", { kind: "judge" });
    listSessions = [task, judge];
    mockListResult((filters) =>
      filters.kind === "judge" ? [judge] : [task, judge],
    );

    renderAt();
    expect(screen.getByText("sess-task")).toBeInTheDocument();
    expect(screen.queryByText("sess-judge")).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Kind"), {
      target: { value: "judge" },
    });
    expect(screen.getByText("sess-judge")).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("?kind=judge");
  });

  it("defaults judge detail to Transcript and hides its re-judge action", () => {
    listSessions = [
      session("sess-judge", {
        kind: "judge",
        judged_session_id: "subject-1",
      }),
    ];
    mockListResult();

    renderAt("/ws/WS/traces?kind=judge");
    fireEvent.click(screen.getByText("sess-judge"));
    expect(screen.getByRole("button", { name: "Transcript" })).toHaveAttribute(
      "data-active",
      "true",
    );
    expect(
      screen.getByRole("button", { name: "Judged session: subject-1" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Eval" }));
    expect(screen.queryByTestId("trace-eval-rejudge")).not.toBeInTheDocument();
  });

  it("round-trips AND tag pills through chips and repeatable URL params", () => {
    listSessions = [session("sess-task", { tags: ["alpha", "beta"] })];
    mockListResult();

    renderAt();
    fireEvent.click(screen.getByRole("button", { name: "alpha" }));
    fireEvent.click(screen.getByRole("button", { name: "beta" }));

    expect(screen.getByText("Tag: alpha")).toBeInTheDocument();
    expect(screen.getByText("Tag: beta")).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent(
      "?tag=alpha&tag=beta",
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear Tag: alpha" }));
    expect(screen.getByTestId("location")).toHaveTextContent("?tag=beta");
  });

  it("renders and clears the URL-only run filter chip back to plain traces", () => {
    renderAt("/ws/WS/traces?task_run_id=run-1&tag=alpha");

    expect(screen.getByText("Run: run-1")).toBeInTheDocument();
    expect(mockUseWorkspaceSessions).toHaveBeenCalledWith(
      expect.objectContaining({ task_run_id: "run-1", tags: ["alpha"] }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear Run: run-1" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/ws/WS/traces");
    expect(screen.getByTestId("location")).not.toHaveTextContent("?");
  });

  it("renders TaskRun truth and fixes selected detail below the run table", () => {
    const runSession = session("run-session", {
      task_run_id: "run-1",
      attempt: 1,
      invocation_key: "agent",
      eval_scores: { efficiency: 75 },
    });
    // An eval run's own judge session: the run view shows its sessions
    // unfiltered — the list's judges-hidden default must not apply here.
    const runJudgeSession = session("run-judge", {
      kind: "judge",
      task_run_id: "run-1",
      attempt: 1,
      invocation_key: "judge",
    });
    listSessions = [runSession];
    traceRun = {
      task_run_id: "run-1",
      task_run: {
        workspace_key: "WS",
        task_run_id: "run-1",
        task_id: "TASK-1",
        status: "completed",
        created_at: "2026-07-20T10:00:00Z",
        updated_at: "2026-07-20T10:01:00Z",
      },
      task_run_missing: false,
      task_id: "TASK-1",
      attempt_count: 2,
      files_changed: 3,
      total_tokens: 123,
      duration_seconds: 42,
      sessions: [runSession, runJudgeSession],
    };

    renderAt("/ws/WS/traces/runs/run-1?session=run-session");

    expect(screen.getByRole("heading", { name: "run-1" })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "← All traces" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "view in list ↗" }),
    ).toHaveAttribute("href", "/ws/WS/traces?task_run_id=run-1");
    expect(screen.getByText("123 tokens")).toBeInTheDocument();
    expect(
      within(screen.getByTestId("trace-session-table")).queryByRole(
        "columnheader",
        { name: "Run" },
      ),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Selected session detail" }),
    ).toBeInTheDocument();
    expect(screen.getByText("run-judge")).toBeInTheDocument();
  });

  it("shows an existing run header with a zero-session empty state", () => {
    traceRun = {
      task_run_id: "run-empty",
      task_run: {
        workspace_key: "WS",
        task_run_id: "run-empty",
        task_id: "TASK-1",
        status: "completed",
        created_at: "2026-07-20T10:00:00Z",
        updated_at: "2026-07-20T10:01:00Z",
      },
      task_run_missing: false,
      task_id: "TASK-1",
      attempt_count: 0,
      files_changed: 0,
      total_tokens: 0,
      duration_seconds: 0,
      sessions: [],
    };

    renderAt("/ws/WS/traces/runs/run-empty");
    expect(
      screen.getByRole("heading", { name: "run-empty" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No sessions were recorded for this run."),
    ).toBeInTheDocument();
  });

  it("shows a sessions-derived header when the task run record is missing", () => {
    const orphan = session("orphan-session", {
      task_run_id: "run-missing",
      attempt: 0,
      invocation_key: "agent",
    });
    traceRun = {
      task_run_id: "run-missing",
      task_run_missing: true,
      task_id: "TASK-1",
      attempt_count: 1,
      files_changed: 1,
      total_tokens: 0,
      duration_seconds: 0,
      sessions: [orphan],
    };

    renderAt("/ws/WS/traces/runs/run-missing");

    expect(
      screen.getByRole("heading", { name: "run-missing" }),
    ).toBeInTheDocument();
    expect(screen.getByText("task run record missing")).toBeInTheDocument();
    expect(screen.getByText("orphan-session")).toBeInTheDocument();
    expect(
      screen.queryByText("No sessions were recorded for this run."),
    ).not.toBeInTheDocument();
  });
});

describe("getTruncationBannerText", () => {
  it("returns the mandated newest-of-total banner when results are truncated", () => {
    expect(getTruncationBannerText(275, 200, 200)).toBe(
      "showing newest 200 of 275 in this range — narrow the time range",
    );
  });

  it("returns null when all matching sessions are shown", () => {
    expect(getTruncationBannerText(12, 12, 200)).toBeNull();
  });
});
