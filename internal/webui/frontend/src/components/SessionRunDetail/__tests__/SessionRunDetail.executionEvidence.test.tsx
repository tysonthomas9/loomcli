/**
 * @vitest-environment jsdom
 */

import { render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSessionDiff, useSessionTranscript } from "@/hooks/terminal";
import type { SessionRecord } from "@/types/agent";

import { SessionRunDetail } from "../SessionRunDetail";

vi.mock("@/hooks/terminal", () => ({
  useSessionTranscript: vi.fn(),
  useSessionDiff: vi.fn(),
}));

vi.mock("@/components/CodeMirrorEditor/CodeMirrorEditor", () => ({
  CodeMirrorEditor: ({ value }: { value: string }) => (
    <div data-testid="codemirror-editor">{value}</div>
  ),
}));

const mockUseSessionTranscript = vi.mocked(useSessionTranscript);
const mockUseSessionDiff = vi.mocked(useSessionDiff);

function createSession(overrides: Partial<SessionRecord> = {}): SessionRecord {
  return {
    session_id: "session-phase-4",
    task_id: "task-phase-4",
    agent_name: "nova",
    backend: "codex",
    model: "gpt-5.6-sol",
    status: "completed",
    started_at: "2026-07-16T10:00:00Z",
    ended_at: "2026-07-16T10:01:00Z",
    duration_s: 60,
    exit_code: 0,
    input_tokens: 10,
    output_tokens: 20,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    attempt_num: 1,
    is_active: false,
    has_transcript: false,
    has_diff: false,
    ...overrides,
  };
}

describe("SessionRunDetail execution evidence", () => {
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

  it("renders the complete Phase 4 execution evidence row", () => {
    render(
      <SessionRunDetail
        taskId="task-phase-4"
        session={createSession({
          runtime_strategy: "local_worktree",
          delivery: "patch_back",
          patch_back_status: "applied_cleanly",
          local_branch: "modular-monolith-phase4",
          github_branch: "ignored-when-local-branch-exists",
          head_sha: "0123456789abcdef0123456789abcdef01234567",
          logs_ref: "artifacts/logs/session-phase-4.jsonl",
          github_pr_url: "https://github.com/example/loomcli/pull/404",
        })}
      />,
    );

    const evidence = screen.getByTestId("session-execution-evidence");

    expect(within(evidence).getByText("Runtime")).toBeInTheDocument();
    expect(within(evidence).getByText("local worktree")).toHaveAttribute(
      "title",
      "local_worktree",
    );
    expect(within(evidence).getByText("Delivery")).toBeInTheDocument();
    expect(within(evidence).getByText("patch back")).toHaveAttribute(
      "title",
      "patch_back",
    );
    expect(within(evidence).getByText("Patch back")).toBeInTheDocument();
    expect(within(evidence).getByText("applied cleanly")).toBeInTheDocument();
    expect(
      within(evidence).getByText("modular-monolith-phase4"),
    ).toHaveAttribute("title", "modular-monolith-phase4");
    expect(
      within(evidence).queryByText("ignored-when-local-branch-exists"),
    ).not.toBeInTheDocument();
    expect(within(evidence).getByText("0123456789ab")).toHaveAttribute(
      "title",
      "0123456789abcdef0123456789abcdef01234567",
    );
    expect(
      within(evidence).getByText("artifacts/logs/session-phase-4.jsonl"),
    ).toHaveAttribute("title", "artifacts/logs/session-phase-4.jsonl");

    const pullRequest = within(evidence).getByRole("link", {
      name: "Open PR",
    });
    expect(pullRequest).toHaveAttribute(
      "href",
      "https://github.com/example/loomcli/pull/404",
    );
    expect(pullRequest).toHaveAttribute("target", "_blank");
    expect(pullRequest).toHaveAttribute("rel", "noreferrer");
  });

  it("omits the execution evidence row for a legacy session record", () => {
    render(
      <SessionRunDetail
        taskId="task-phase-4"
        session={createSession({ session_id: "legacy-session" })}
      />,
    );

    expect(
      screen.queryByTestId("session-execution-evidence"),
    ).not.toBeInTheDocument();
  });

  it("does not turn non-HTTP execution metadata into a link", () => {
    render(
      <SessionRunDetail
        taskId="task-phase-4"
        session={createSession({ github_pr_url: "javascript:alert(1)" })}
      />,
    );

    expect(
      screen.queryByRole("link", { name: "Open PR" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("session-execution-evidence"),
    ).not.toBeInTheDocument();
  });
});
