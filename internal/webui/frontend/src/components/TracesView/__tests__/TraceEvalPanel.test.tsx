/**
 * @vitest-environment jsdom
 */
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { SessionEvalRecord, SessionEvalState } from "@/types";
import { useToast } from "@/hooks/ui";

import { TraceEvalPanel } from "../TraceEvalPanel";

vi.mock("@/hooks/ui", () => ({ useToast: vi.fn() }));

const mockUseToast = vi.mocked(useToast);

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
    judge_session_id: "judge-sess-1",
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

function renderPanel(
  evalState: SessionEvalState,
  overrides: Partial<{
    kind: string;
    isLoading: boolean;
    isRejudging: boolean;
    error: Error | null;
    requestRejudge: ReturnType<typeof vi.fn>;
    onOpenJudge: ReturnType<typeof vi.fn>;
  }> = {},
) {
  const requestRejudge =
    overrides.requestRejudge ??
    vi.fn().mockResolvedValue({ requested: true, binding_enabled: true });
  const onOpenJudge = overrides.onOpenJudge ?? vi.fn();
  const result = render(
    <TraceEvalPanel
      sessionId="sess-1"
      {...(overrides.kind ? { kind: overrides.kind } : {})}
      evalState={evalState}
      isLoading={overrides.isLoading ?? false}
      isRejudging={overrides.isRejudging ?? false}
      error={overrides.error ?? null}
      requestRejudge={requestRejudge}
      onOpenJudge={onOpenJudge}
    />,
  );
  return { ...result, requestRejudge, onOpenJudge };
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
    renderPanel({ eval_status: "none", eval_requested: true, eval: null });

    expect(screen.getByText("Not evaluated yet")).toBeInTheDocument();
    expect(screen.getAllByText("Re-judge requested").length).toBeGreaterThan(0);
  });

  it("renders the failed state with error class and prompt version", () => {
    renderPanel({
      eval_status: "failed",
      eval_error_class: "transcript_too_large",
      eval_prompt_version: "v1",
      eval_requested: false,
      eval: null,
    });

    expect(screen.getByText("transcript_too_large")).toBeInTheDocument();
    expect(screen.getByText("Prompt v1")).toBeInTheDocument();
  });

  it("renders the eval and cross-links its exact judge session", () => {
    const { onOpenJudge } = renderPanel({
      eval_status: "done",
      eval_requested: false,
      eval: doneEval(),
    });

    expect(screen.getAllByText("Outcome success").length).toBeGreaterThan(0);
    expect(screen.getByText("verification_skipped")).toBeInTheDocument();
    expect(screen.getByText("Judge says good.")).toBeInTheDocument();
    expect(screen.getByText(/1.5k tokens/)).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "View judge transcript" }),
    );
    expect(onOpenJudge).toHaveBeenCalledWith("judge-sess-1");
  });

  it("hides re-judge for judge sessions", () => {
    renderPanel(
      { eval_status: "none", eval_requested: false, eval: null },
      { kind: "judge" },
    );

    expect(screen.queryByTestId("trace-eval-rejudge")).not.toBeInTheDocument();
  });

  it("warns when rejudge is queued while evals are paused", async () => {
    const requestRejudge = vi.fn().mockResolvedValue({
      requested: true,
      binding_enabled: false,
    });
    renderPanel(
      { eval_status: "none", eval_requested: false, eval: null },
      { requestRejudge },
    );

    await act(async () => {
      fireEvent.click(screen.getByTestId("trace-eval-rejudge"));
    });

    expect(showToast).toHaveBeenCalledWith(
      "evals are paused — the request queues until re-enabled",
      { type: "warning" },
    );
  });

  it("toasts a rejudge rejection without replacing the eval state", async () => {
    const requestRejudge = vi
      .fn()
      .mockRejectedValue(new Error("not an eval candidate"));
    renderPanel(
      { eval_status: "none", eval_requested: false, eval: null },
      { requestRejudge },
    );

    await act(async () => {
      fireEvent.click(screen.getByTestId("trace-eval-rejudge"));
    });

    expect(showToast).toHaveBeenCalledWith(
      "Re-judge rejected: not an eval candidate",
      { type: "error" },
    );
    expect(screen.getByText("Not evaluated yet")).toBeInTheDocument();
  });
});
