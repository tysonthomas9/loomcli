/**
 * @vitest-environment jsdom
 */
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { SessionEvalRecord, SessionEvalState } from "@/types";
import { useSessionEval } from "@/hooks/evals";
import { useToast } from "@/hooks/ui";

import { TraceEvalPanel } from "../TraceEvalPanel";

vi.mock("@/hooks/evals", () => ({
  useSessionEval: vi.fn(),
}));

vi.mock("@/hooks/ui", () => ({
  useToast: vi.fn(),
}));

const mockUseSessionEval = vi.mocked(useSessionEval);
const mockUseToast = vi.mocked(useToast);

function mockEvalHook(state: SessionEvalState, overrides = {}) {
  mockUseSessionEval.mockReturnValue({
    evalState: state,
    isLoading: false,
    isRejudging: false,
    error: null,
    refetch: vi.fn().mockResolvedValue(undefined),
    requestRejudge: vi.fn().mockResolvedValue({
      requested: true,
      binding_enabled: true,
    }),
    ...overrides,
  });
}

function doneEval(overrides?: Partial<SessionEvalRecord>): SessionEvalRecord {
  return {
    eval_id: "eval-sess-1-v2",
    session_id: "sess-1",
    agent_id: "coder",
    workspace_key: "WS",
    scores: {
      outcome_success: 91,
      instruction_adherence: 88,
      efficiency: 72,
      tool_use_quality: 95,
    },
    score_rationales: {
      outcome_success: "The task was completed.",
      instruction_adherence: "The agent followed instructions.",
      efficiency: "The agent used few turns.",
      tool_use_quality: "Tool calls were appropriate.",
    },
    error_taxonomy_tags: ["verification_skipped"],
    improvement_categories: {
      harness: ["Add a harness fixture."],
      linter: [],
      prompt: ["Clarify acceptance criteria."],
      skill: [],
    },
    judge_summary: "Judge says good.",
    judge_model: "codex-mini",
    judge_prompt_version: "v2",
    eval_cost: {
      input_tokens: 1000,
      output_tokens: 500,
      total_tokens: 1500,
    },
    session_started_at: "2026-07-17T10:00:00Z",
    session_ended_at: "2026-07-17T10:10:00Z",
    created_at: "2026-07-17T10:11:00Z",
    updated_at: "2026-07-17T10:11:00Z",
    ...overrides,
  };
}

describe("TraceEvalPanel", () => {
  const showToast = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseToast.mockReturnValue({
      showToast,
      toasts: [],
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    });
  });

  it("renders the none state with a requested badge", () => {
    mockEvalHook({
      eval_status: "none",
      eval_requested: true,
      eval: null,
    });

    render(<TraceEvalPanel sessionId="sess-1" enabled />);

    expect(screen.getByText("Not evaluated yet")).toBeInTheDocument();
    expect(screen.getAllByText("Re-judge requested").length).toBeGreaterThan(0);
  });

  it("renders the failed state with error class and prompt version", () => {
    mockEvalHook({
      eval_status: "failed",
      eval_error_class: "transcript_too_large",
      eval_prompt_version: "v1",
      eval_requested: false,
      eval: null,
    });

    render(<TraceEvalPanel sessionId="sess-1" enabled />);

    expect(screen.getByText("transcript_too_large")).toBeInTheDocument();
    expect(screen.getByText("Prompt v1")).toBeInTheDocument();
  });

  it("renders done eval scores, tags, summary, rationales, insights, and cost", () => {
    mockEvalHook({
      eval_status: "done",
      eval_requested: false,
      eval: doneEval(),
    });

    render(<TraceEvalPanel sessionId="sess-1" enabled />);

    expect(screen.getAllByText("Outcome success").length).toBeGreaterThan(0);
    expect(screen.getByText("verification_skipped")).toBeInTheDocument();
    expect(screen.getByText("Judge says good.")).toBeInTheDocument();
    expect(screen.getByText("Score rationales")).toBeInTheDocument();
    expect(screen.getByText("Add a harness fixture.")).toBeInTheDocument();
    expect(screen.getByText("codex-mini")).toBeInTheDocument();
    expect(screen.getByText(/1.5k tokens/)).toBeInTheDocument();
  });

  it("warns when rejudge is queued while evals are paused", async () => {
    const requestRejudge = vi.fn().mockResolvedValue({
      requested: true,
      binding_enabled: false,
    });
    mockEvalHook(
      {
        eval_status: "none",
        eval_requested: false,
        eval: null,
      },
      { requestRejudge },
    );

    render(<TraceEvalPanel sessionId="sess-1" enabled />);

    await act(async () => {
      fireEvent.click(screen.getByTestId("trace-eval-rejudge"));
    });

    expect(requestRejudge).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledWith(
      "evals are paused — the request queues until re-enabled",
      { type: "warning" },
    );
    expect(
      screen.getByText(
        "evals are paused — the request queues until re-enabled",
      ),
    ).toBeInTheDocument();
  });

  it("toasts the rejection reason when rejudge is rejected, without a load error", async () => {
    const requestRejudge = vi
      .fn()
      .mockRejectedValue(
        new Error('not an eval candidate: session "s1" has no transcript_ref'),
      );
    mockEvalHook(
      {
        eval_status: "none",
        eval_requested: false,
        eval: null,
      },
      { requestRejudge },
    );

    render(<TraceEvalPanel sessionId="sess-1" enabled />);

    await act(async () => {
      fireEvent.click(screen.getByTestId("trace-eval-rejudge"));
    });

    expect(showToast).toHaveBeenCalledWith(
      'Re-judge rejected: not an eval candidate: session "s1" has no transcript_ref',
      { type: "error" },
    );
    expect(screen.queryByText(/Failed to load eval/)).not.toBeInTheDocument();
    expect(screen.getByText("Not evaluated yet")).toBeInTheDocument();
  });
});
